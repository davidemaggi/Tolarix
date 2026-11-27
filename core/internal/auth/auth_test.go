package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/store"
)

// cheap parameters keep the tests fast.
var testParams = Params{Memory: 1024, Time: 1, Threads: 1}

func newService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(st, nil)
	s.Params = testParams
	return s
}

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery", testParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Fatalf("encoding %q", h)
	}
	if err := VerifyPassword(h, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassword(h, "wrong"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("want mismatch, got %v", err)
	}
	if err := VerifyPassword("$bcrypt$x", "a"); err == nil || errors.Is(err, ErrMismatch) {
		t.Fatalf("want format error, got %v", err)
	}
	h2, _ := HashPassword("correct horse battery", testParams)
	if h == h2 {
		t.Fatal("salt not random")
	}
}

func TestGeneratePassword(t *testing.T) {
	re := regexp.MustCompile(`^[a-zA-Z2-9]{5}(-[a-zA-Z2-9]{5}){3}$`)
	seen := map[string]bool{}
	for range 50 {
		p := GeneratePassword()
		if !re.MatchString(p) || strings.ContainsAny(p, "0O1lI") {
			t.Fatalf("bad password %q", p)
		}
		if seen[p] {
			t.Fatal("duplicate password")
		}
		seen[p] = true
	}
}

func TestBootstrapPrintsOnce(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := s.Bootstrap(ctx, &out); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`password: (\S+)`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no password block:\n%s", out.String())
	}
	if must, _ := s.MustChangePassword(ctx); !must {
		t.Fatal("first password must be changed")
	}
	out.Reset()
	if err := s.Bootstrap(ctx, &out); err != nil || out.Len() != 0 {
		t.Fatalf("second bootstrap printed %q (err %v)", out.String(), err)
	}
	if _, _, err := s.Login(ctx, m[1]); err != nil {
		t.Fatalf("login with the printed password: %v", err)
	}
}

func TestLoginSessionChangePasswordLogout(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	pw, err := s.ResetPassword(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login(ctx, "nope"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("want invalid, got %v", err)
	}
	tokA, sessA, err := s.Login(ctx, pw)
	if err != nil {
		t.Fatal(err)
	}
	tokB, _, _ := s.Login(ctx, pw)
	if sessA.CSRFToken == "" || tokA == tokB {
		t.Fatal("tokens must be random and carry a CSRF token")
	}
	got, err := s.Authenticate(ctx, tokA)
	if err != nil || got.IDHash != HashToken(tokA) || got.CSRFToken != sessA.CSRFToken {
		t.Fatalf("authenticate: %+v %v", got, err)
	}

	var pe *PolicyError
	if err := s.ChangePassword(ctx, sessA.IDHash, pw, "short"); !errors.As(err, &pe) {
		t.Fatalf("want policy error, got %v", err)
	}
	if err := s.ChangePassword(ctx, sessA.IDHash, "bad", "a long new password"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("want invalid current, got %v", err)
	}
	if err := s.ChangePassword(ctx, sessA.IDHash, pw, "a long new password"); err != nil {
		t.Fatal(err)
	}
	if must, _ := s.MustChangePassword(ctx); must {
		t.Fatal("flag not cleared")
	}
	if _, err := s.Authenticate(ctx, tokA); err != nil {
		t.Fatal("the changing session must stay signed in")
	}
	if _, err := s.Authenticate(ctx, tokB); !errors.Is(err, ErrNoSession) {
		t.Fatal("other sessions must end on password change")
	}
	if err := s.Logout(ctx, tokA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, tokA); !errors.Is(err, ErrNoSession) {
		t.Fatal("session survived logout")
	}

	// A reset ends every session and sets the flag again.
	tokC, _, _ := s.Login(ctx, "a long new password")
	if _, err := s.ResetPassword(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, tokC); !errors.Is(err, ErrNoSession) {
		t.Fatal("session survived reset")
	}
	if must, _ := s.MustChangePassword(ctx); !must {
		t.Fatal("reset must force a change")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return clock }
	pw, _ := s.ResetPassword(ctx)
	tok, _, err := s.Login(ctx, pw)
	if err != nil {
		t.Fatal(err)
	}
	// Activity keeps it alive past the idle timeout...
	for range 4 { // 4 × 167h stays under MaxLifetime
		clock = clock.Add(IdleTimeout - time.Hour)
		if _, err := s.Authenticate(ctx, tok); err != nil {
			t.Fatalf("at %s: %v", clock, err)
		}
	}
	// ...but never past the maximum lifetime.
	clock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(MaxLifetime + time.Second)
	if _, err := s.Authenticate(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("want expired, got %v", err)
	}

	tok2, _, _ := s.Login(ctx, pw)
	clock = clock.Add(IdleTimeout + time.Second)
	if _, err := s.Authenticate(ctx, tok2); !errors.Is(err, ErrNoSession) {
		t.Fatalf("want idle expiry, got %v", err)
	}
}

func TestLimiter(t *testing.T) {
	clock := time.Unix(0, 0)
	l := NewLimiter(3, time.Minute)
	l.Now = func() time.Time { return clock }
	for range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatal("blocked too early")
		}
		l.Fail("a")
	}
	ok, wait := l.Allow("a")
	if ok || wait != time.Minute {
		t.Fatalf("ok=%v wait=%s", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("other clients must not be affected")
	}
	clock = clock.Add(time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("window did not reset")
	}
	l.Fail("a")
	l.Reset("a")
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("reset did not clear")
	}
}

func TestClientIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8, 192.168.1.5")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseTrustedProxies("nonsense"); err == nil {
		t.Fatal("want parse error")
	}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},             // untrusted peer: header ignored
		{"10.1.2.3:1234", "1.2.3.4", "1.2.3.4"},                    // trusted proxy
		{"10.1.2.3:1234", "6.6.6.6, 1.2.3.4, 10.9.9.9", "1.2.3.4"}, // forged left-most entry ignored
		{"192.168.1.5:80", "", "192.168.1.5"},                      // no header
		{"[::ffff:10.0.0.1]:80", "2001:db8::1", "2001:db8::1"},     // mapped IPv4 peer
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r, trusted); got != c.want {
			t.Errorf("%s / %q: got %s want %s", c.remote, c.xff, got, c.want)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1"
	r.Header.Set("X-Forwarded-Proto", "https")
	if !ForwardedHTTPS(r, trusted) {
		t.Error("trusted proxy https not detected")
	}
	r.RemoteAddr = "8.8.8.8:1"
	if ForwardedHTTPS(r, trusted) {
		t.Error("untrusted X-Forwarded-Proto must be ignored")
	}
}

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

var testUI = fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}}

func do(h http.Handler, method, target string, mut func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if mut != nil {
		mut(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHealthz(t *testing.T) {
	h := NewHandler(Config{Mode: ModeWeb, DB: fakeDB{}, UI: testUI})
	rr := do(h, http.MethodGet, "/healthz", nil)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body)
	}

	h = NewHandler(Config{Mode: ModeWeb, DB: fakeDB{err: errors.New("down")}, UI: testUI})
	rr = do(h, http.MethodGet, "/healthz", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestWebModeServesUIAndAPI404(t *testing.T) {
	h := NewHandler(Config{Mode: ModeWeb, DB: fakeDB{}, UI: testUI})
	if rr := do(h, http.MethodGet, "/flows", nil); rr.Code != 200 || !strings.Contains(rr.Body.String(), "ui") {
		t.Fatalf("ui: code=%d", rr.Code)
	}
	rr := do(h, http.MethodGet, "/api/v1/nope", nil)
	if rr.Code != 404 || !strings.Contains(rr.Body.String(), `"code":"not_found"`) {
		t.Fatalf("api 404: code=%d body=%s", rr.Code, rr.Body)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := rr.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"Content-Security-Policy", "Permissions-Policy"} {
		if rr.Header().Get(name) == "" {
			t.Errorf("%s missing", name)
		}
	}
}

func TestDesktopToken(t *testing.T) {
	const tok = "launch-token-123"
	h := NewHandler(Config{Mode: ModeDesktop, DesktopToken: tok, DB: fakeDB{}, UI: testUI})

	if rr := do(h, http.MethodGet, "/healthz", nil); rr.Code != 200 {
		t.Errorf("healthz must stay open: %d", rr.Code)
	}
	if rr := do(h, http.MethodGet, "/", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rr.Code)
	}
	if rr := do(h, http.MethodGet, "/?token=wrong", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", rr.Code)
	}

	// Bootstrap: token in the query becomes a cookie and is stripped by redirect.
	rr := do(h, http.MethodGet, "/flows?token="+tok+"&x=1", nil)
	if rr.Code != http.StatusFound {
		t.Fatalf("bootstrap: %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); strings.Contains(loc, tok) || !strings.HasPrefix(loc, "/flows") {
		t.Errorf("redirect location %q", loc)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Value != tok {
		t.Fatalf("cookie: %+v", cookies)
	}

	if rr := do(h, http.MethodGet, "/", func(r *http.Request) { r.AddCookie(cookies[0]) }); rr.Code != 200 {
		t.Errorf("cookie auth: %d", rr.Code)
	}
	if rr := do(h, http.MethodGet, "/api/v1/x", func(r *http.Request) { r.Header.Set(desktopTokenHeader, tok) }); rr.Code != 404 {
		t.Errorf("header auth should reach the API (404 expected): %d", rr.Code)
	}
}

func TestDesktopModeWithoutTokenRejectsEverything(t *testing.T) {
	h := NewHandler(Config{Mode: ModeDesktop, DB: fakeDB{}, UI: testUI})
	if rr := do(h, http.MethodGet, "/", func(r *http.Request) { r.Header.Set(desktopTokenHeader, "") }); rr.Code != http.StatusUnauthorized {
		t.Fatalf("empty configured token must never match: %d", rr.Code)
	}
}

func TestDesktopBootstrapRedirectStaysLocal(t *testing.T) {
	const tok = "launch-token-123"
	h := NewHandler(Config{Mode: ModeDesktop, DesktopToken: tok, DB: fakeDB{}, UI: testUI})
	for target, want := range map[string]string{
		"/?token=" + tok:                "/",
		"//evil.example/?token=" + tok:  "/evil.example/",
		"/%5Cevil.example?token=" + tok: "/%5Cevil.example", // stays an encoded path segment
		"/flows/1?a=b&token=" + tok:     "/flows/1?a=b",
	} {
		rr := do(h, http.MethodGet, target, nil)
		if rr.Code != http.StatusFound {
			t.Errorf("%s: code %d", target, rr.Code)
			continue
		}
		if got := rr.Header().Get("Location"); got != want {
			t.Errorf("%s: Location %q want %q", target, got, want)
		}
	}
}

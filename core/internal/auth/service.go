package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/store"
)

// Session lifetimes.
const (
	// IdleTimeout ends a session not used for this long.
	IdleTimeout = 7 * 24 * time.Hour
	// MaxLifetime ends a session this long after login, whatever the activity.
	MaxLifetime = 30 * 24 * time.Hour
	// touchEvery limits how often activity is written to the database.
	touchEvery = time.Minute
)

// Errors returned by Service.
var (
	ErrNoSession       = errors.New("not signed in")
	ErrInvalidPassword = errors.New("the password is not correct")
)

// Service is the admin account and its sessions.
type Service struct {
	Store  *store.Store
	Log    *slog.Logger
	Params Params
	Now    func() time.Time
}

// New returns a Service with default parameters.
func New(st *store.Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Store: st, Log: log, Params: DefaultParams, Now: time.Now}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) params() Params {
	if s.Params == (Params{}) {
		return DefaultParams
	}
	return s.Params
}

// Bootstrap creates the admin account on first start and writes the
// one-time password to out in a clearly visible block. It does nothing when
// the account exists. Only the process that created the account prints it.
func (s *Service) Bootstrap(ctx context.Context, out io.Writer) error {
	if _, err := s.Store.GetAdmin(ctx); err == nil {
		return nil
	} else if !store.IsNotFound(err) {
		return err
	}
	pw := GeneratePassword()
	hash, err := HashPassword(pw, s.params())
	if err != nil {
		return err
	}
	created, err := s.Store.CreateAdminIfMissing(ctx, hash)
	if err != nil || !created {
		return err
	}
	s.Log.Warn("created the admin account with a one-time password; it is printed once below and must be changed at first sign-in")
	PrintPasswordBlock(out, "First start: sign in to Tolarix as admin with this one-time password.", pw)
	return nil
}

// PrintPasswordBlock writes a boxed, easy-to-spot password notice.
func PrintPasswordBlock(out io.Writer, title, pw string) {
	lines := []string{
		title,
		"",
		"    username: admin",
		"    password: " + pw,
		"",
		"You will be asked to choose a new password after signing in.",
		"This password is shown only once. Lost it? Run: tolarix admin reset-password",
	}
	width := 0
	for _, l := range lines {
		width = max(width, len(l))
	}
	bar := "+" + strings.Repeat("-", width+2) + "+"
	var b strings.Builder
	b.WriteString("\n" + bar + "\n")
	for _, l := range lines {
		b.WriteString("| " + l + strings.Repeat(" ", width-len(l)) + " |\n")
	}
	b.WriteString(bar + "\n\n")
	_, _ = io.WriteString(out, b.String())
}

// ResetPassword sets a new one-time password, ends every session and returns
// the password so the caller can show it.
func (s *Service) ResetPassword(ctx context.Context) (string, error) {
	pw := GeneratePassword()
	hash, err := HashPassword(pw, s.params())
	if err != nil {
		return "", err
	}
	if err := s.Store.SetAdminPassword(ctx, hash, true, ""); err != nil {
		return "", err
	}
	return pw, nil
}

// Login checks the password and opens a session. It returns the cookie value
// (only its hash is stored).
func (s *Service) Login(ctx context.Context, password string) (string, store.Session, error) {
	admin, err := s.Store.GetAdmin(ctx)
	if store.IsNotFound(err) {
		return "", store.Session{}, ErrInvalidPassword
	}
	if err != nil {
		return "", store.Session{}, err
	}
	if err := VerifyPassword(admin.PasswordHash, password); err != nil {
		if errors.Is(err, ErrMismatch) {
			return "", store.Session{}, ErrInvalidPassword
		}
		return "", store.Session{}, fmt.Errorf("check the password: %w", err)
	}
	token := RandomToken()
	t := s.now().UTC()
	sess := store.Session{
		IDHash: HashToken(token), CSRFToken: RandomToken(),
		CreatedAt: t, LastSeenAt: t, ExpiresAt: t.Add(IdleTimeout),
	}
	if err := s.Store.CreateSession(ctx, sess); err != nil {
		return "", store.Session{}, err
	}
	if n, err := s.Store.DeleteExpiredSessions(ctx, t); err == nil && n > 0 {
		s.Log.Debug("removed expired sessions", "count", n)
	}
	return token, sess, nil
}

// Authenticate resolves a cookie value to a live session and records the
// activity (sliding idle timeout, capped by MaxLifetime).
func (s *Service) Authenticate(ctx context.Context, token string) (store.Session, error) {
	if token == "" {
		return store.Session{}, ErrNoSession
	}
	t := s.now().UTC()
	sess, err := s.Store.GetSession(ctx, HashToken(token), t)
	if store.IsNotFound(err) {
		return sess, ErrNoSession
	}
	if err != nil {
		return sess, err
	}
	if t.Sub(sess.LastSeenAt) >= touchEvery {
		exp := t.Add(IdleTimeout)
		if limit := sess.CreatedAt.Add(MaxLifetime); exp.After(limit) {
			exp = limit
		}
		if err := s.Store.TouchSession(ctx, sess.IDHash, t, exp); err != nil {
			return sess, err
		}
		sess.LastSeenAt, sess.ExpiresAt = t, exp
	}
	return sess, nil
}

// Logout ends the session of a cookie value.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.Store.DeleteSession(ctx, HashToken(token))
}

// MustChangePassword reports whether the admin still has a one-time password.
func (s *Service) MustChangePassword(ctx context.Context) (bool, error) {
	a, err := s.Store.GetAdmin(ctx)
	if err != nil {
		return false, err
	}
	return a.MustChangePassword, nil
}

// PolicyError is a new password that does not meet the rules.
type PolicyError struct{ Msg string }

func (e *PolicyError) Error() string { return e.Msg }

// ChangePassword replaces the password after checking the current one. Every
// other session is ended; the caller's session (by hash) stays signed in.
func (s *Service) ChangePassword(ctx context.Context, sessionHash, current, next string) error {
	admin, err := s.Store.GetAdmin(ctx)
	if err != nil {
		return err
	}
	if err := VerifyPassword(admin.PasswordHash, current); err != nil {
		if errors.Is(err, ErrMismatch) {
			return ErrInvalidPassword
		}
		return err
	}
	if err := CheckNewPassword(next); err != nil {
		return &PolicyError{Msg: err.Error()}
	}
	if current == next {
		return &PolicyError{Msg: "choose a password different from the current one"}
	}
	hash, err := HashPassword(next, s.params())
	if err != nil {
		return err
	}
	return s.Store.SetAdminPassword(ctx, hash, false, sessionHash)
}

// HashToken is how session cookie values are stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

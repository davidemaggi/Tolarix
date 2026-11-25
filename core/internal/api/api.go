package api

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/davidemaggi/tolarix/core/internal/api/oapi"
	"github.com/davidemaggi/tolarix/core/internal/auth"
	"github.com/davidemaggi/tolarix/core/internal/runner"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

//go:embed openapi.yaml
var openAPISpec []byte

// OpenAPISpec returns the API contract (served at /api/v1/openapi.yaml).
func OpenAPISpec() []byte { return openAPISpec }

// CookieSecure selects the Secure attribute of the session cookie.
type CookieSecure string

const (
	// CookieSecureAuto sets Secure when the request came over HTTPS, directly
	// or through a trusted proxy (X-Forwarded-Proto).
	CookieSecureAuto   CookieSecure = "auto"
	CookieSecureAlways CookieSecure = "true"
	CookieSecureNever  CookieSecure = "false"
)

// ParseCookieSecure validates TOLARIX_COOKIE_SECURE ("" = auto).
func ParseCookieSecure(s string) (CookieSecure, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return CookieSecureAuto, true
	case "true", "1", "yes":
		return CookieSecureAlways, true
	case "false", "0", "no":
		return CookieSecureNever, true
	}
	return "", false
}

// Backend is what the JSON API needs. When Config.Backend is nil the API
// answers 404 (used by tests of the bare HTTP shell).
type Backend struct {
	Store      *store.Store
	Cipher     *secrets.Cipher
	Auth       *auth.Service
	Runner     *runner.Runner
	Background *runner.Background
	Hub        *runner.Hub
	// TrustedProxies may set X-Forwarded-For / X-Forwarded-Proto.
	TrustedProxies []netip.Prefix
	CookieSecure   CookieSecure
	// Timezone is the server's default timezone shown in the UI.
	Timezone string
	// Limiter throttles failed sign-ins (nil = 10 per 15 minutes per client).
	Limiter *auth.Limiter
	// PollInterval is how often live logs re-read the database (default 1s).
	PollInterval time.Duration
}

const (
	sessionCookie = "tolarix_session"
	csrfHeader    = "X-CSRF-Token" //nolint:gosec // a header name, not a credential
)

type server struct {
	mode        Mode
	log         *slog.Logger
	b           Backend
	desktopCSRF string
	limiter     *auth.Limiter
}

var _ oapi.ServerInterface = (*server)(nil)

type ctxKey int

const sessionKey ctxKey = 1

// mountAPI registers the JSON API on r (already scoped to /api/v1).
func mountAPI(r chi.Router, mode Mode, log *slog.Logger, b Backend) {
	s := &server{mode: mode, log: log, b: b, limiter: b.Limiter}
	if s.limiter == nil {
		s.limiter = auth.NewLimiter(10, 15*time.Minute)
	}
	if s.b.PollInterval <= 0 {
		s.b.PollInterval = time.Second
	}
	if s.b.Hub == nil {
		s.b.Hub = runner.NewHub()
	}
	if mode == ModeDesktop {
		// The desktop app has no sign-in, but mutating requests still carry a
		// CSRF token so both modes follow the same rules.
		s.desktopCSRF = auth.RandomToken()
	}
	r.Use(s.authenticate)
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(openAPISpec)
	})
	oapi.HandlerWithOptions(s, oapi.ChiServerOptions{
		BaseRouter: r,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			s.writeErr(w, r, errBadRequest("invalid_parameter", "%s", err.Error()))
		},
	})
}

// authenticate enforces sign-in, CSRF and the forced password change.
func (s *server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		public := path == "/openapi.yaml" ||
			(r.Method == http.MethodGet && path == "/auth/session") ||
			(r.Method == http.MethodPost && path == "/auth/login")
		mutating := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions

		if s.mode == ModeDesktop {
			if mutating && !public && !tokenEqual(r.Header.Get(csrfHeader), s.desktopCSRF) {
				s.writeErr(w, r, errCSRF)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		var sess *store.Session
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			got, err := s.b.Auth.Authenticate(r.Context(), c.Value)
			switch {
			case err == nil:
				sess = &got
			case !errors.Is(err, auth.ErrNoSession):
				s.writeErr(w, r, err)
				return
			}
		}
		if sess != nil {
			r = r.WithContext(context.WithValue(r.Context(), sessionKey, sess))
		}
		if public {
			next.ServeHTTP(w, r)
			return
		}
		if sess == nil {
			s.writeErr(w, r, &apiError{Status: http.StatusUnauthorized, Code: "unauthenticated", Message: "Sign in to continue."})
			return
		}
		if mutating && !tokenEqual(r.Header.Get(csrfHeader), sess.CSRFToken) {
			s.writeErr(w, r, errCSRF)
			return
		}
		if !strings.HasPrefix(path, "/auth/") {
			must, err := s.b.Auth.MustChangePassword(r.Context())
			if err != nil {
				s.writeErr(w, r, err)
				return
			}
			if must {
				s.writeErr(w, r, &apiError{Status: http.StatusForbidden, Code: "password_change_required",
					Message: "Choose a new password before continuing."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

var errCSRF = &apiError{Status: http.StatusForbidden, Code: "csrf",
	Message: "The request is missing its security token. Reload the page and try again."}

func tokenEqual(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func sessionFrom(ctx context.Context) *store.Session {
	s, _ := ctx.Value(sessionKey).(*store.Session)
	return s
}

func (s *server) cookieSecure(r *http.Request) bool {
	switch s.b.CookieSecure {
	case CookieSecureAlways:
		return true
	case CookieSecureNever:
		return false
	}
	return auth.ForwardedHTTPS(r, s.b.TrustedProxies)
}

func (s *server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is configurable (TOLARIX_COOKIE_SECURE); HttpOnly and SameSite are set
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.cookieSecure(r), SameSite: http.SameSiteStrictMode,
	})
}

func (s *server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is configurable (TOLARIX_COOKIE_SECURE); HttpOnly and SameSite are set
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cookieSecure(r), SameSite: http.SameSiteStrictMode,
	})
}

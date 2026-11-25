// Package api is the HTTP layer: health check, JSON API and the embedded web UI.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/davidemaggi/tolarix/core/internal/buildinfo"
	"github.com/davidemaggi/tolarix/core/internal/webui"
)

// Mode selects how the server is exposed.
type Mode string

const (
	// ModeWeb is the Docker/server mode: binds all interfaces, admin login required.
	ModeWeb Mode = "web"
	// ModeDesktop is started by the Tauri shell: loopback only, every request carries the launch token.
	ModeDesktop Mode = "desktop"
)

// ParseMode validates a --mode value.
func ParseMode(s string) (Mode, bool) {
	switch Mode(s) {
	case ModeWeb, ModeDesktop:
		return Mode(s), true
	}
	return "", false
}

// Pinger reports database health.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Config holds server dependencies.
type Config struct {
	Mode         Mode
	DesktopToken string // required in desktop mode
	DB           Pinger
	Logger       *slog.Logger
	UI           fs.FS // nil uses the embedded build
	// API serves the JSON API; nil answers 404 on /api/v1 (bare shell tests).
	API *Backend
}

const (
	desktopTokenHeader = "X-Tolarix-Token" //nolint:gosec // a header name, not a credential
	desktopTokenCookie = "tolarix_desktop"
	desktopTokenQuery  = "token"
)

// NewHandler builds the root HTTP handler.
func NewHandler(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.UI == nil {
		cfg.UI = webui.FS()
	}
	r := chi.NewRouter()
	// No middleware.RealIP: it trusts X-Forwarded-For blindly (IP spoofing).
	// Client IPs come from auth.ClientIP with the trusted-proxy setting.
	r.Use(middleware.RequestID)
	r.Use(recoverer(cfg.Logger))
	r.Use(requestLogger(cfg.Logger))
	r.Use(securityHeaders)

	r.Get("/healthz", healthz(cfg.DB))

	r.Group(func(r chi.Router) {
		if cfg.Mode == ModeDesktop {
			r.Use(desktopToken(cfg.DesktopToken))
		}
		r.Route("/api/v1", func(r chi.Router) {
			if cfg.API != nil {
				mountAPI(r, cfg.Mode, cfg.Logger, *cfg.API)
			}
			r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
				writeError(w, http.StatusNotFound, "not_found", "No such API endpoint.")
			})
		})
		r.Handle("/*", webui.Handler(cfg.UI))
	})
	return r
}

func healthz(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		status, code, dbStatus := "ok", http.StatusOK, "ok"
		if db != nil {
			if err := db.Ping(ctx); err != nil {
				status, code, dbStatus = "error", http.StatusServiceUnavailable, "unreachable"
			}
		}
		writeJSON(w, code, map[string]any{
			"status":   status,
			"database": dbStatus,
			"version":  buildinfo.Get().Version,
		})
	}
}

// desktopToken rejects requests without the per-launch token. The Tauri shell
// opens "/?token=<t>" once; the server turns that into an HttpOnly cookie and
// redirects to the same URL without the token so it does not linger in history.
func desktopToken(token string) func(http.Handler) http.Handler {
	want := []byte(token)
	ok := func(got string) bool {
		return len(want) > 0 && subtle.ConstantTimeCompare([]byte(got), want) == 1
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if q := r.URL.Query().Get(desktopTokenQuery); q != "" && r.Method == http.MethodGet {
				if !ok(q) {
					writeError(w, http.StatusUnauthorized, "invalid_token", "Invalid launch token.")
					return
				}
				// Not Secure: the desktop server is plain HTTP on 127.0.0.1 only.
				http.SetCookie(w, &http.Cookie{ //nolint:gosec // loopback-only plain HTTP, see above
					Name: desktopTokenCookie, Value: token, Path: "/",
					HttpOnly: true, SameSite: http.SameSiteStrictMode,
				})
				http.Redirect(w, r, sameOriginTarget(r.URL), http.StatusFound) //nolint:gosec // sameOriginTarget only returns local paths
				return
			}
			if ok(r.Header.Get(desktopTokenHeader)) {
				next.ServeHTTP(w, r)
				return
			}
			if c, err := r.Cookie(desktopTokenCookie); err == nil && ok(c.Value) {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusUnauthorized, "missing_token", "This Tolarix instance only accepts requests from the desktop app.")
		})
	}
}

// sameOriginTarget returns the request path and query without the token
// parameter, always as a local absolute path. Leading slashes and backslashes
// are collapsed so "//evil.example" or "/\evil.example" cannot become a
// protocol-relative redirect to another host.
func sameOriginTarget(u *url.URL) string {
	p := "/" + strings.TrimLeft(u.EscapedPath(), `/\`)
	qs := u.Query()
	qs.Del(desktopTokenQuery)
	if enc := qs.Encode(); enc != "" {
		p += "?" + enc
	}
	return p
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// requestLogger logs one line per request. It logs the path only, never the
// query string (the desktop token travels there once).
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			lvl := slog.LevelDebug
			if ww.Status() >= 500 {
				lvl = slog.LevelError
			}
			log.Log(r.Context(), lvl, "http request",
				"method", r.Method,
				"path", (&url.URL{Path: r.URL.Path}).EscapedPath(),
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()))
		})
	}
}

func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if err, isErr := rec.(error); isErr && errors.Is(err, http.ErrAbortHandler) {
						panic(rec)
					}
					log.Error("panic in HTTP handler", "panic", rec, "path", r.URL.Path,
						"request_id", middleware.GetReqID(r.Context()))
					writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. Check the server logs.")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ErrorBody is the JSON shape of every API error.
type ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var b ErrorBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/davidemaggi/tolarix/core/internal/api/oapi"
	"github.com/davidemaggi/tolarix/core/internal/auth"
	"github.com/davidemaggi/tolarix/core/internal/buildinfo"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

func (s *server) sessionBody(r *http.Request, sess *store.Session) (oapi.Session, error) {
	out := oapi.Session{Version: buildinfo.Get().Version, Mode: oapi.SessionModeWeb}
	if s.b.Timezone != "" {
		out.Timezone = ptr(s.b.Timezone)
	}
	if s.mode == ModeDesktop {
		out.Mode, out.Authenticated, out.CsrfToken = oapi.SessionModeDesktop, true, ptr(s.desktopCSRF)
		return out, nil
	}
	if sess == nil {
		return out, nil
	}
	must, err := s.b.Auth.MustChangePassword(r.Context())
	if err != nil {
		return out, err
	}
	out.Authenticated, out.MustChangePassword, out.CsrfToken = true, must, ptr(sess.CSRFToken)
	return out, nil
}

func (s *server) GetSession(w http.ResponseWriter, r *http.Request) {
	body, err := s.sessionBody(r, sessionFrom(r.Context()))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) Login(w http.ResponseWriter, r *http.Request) {
	var in oapi.LoginRequest
	if err := decodeJSON(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if s.mode == ModeDesktop {
		s.GetSession(w, r)
		return
	}
	client := auth.ClientIP(r, s.b.TrustedProxies)
	if !s.allow(w, r, client) {
		return
	}
	token, sess, err := s.b.Auth.Login(r.Context(), in.Password)
	if errors.Is(err, auth.ErrInvalidPassword) {
		s.limiter.Fail(client)
		s.log.Warn("failed sign-in", "client", client)
		s.writeErr(w, r, &apiError{Status: http.StatusUnauthorized, Code: "invalid_password", Message: "Wrong password."})
		return
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.limiter.Reset(client)
	s.log.Info("admin signed in", "client", client)
	s.setSessionCookie(w, r, token, sess.CreatedAt.Add(auth.MaxLifetime))
	body, err := s.sessionBody(r, &sess)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// allow applies the sign-in rate limit, answering 429 when exceeded.
func (s *server) allow(w http.ResponseWriter, r *http.Request, client string) bool {
	ok, wait := s.limiter.Allow(client)
	if ok {
		return true
	}
	mins := int(math.Ceil(wait.Minutes()))
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	s.writeErr(w, r, &apiError{Status: http.StatusTooManyRequests, Code: "too_many_attempts",
		Message: fmt.Sprintf("Too many wrong passwords. Try again in %d minute(s).", max(mins, 1))})
	return false
}

func (s *server) Logout(w http.ResponseWriter, r *http.Request) {
	if s.mode == ModeWeb {
		if c, err := r.Cookie(sessionCookie); err == nil {
			if err := s.b.Auth.Logout(r.Context(), c.Value); err != nil {
				s.writeErr(w, r, err)
				return
			}
		}
		s.clearSessionCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var in oapi.ChangePasswordRequest
	if err := decodeJSON(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if s.mode == ModeDesktop {
		s.writeErr(w, r, errBadRequest("not_available", "The desktop app has no password."))
		return
	}
	sess := sessionFrom(r.Context())
	client := auth.ClientIP(r, s.b.TrustedProxies)
	if !s.allow(w, r, client) {
		return
	}
	err := s.b.Auth.ChangePassword(r.Context(), sess.IDHash, in.CurrentPassword, in.NewPassword)
	var pe *auth.PolicyError
	switch {
	case errors.Is(err, auth.ErrInvalidPassword):
		s.limiter.Fail(client)
		s.writeErr(w, r, &apiError{Status: http.StatusBadRequest, Code: "validation", Message: "Some fields need attention.",
			Fields: map[string]string{"currentPassword": "The current password is wrong."}})
		return
	case errors.As(err, &pe):
		s.writeErr(w, r, errValidation(map[string]string{"newPassword": capitalize(pe.Msg) + "."}))
		return
	case err != nil:
		s.writeErr(w, r, err)
		return
	}
	s.limiter.Reset(client)
	s.log.Info("admin password changed", "client", client)
	body, err := s.sessionBody(r, sess)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

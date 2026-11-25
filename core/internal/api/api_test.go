package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/davidemaggi/tolarix/core/internal/auth"
	_ "github.com/davidemaggi/tolarix/core/internal/connectors/all"
	"github.com/davidemaggi/tolarix/core/internal/runner"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
	"github.com/davidemaggi/tolarix/core/internal/store"
	"github.com/davidemaggi/tolarix/core/internal/testkit"
)

var (
	specOnce   sync.Once
	specRouter routers.Router
	specErr    error
)

// contract returns a router over openapi.yaml, used to check that every
// response matches the documented schema.
func contract(t *testing.T) routers.Router {
	t.Helper()
	specOnce.Do(func() {
		doc, err := openapi3.NewLoader().LoadFromData(OpenAPISpec())
		if err != nil {
			specErr = err
			return
		}
		if err := doc.Validate(context.Background()); err != nil {
			specErr = fmt.Errorf("openapi.yaml is invalid: %w", err)
			return
		}
		doc.Servers = openapi3.Servers{{URL: "http://example.com/api/v1"}}
		specRouter, specErr = legacy.NewRouter(doc)
	})
	if specErr != nil {
		t.Fatal(specErr)
	}
	return specRouter
}

type env struct {
	t       *testing.T
	h       http.Handler
	st      *store.Store
	auth    *auth.Service
	bg      *runner.Background
	dir     string
	limiter *auth.Limiter
}

func newEnv(t *testing.T, mode Mode) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "tolarix.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	ci, _ := secrets.NewCipher(key)
	as := auth.New(st, nil)
	as.Params = auth.Params{Memory: 1024, Time: 1, Threads: 1}
	hub := runner.NewHub()
	quiet := slog.New(slog.DiscardHandler)
	as.Log = quiet
	rn := &runner.Runner{Store: st, Cipher: ci, Log: quiet, Notify: hub.Notify, HeartbeatEvery: 10 * time.Millisecond}
	bg := runner.NewBackground(rn)
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = bg.Shutdown(sctx)
		_ = st.Close()
	})
	lim := auth.NewLimiter(3, time.Minute)
	h := NewHandler(Config{
		Mode: mode, DesktopToken: "desk", DB: st, UI: testUI, Logger: quiet,
		API: &Backend{
			Store: st, Cipher: ci, Auth: as, Runner: rn, Background: bg, Hub: hub,
			Timezone: "Europe/Rome", Limiter: lim, PollInterval: 20 * time.Millisecond,
		},
	})
	return &env{t: t, h: h, st: st, auth: as, bg: bg, dir: dir, limiter: lim}
}

// client keeps the session cookie and CSRF token like the web UI does.
type client struct {
	e       *env
	cookies map[string]*http.Cookie
	csrf    string
	header  http.Header
}

func (e *env) client() *client {
	return &client{e: e, cookies: map[string]*http.Cookie{}, header: http.Header{}}
}

type resp struct {
	code int
	hdr  http.Header
	body []byte
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r resp) errCode(t *testing.T) string {
	t.Helper()
	var b struct {
		Error struct {
			Code    string            `json:"code"`
			Fields  map[string]string `json:"fields"`
			Message string            `json:"message"`
		} `json:"error"`
	}
	r.json(t, &b)
	if b.Error.Message == "" {
		t.Fatalf("error without message: %s", r.body)
	}
	return b.Error.Code
}

func (r resp) fields(t *testing.T) map[string]string {
	t.Helper()
	var b struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	r.json(t, &b)
	return b.Error.Fields
}

// do sends a request and validates the response against the contract.
func (c *client) do(method, path string, body any) resp {
	t := c.e.t
	t.Helper()
	var raw []byte
	if body != nil {
		if s, ok := body.(string); ok {
			raw = []byte(s)
		} else {
			raw, _ = json.Marshal(body)
		}
	}
	req := httptest.NewRequest(method, "http://example.com/api/v1"+path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		req.Header.Set(csrfHeader, c.csrf)
	}
	for k, v := range c.header {
		req.Header[k] = v
	}
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	rr := httptest.NewRecorder()
	c.e.h.ServeHTTP(rr, req)
	for _, ck := range rr.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	out := resp{code: rr.Code, hdr: rr.Header(), body: rr.Body.Bytes()}
	validate(t, req, raw, out)
	return out
}

// validate checks a response against openapi.yaml. Errors produced by the
// middleware (401, 403, 415, 500) are not listed per operation; they must
// still have the Error shape.
func validate(t *testing.T, req *http.Request, reqBody []byte, r resp) {
	t.Helper()
	if req.URL.Path == "/api/v1/openapi.yaml" { // the contract itself
		return
	}
	route, params, err := contract(t).FindRoute(req)
	if err != nil {
		if r.code != http.StatusNotFound {
			t.Fatalf("%s %s is not in openapi.yaml (status %d)", req.Method, req.URL.Path, r.code)
		}
		return
	}
	switch r.code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusUnsupportedMediaType, http.StatusInternalServerError:
		if route.Operation.Responses.Status(r.code) == nil {
			var b ErrorBody
			if json.Unmarshal(r.body, &b) != nil || b.Error.Code == "" || b.Error.Message == "" {
				t.Fatalf("%s %s: %d without an error body: %s", req.Method, req.URL.Path, r.code, r.body)
			}
			return
		}
	}
	req.Body = io.NopCloser(bytes.NewReader(reqBody))
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}
	err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in, Status: r.code, Header: r.hdr,
		Body:    io.NopCloser(bytes.NewReader(r.body)),
		Options: &openapi3filter.Options{IncludeResponseStatus: true},
	})
	if err != nil {
		t.Fatalf("%s %s: response does not match openapi.yaml: %v\nbody: %s", req.Method, req.URL.Path, err, r.body)
	}
}

// signIn creates the admin, signs in and completes the forced change.
func (e *env) signIn() *client {
	e.t.Helper()
	pw, err := e.auth.ResetPassword(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	c := e.client()
	r := c.do("POST", "/auth/login", map[string]string{"password": pw})
	if r.code != 200 {
		e.t.Fatalf("login: %d %s", r.code, r.body)
	}
	var s struct{ CsrfToken string }
	r.json(e.t, &s)
	c.csrf = s.CsrfToken
	if r := c.do("POST", "/auth/password", map[string]string{"currentPassword": pw, "newPassword": "a long enough password"}); r.code != 200 {
		e.t.Fatalf("change password: %d %s", r.code, r.body)
	}
	return c
}

func TestAuthFlow(t *testing.T) {
	e := newEnv(t, ModeWeb)
	c := e.client()

	var s map[string]any
	c.do("GET", "/auth/session", nil).json(t, &s)
	if s["authenticated"] != false || s["mode"] != "web" || s["csrfToken"] != nil {
		t.Fatalf("anonymous session: %v", s)
	}
	if r := c.do("GET", "/flows", nil); r.code != 401 || r.errCode(t) != "unauthenticated" {
		t.Fatalf("want 401, got %d", r.code)
	}

	pw, _ := e.auth.ResetPassword(context.Background())
	if r := c.do("POST", "/auth/login", map[string]string{"password": "wrong"}); r.code != 401 || r.errCode(t) != "invalid_password" {
		t.Fatalf("wrong password: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/auth/login", `{"password": 1}`); r.code != 400 {
		t.Fatalf("bad body: %d", r.code)
	}
	r := c.do("POST", "/auth/login", map[string]string{"password": pw})
	if r.code != 200 {
		t.Fatalf("login: %d %s", r.code, r.body)
	}
	ck := c.cookies[sessionCookie]
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Secure {
		t.Fatalf("session cookie: %+v", ck)
	}
	r.json(t, &s)
	if s["authenticated"] != true || s["mustChangePassword"] != true || s["timezone"] != "Europe/Rome" {
		t.Fatalf("session after login: %v", s)
	}
	csrf, _ := s["csrfToken"].(string)

	if r := c.do("GET", "/flows", nil); r.code != 403 || r.errCode(t) != "password_change_required" {
		t.Fatalf("want forced change, got %d %s", r.code, r.body)
	}
	change := map[string]string{"currentPassword": pw, "newPassword": "a long enough password"}
	if r := c.do("POST", "/auth/password", change); r.code != 403 || r.errCode(t) != "csrf" {
		t.Fatalf("want csrf error, got %d %s", r.code, r.body)
	}
	c.csrf = "wrong"
	if r := c.do("POST", "/auth/password", change); r.code != 403 {
		t.Fatalf("wrong csrf accepted: %d", r.code)
	}
	c.csrf = csrf
	if r := c.do("POST", "/auth/password", map[string]string{"currentPassword": pw, "newPassword": "short"}); r.code != 400 || r.fields(t)["newPassword"] == "" {
		t.Fatalf("policy: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/auth/password", map[string]string{"currentPassword": "nope", "newPassword": "a long enough password"}); r.code != 400 || r.fields(t)["currentPassword"] == "" {
		t.Fatalf("wrong current: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/auth/password", change); r.code != 200 {
		t.Fatalf("change: %d %s", r.code, r.body)
	}
	if r := c.do("GET", "/flows", nil); r.code != 200 || strings.TrimSpace(string(r.body)) != "[]" {
		t.Fatalf("flows after change: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/auth/logout", nil); r.code != 204 {
		t.Fatalf("logout: %d", r.code)
	}
	if _, ok := c.cookies[sessionCookie]; ok {
		t.Fatal("logout must clear the cookie")
	}
	if r := c.do("GET", "/flows", nil); r.code != 401 {
		t.Fatalf("after logout: %d", r.code)
	}
	if r := c.do("GET", "/openapi.yaml", nil); r.code != 200 || !bytes.Contains(r.body, []byte("openapi: 3")) {
		t.Fatalf("spec: %d", r.code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t, ModeWeb)
	pw, _ := e.auth.ResetPassword(context.Background())
	c := e.client()
	for range 3 {
		if r := c.do("POST", "/auth/login", map[string]string{"password": "wrong"}); r.code != 401 {
			t.Fatalf("got %d", r.code)
		}
	}
	r := c.do("POST", "/auth/login", map[string]string{"password": pw})
	if r.code != 429 || r.errCode(t) != "too_many_attempts" || r.hdr.Get("Retry-After") == "" {
		t.Fatalf("want 429, got %d %s", r.code, r.body)
	}
	// A different client (direct peer) is not affected.
	other := e.client()
	other.header.Set("X-Forwarded-For", "9.9.9.9") // ignored: no trusted proxies
	if r := other.do("POST", "/auth/login", map[string]string{"password": pw}); r.code != 429 {
		t.Fatalf("X-Forwarded-For from an untrusted peer must be ignored, got %d", r.code)
	}
}

func TestCookieSecureBehindTrustedProxy(t *testing.T) {
	e := newEnv(t, ModeWeb)
	pw, _ := e.auth.ResetPassword(context.Background())
	trusted, _ := auth.ParseTrustedProxies("192.0.2.0/24") // httptest uses 192.0.2.1
	e.h = NewHandler(Config{Mode: ModeWeb, UI: testUI, API: &Backend{
		Store: e.st, Auth: e.auth, Runner: &runner.Runner{Store: e.st}, Background: e.bg,
		TrustedProxies: trusted,
	}})
	c := e.client()
	c.header.Set("X-Forwarded-Proto", "https")
	if r := c.do("POST", "/auth/login", map[string]string{"password": pw}); r.code != 200 || !c.cookies[sessionCookie].Secure {
		t.Fatalf("cookie must be Secure behind a trusted HTTPS proxy: %d %+v", r.code, c.cookies[sessionCookie])
	}
}

func TestDesktopModeCSRF(t *testing.T) {
	e := newEnv(t, ModeDesktop)
	c := e.client()
	c.header.Set(desktopTokenHeader, "desk")
	var s map[string]any
	c.do("GET", "/auth/session", nil).json(t, &s)
	if s["authenticated"] != true || s["mode"] != "desktop" || s["csrfToken"] == "" {
		t.Fatalf("desktop session: %v", s)
	}
	body := map[string]any{"kind": "obsidian", "config": map[string]string{"path": e.dir}}
	if r := c.do("POST", "/connections/test", body); r.code != 403 || r.errCode(t) != "csrf" {
		t.Fatalf("want csrf error, got %d", r.code)
	}
	c.csrf = s["csrfToken"].(string)
	if r := c.do("POST", "/connections/test", body); r.code != 200 {
		t.Fatalf("test: %d %s", r.code, r.body)
	}
}

// vaults creates a Master vault with content and an empty Mirror folder.
func (e *env) vaults() (master, mirror string) {
	master, mirror = filepath.Join(e.dir, "master"), filepath.Join(e.dir, "mirror")
	testkit.CopyDir(e.t, "../connectors/obsidian/testdata/decode/basic/vault", master)
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return master, mirror
}

func TestConnections(t *testing.T) {
	e := newEnv(t, ModeWeb)
	c := e.signIn()
	master, _ := e.vaults()

	var kinds []map[string]any
	c.do("GET", "/connector-kinds", nil).json(t, &kinds)
	if len(kinds) == 0 || kinds[0]["kind"] != "obsidian" {
		t.Fatalf("kinds: %v", kinds)
	}

	r := c.do("POST", "/connections", map[string]any{"name": " ", "kind": "obsidian", "config": map[string]any{}})
	if f := r.fields(t); r.code != 400 || f["name"] == "" || f["config.path"] == "" {
		t.Fatalf("validation: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/connections", map[string]any{"name": "X", "kind": "nope", "config": map[string]any{}}); r.code != 400 || r.fields(t)["kind"] == "" {
		t.Fatalf("unknown kind: %d %s", r.code, r.body)
	}
	if r := c.do("POST", "/connections", map[string]any{"name": "X", "kind": "obsidian", "config": map[string]any{}, "extra": 1}); r.code != 400 || r.errCode(t) != "invalid_json" {
		t.Fatalf("unknown field: %d %s", r.code, r.body)
	}

	const secret = "s3cr3t-value"
	r = c.do("POST", "/connections", map[string]any{"name": " Notes ", "kind": "obsidian", "config": map[string]any{"path": master}, "secret": secret})
	if r.code != 201 {
		t.Fatalf("create: %d %s", r.code, r.body)
	}
	if bytes.Contains(r.body, []byte(secret)) {
		t.Fatal("the secret must never be returned")
	}
	var conn struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		HasSecret bool   `json:"hasSecret"`
	}
	r.json(t, &conn)
	if conn.Name != "Notes" || !conn.HasSecret {
		t.Fatalf("created: %s", r.body)
	}
	if r := c.do("POST", "/connections", map[string]any{"name": "notes", "kind": "obsidian", "config": map[string]any{"path": master}}); r.code != 409 {
		t.Fatalf("duplicate name: %d", r.code)
	}

	// Update without "secret" keeps it; "" removes it.
	r = c.do("PUT", "/connections/"+conn.ID, map[string]any{"name": "Notes", "kind": "obsidian", "config": map[string]any{"path": master}})
	r.json(t, &conn)
	if r.code != 200 || !conn.HasSecret {
		t.Fatalf("update keep secret: %d %s", r.code, r.body)
	}
	r = c.do("PUT", "/connections/"+conn.ID, map[string]any{"name": "Notes", "kind": "obsidian", "config": map[string]any{"path": master}, "secret": ""})
	r.json(t, &conn)
	if conn.HasSecret {
		t.Fatal("empty secret must clear it")
	}
	if r := c.do("PUT", "/connections/"+conn.ID, map[string]any{"name": "Notes", "kind": "other", "config": map[string]any{}}); r.code != 400 {
		t.Fatalf("kind change: %d", r.code)
	}
	if r := c.do("GET", "/connections/nope", nil); r.code != 404 {
		t.Fatalf("missing: %d", r.code)
	}

	var tr struct {
		Ok      bool
		Message string
	}
	c.do("POST", "/connections/test", map[string]any{"kind": "obsidian", "config": map[string]any{"path": master}}).json(t, &tr)
	if !tr.Ok {
		t.Fatalf("test ok: %+v", tr)
	}
	c.do("POST", "/connections/test", map[string]any{"kind": "obsidian", "config": map[string]any{"path": filepath.Join(e.dir, "missing")}}).json(t, &tr)
	if tr.Ok || !strings.Contains(tr.Message, "does not exist") {
		t.Fatalf("test fail: %+v", tr)
	}
	if r := c.do("POST", "/connections/test", map[string]any{"kind": "obsidian", "config": map[string]any{"path": master}, "connectionId": conn.ID}); r.code != 200 {
		t.Fatalf("test saved: %d %s", r.code, r.body)
	}

	var nodes []map[string]any
	r = c.do("GET", "/connections/"+conn.ID+"/browse", nil)
	r.json(t, &nodes)
	if r.code != 200 || len(nodes) == 0 {
		t.Fatalf("browse: %d %s", r.code, r.body)
	}

	var list []map[string]any
	c.do("GET", "/connections", nil).json(t, &list)
	if len(list) != 1 {
		t.Fatalf("list: %v", list)
	}
	if r := c.do("DELETE", "/connections/"+conn.ID, nil); r.code != 204 {
		t.Fatalf("delete: %d %s", r.code, r.body)
	}
	if r := c.do("DELETE", "/connections/"+conn.ID, nil); r.code != 404 {
		t.Fatalf("delete twice: %d", r.code)
	}
}

func TestFlowsAndRuns(t *testing.T) {
	e := newEnv(t, ModeWeb)
	c := e.signIn()
	master, mirror := e.vaults()

	mk := func(name, path string) string {
		var out struct{ ID string }
		r := c.do("POST", "/connections", map[string]any{"name": name, "kind": "obsidian", "config": map[string]any{"path": path}})
		if r.code != 201 {
			t.Fatalf("create connection: %d %s", r.code, r.body)
		}
		r.json(t, &out)
		return out.ID
	}
	src, dst := mk("Notes", master), mk("Team", mirror)

	bad := map[string]any{"name": "", "sourceConnectionId": src, "scope": map[string]any{"mode": "selected"}, "options": map[string]any{},
		"targets": []map[string]any{{"connectionId": src, "location": map[string]any{}}}}
	r := c.do("POST", "/flows", bad)
	f := r.fields(t)
	if r.code != 400 || f["name"] == "" || f["scope.include"] == "" || f["targets.0.connectionId"] == "" {
		t.Fatalf("validation: %d %s", r.code, r.body)
	}

	in := map[string]any{
		"name": "Notes to team", "sourceConnectionId": src, "scope": map[string]any{"mode": "all"},
		"options": map[string]any{"deletePolicy": "archive", "banner": true, "bannerText": "Copy of {master}"},
		"targets": []map[string]any{{"connectionId": dst, "location": map[string]any{"folder": " Mirror "}}},
	}
	r = c.do("POST", "/flows", in)
	if r.code != 201 {
		t.Fatalf("create flow: %d %s", r.code, r.body)
	}
	var flow struct {
		ID       string
		Timezone string
		Targets  []struct {
			ID       string
			Location map[string]string
		}
		Options struct{ BannerText string }
		LastRun *struct{ ID string }
	}
	r.json(t, &flow)
	if flow.Timezone != "Europe/Rome" || len(flow.Targets) != 1 || flow.Targets[0].Location["folder"] != "Mirror" ||
		flow.Options.BannerText != "Copy of {master}" || flow.LastRun != nil {
		t.Fatalf("created flow: %s", r.body)
	}
	if r := c.do("POST", "/flows", in); r.code != 409 {
		t.Fatalf("duplicate flow name: %d", r.code)
	}
	if r := c.do("DELETE", "/connections/"+dst, nil); r.code != 409 || r.errCode(t) != "in_use" {
		t.Fatalf("delete used connection: %d %s", r.code, r.body)
	}

	// Update keeps the target id when it is passed back.
	in["targets"] = []map[string]any{{"id": flow.Targets[0].ID, "connectionId": dst, "location": map[string]any{"folder": "Mirror"}}}
	in["enabled"] = false
	r = c.do("PUT", "/flows/"+flow.ID, in)
	var upd struct {
		Enabled bool
		Targets []struct{ ID string }
	}
	r.json(t, &upd)
	if r.code != 200 || upd.Enabled || upd.Targets[0].ID != flow.Targets[0].ID {
		t.Fatalf("update: %d %s", r.code, r.body)
	}
	in["targets"] = []map[string]any{{"id": "someone-else", "connectionId": dst, "location": map[string]any{}}}
	if r := c.do("PUT", "/flows/"+flow.ID, in); r.code != 400 {
		t.Fatalf("foreign target id: %d", r.code)
	}

	// Dry run, then a real run.
	runID := c.startRun(flow.ID, true)
	run := c.waitRun(runID)
	if run["status"] != "succeeded" || run["dryRun"] != true {
		t.Fatalf("dry run: %v", run)
	}
	if entries, _ := os.ReadDir(mirror); len(entries) != 0 {
		t.Fatal("dry run wrote to the mirror")
	}
	runID = c.startRun(flow.ID, false)
	run = c.waitRun(runID)
	if run["status"] != "succeeded" {
		t.Fatalf("run: %v", run)
	}
	if _, err := os.Stat(filepath.Join(mirror, "Mirror")); err != nil {
		t.Fatalf("mirror not written: %v", err)
	}
	report := run["report"].(map[string]any)
	if targets := report["targets"].([]any); len(targets) != 1 {
		t.Fatalf("report: %v", report)
	}

	var list struct {
		Items      []map[string]any
		NextBefore *string
	}
	c.do("GET", "/runs?limit=1", nil).json(t, &list)
	if len(list.Items) != 1 || list.Items[0]["id"] != runID || list.NextBefore == nil {
		t.Fatalf("page 1: %+v", list)
	}
	next := *list.NextBefore
	list.Items, list.NextBefore = nil, nil
	c.do("GET", "/runs?limit=1&before="+next, nil).json(t, &list)
	if len(list.Items) != 1 || list.Items[0]["dryRun"] != true || list.NextBefore != nil {
		t.Fatalf("page 2: %+v", list)
	}
	c.do("GET", "/runs?dryRun=false&flowId="+flow.ID+"&status=succeeded", nil).json(t, &list)
	if len(list.Items) != 1 {
		t.Fatalf("filtered: %+v", list)
	}
	if r := c.do("GET", "/runs?limit=0", nil); r.code != 400 {
		t.Fatalf("limit 0: %d", r.code)
	}
	if r := c.do("GET", "/runs?status=bogus", nil); r.code != 400 {
		t.Fatalf("bad status: %d", r.code)
	}

	var events []map[string]any
	c.do("GET", "/runs/"+runID+"/events", nil).json(t, &events)
	if len(events) == 0 {
		t.Fatal("no log lines")
	}
	last := int64(events[len(events)-1]["id"].(float64))
	c.do("GET", fmt.Sprintf("/runs/%s/events?after=%d", runID, last), nil).json(t, &events)
	if len(events) != 0 {
		t.Fatalf("after last: %v", events)
	}

	logs, end := c.stream(runID, "")
	if len(logs) == 0 || end["status"] != "succeeded" {
		t.Fatalf("stream: %d lines, end %v", len(logs), end)
	}
	logs, _ = c.stream(runID, fmt.Sprint(last))
	if len(logs) != 0 {
		t.Fatalf("resume after the last id replayed %d lines", len(logs))
	}

	if r := c.do("POST", "/runs/"+runID+"/cancel", nil); r.code != 409 || r.errCode(t) != "not_running" {
		t.Fatalf("cancel finished: %d %s", r.code, r.body)
	}
	var fl []map[string]any
	c.do("GET", "/flows", nil).json(t, &fl)
	if lr, _ := fl[0]["lastRun"].(map[string]any); lr == nil || lr["id"] != runID || fl[0]["running"] != nil {
		t.Fatalf("flow list state: %v", fl[0])
	}
	if r := c.do("POST", "/flows/missing/runs", map[string]any{}); r.code != 404 {
		t.Fatalf("run missing flow: %d", r.code)
	}
	if r := c.do("DELETE", "/flows/"+flow.ID, nil); r.code != 204 {
		t.Fatalf("delete flow: %d %s", r.code, r.body)
	}
	if r := c.do("GET", "/runs/"+runID, nil); r.code != 404 {
		t.Fatalf("runs go with the flow: %d", r.code)
	}
}

func TestStartRunWhileLocked(t *testing.T) {
	e := newEnv(t, ModeWeb)
	c := e.signIn()
	master, mirror := e.vaults()
	ctx := context.Background()
	cfg := func(p string) json.RawMessage { b, _ := json.Marshal(map[string]string{"path": p}); return b }
	src, _ := e.st.SaveConnection(ctx, store.Connection{Name: "A", Kind: "obsidian", Config: cfg(master)}, nil, nil)
	dst, _ := e.st.SaveConnection(ctx, store.Connection{Name: "B", Kind: "obsidian", Config: cfg(mirror)}, nil, nil)
	flow, err := e.st.SaveFlow(ctx, store.Flow{Name: "F", Enabled: true, SourceConnectionID: src.ID,
		Targets: []store.FlowTarget{{ConnectionID: dst.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	// Another process holds the lock.
	if _, err := e.st.AcquireLock(ctx, flow.ID, "other-run", "cli/42", time.Hour); err != nil {
		t.Fatal(err)
	}
	if r := c.do("POST", "/flows/"+flow.ID+"/runs", map[string]any{}); r.code != 409 || r.errCode(t) != "running" {
		t.Fatalf("start while locked: %d %s", r.code, r.body)
	}
	var got map[string]any
	c.do("GET", "/flows/"+flow.ID, nil).json(t, &got)
	if ri, _ := got["running"].(map[string]any); ri == nil || ri["holder"] != "cli/42" {
		t.Fatalf("running info: %v", got)
	}
	if r := c.do("DELETE", "/flows/"+flow.ID, nil); r.code != 409 {
		t.Fatalf("delete while running: %d", r.code)
	}
}

func (c *client) startRun(flowID string, dry bool) string {
	c.e.t.Helper()
	r := c.do("POST", "/flows/"+flowID+"/runs", map[string]any{"dryRun": dry})
	if r.code != 202 {
		c.e.t.Fatalf("start run: %d %s", r.code, r.body)
	}
	var run struct{ ID, Status string }
	r.json(c.e.t, &run)
	return run.ID
}

func (c *client) waitRun(id string) map[string]any {
	c.e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var run map[string]any
		c.do("GET", "/runs/"+id, nil).json(c.e.t, &run)
		if run["status"] != "running" {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.e.t.Fatal("run did not finish")
	return nil
}

// stream reads the SSE stream to the end over a real HTTP connection.
func (c *client) stream(runID, lastEventID string) (logs []map[string]any, end map[string]any) {
	t := c.e.t
	t.Helper()
	srv := httptest.NewServer(c.e.h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/runs/"+runID+"/stream", nil)
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var event string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var v map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v); err != nil {
				t.Fatalf("bad data line %q", line)
			}
			if event == "end" {
				return logs, v
			}
			logs = append(logs, v)
		}
	}
	t.Fatalf("stream ended without an end event (%v)", sc.Err())
	return nil, nil
}

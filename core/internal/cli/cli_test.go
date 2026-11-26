package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Execute(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, "version")
	if code != ExitOK || !strings.HasPrefix(out, "tolarix ") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, out, _ = run(t, "version", "--json")
	var v map[string]any
	if code != ExitOK || json.Unmarshal([]byte(out), &v) != nil || v["version"] == nil {
		t.Fatalf("json: code=%d out=%q", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	if code, _, _ := run(t, "no-such-command"); code != ExitUsage {
		t.Errorf("unknown command: %d", code)
	}
	if code, _, _ := run(t, "version", "--bogus"); code != ExitUsage {
		t.Errorf("unknown flag: %d", code)
	}
	if code, _, _ := run(t, "--log-level", "loud", "version"); code != ExitUsage {
		t.Errorf("bad log level: %d", code)
	}
	if code, _, _ := run(t, "serve", "--mode", "kiosk", "--data-dir", t.TempDir()); code != ExitUsage {
		t.Errorf("bad mode: %d", code)
	}
	if code, _, errs := run(t, "serve", "--mode", "desktop", "--data-dir", t.TempDir()); code != ExitUsage || !strings.Contains(errs, "--token") {
		t.Errorf("desktop without token: %d %q", code, errs)
	}
}

func TestServeDesktopPrintsReadyLineAndServes(t *testing.T) {
	t.Setenv("TOLARIX_SECRET_KEY", "")
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	token := "0123456789abcdef-token"
	go func() {
		done <- Execute(ctx, []string{"serve", "--mode", "desktop", "--token", token, "--data-dir", t.TempDir()}, pw, io.Discard)
		_ = pw.Close()
	}()

	lineCh := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(pr)
		if s.Scan() {
			lineCh <- s.Text()
		}
		_, _ = io.Copy(io.Discard, pr)
	}()

	var line string
	select {
	case line = <-lineCh:
	case <-time.After(60 * time.Second):
		t.Fatal("no ready line")
	}
	if !strings.HasPrefix(line, ReadyLinePrefix) {
		t.Fatalf("ready line %q", line)
	}
	var ready struct {
		Port int    `json:"port"`
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, ReadyLinePrefix)), &ready); err != nil || ready.Port == 0 || ready.Mode != "desktop" {
		t.Fatalf("ready payload %q: %v", line, err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(ready.Port)

	resp, err := http.Get(base + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", err, resp)
	}
	resp.Body.Close()
	resp, err = http.Get(base + "/")
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("root without token: %v %v", err, resp)
	}
	resp.Body.Close()

	cancel()
	select {
	case code := <-done:
		if code != ExitOK {
			t.Fatalf("serve exit code %d", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not shut down")
	}
}

// syncBuffer is a bytes.Buffer safe for a writer and a reader goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestServeWebFirstStartPasswordAndHealthcheck(t *testing.T) {
	t.Setenv("TOLARIX_SECRET_KEY", "")
	dir := t.TempDir()
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	var stderr syncBuffer
	go func() {
		done <- Execute(ctx, []string{"serve", "--host", "127.0.0.1", "--port", "0", "--data-dir", dir}, pw, &stderr)
		_ = pw.Close()
	}()
	lineCh := make(chan string, 1)
	go func() {
		s := bufio.NewScanner(pr)
		if s.Scan() {
			lineCh <- s.Text()
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	var line string
	select {
	case line = <-lineCh:
	case <-time.After(60 * time.Second):
		t.Fatal("no ready line")
	}
	var ready struct{ Port int }
	_ = json.Unmarshal([]byte(strings.TrimPrefix(line, ReadyLinePrefix)), &ready)
	base := "http://127.0.0.1:" + strconv.Itoa(ready.Port)

	m := regexp.MustCompile(`password: (\S+)`).FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("no one-time password on stderr:\n%s", stderr.String())
	}
	res, err := http.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(`{"password":"`+m[1]+`"}`))
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("login with the printed password: %v %v", err, res)
	}
	res.Body.Close()

	if code, out, errs := run(t, "healthcheck", "--url", base+"/healthz"); code != ExitOK || strings.TrimSpace(out) != "ok" {
		t.Fatalf("healthcheck: %d %q %q", code, out, errs)
	}
	if code, _, _ := run(t, "healthcheck", "--url", "http://127.0.0.1:1/healthz", "--timeout", "1s"); code != ExitFailure {
		t.Fatalf("healthcheck of nothing: %d", code)
	}

	// A reset prints a new password and the old one stops working.
	code, out, _ := run(t, "admin", "reset-password", "--data-dir", dir)
	m2 := regexp.MustCompile(`password: (\S+)`).FindStringSubmatch(out)
	if code != ExitOK || m2 == nil || m2[1] == m[1] {
		t.Fatalf("reset: %d %q", code, out)
	}
	res, _ = http.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(`{"password":"`+m[1]+`"}`))
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password still accepted: %d", res.StatusCode)
	}
	res.Body.Close()

	cancel()
	select {
	case code := <-done:
		if code != ExitOK {
			t.Fatalf("serve exit code %d", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not shut down")
	}
}

// The desktop app passes the token in the environment and holds stdin open;
// closing it (app quit or crash) stops the server cleanly.
func TestServeDesktopStopsWhenStdinCloses(t *testing.T) {
	t.Setenv("TOLARIX_SECRET_KEY", "")
	t.Setenv("TOLARIX_DESKTOP_TOKEN", "0123456789abcdef-token")
	stdinR, stdinW := io.Pipe()
	out := &syncBuffer{}
	root := NewRootCommand(out, io.Discard)
	root.SetIn(stdinR)
	root.SetArgs([]string{"serve", "--mode", "desktop", "--stop-on-stdin-close", "--data-dir", t.TempDir()})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(context.Background()) }()

	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(out.String(), ReadyLinePrefix) {
		if time.Now().After(deadline) {
			t.Fatal("no ready line")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = stdinW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("serve did not stop when stdin closed")
	}
}

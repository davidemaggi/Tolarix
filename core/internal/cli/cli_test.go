package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
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

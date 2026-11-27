package drawio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/render"
)

const diagram = `<mxfile><diagram name="Page-1">abc</diagram></mxfile>`

var png = append([]byte("\x89PNG\r\n\x1a\n"), "rendered"...)

// isolated returns a renderer that finds no draw.io Desktop on this machine.
func isolated(t *testing.T) *Renderer {
	t.Helper()
	notFound := errors.New("not found")
	return &Renderer{
		lookPath: func(string) (string, error) { return "", notFound },
		stat:     func(string) (os.FileInfo, error) { return nil, notFound },
		getenv:   func(string) string { return "" },
	}
}

func input(src *model.Attachment, siblings map[*model.Attachment][]byte) render.Input {
	list := []*model.Attachment{src}
	for a := range siblings {
		list = append(list, a)
	}
	return render.Input{
		Page: &model.Page{ID: "p"}, Source: src, Siblings: list,
		Open: func() (io.ReadCloser, error) { return render.NewBlobReader([]byte(diagram)), nil },
		OpenAny: func(a *model.Attachment) (io.ReadCloser, error) {
			if b, ok := siblings[a]; ok {
				return render.NewBlobReader(b), nil
			}
			return nil, errors.New("unknown attachment")
		},
	}
}

func source() *model.Attachment {
	return &model.Attachment{Hash: model.HashBytes([]byte(diagram)), Name: "Architecture.drawio", MediaType: mediaType}
}

func TestMatch(t *testing.T) {
	r := isolated(t)
	for name, want := range map[string]bool{"a.drawio": true, "A.DRAWIO": true, "a.drawio.png": false, "a.drawio.svg": false, "a.png": false} {
		if got := r.Match(&model.Attachment{Name: name}); got != want {
			t.Errorf("Match(%s) = %v", name, got)
		}
	}
	if !r.Match(&model.Attachment{Name: "arch", Role: model.RoleDiagramSource}) {
		t.Error("a diagram source without extension must match")
	}
}

func TestUsesThePreviewNextToTheDiagram(t *testing.T) {
	r := isolated(t)
	r.ExportURL = "http://127.0.0.1:1/never-called"
	prev := &model.Attachment{Hash: "p", Name: "architecture.png", MediaType: "image/png"}
	outs, err := r.Render(context.Background(), input(source(), map[*model.Attachment][]byte{prev: png}))
	if err != nil || len(outs) != 1 || string(outs[0].Content) != string(png) || outs[0].Name != "Architecture.png" {
		t.Fatalf("outs = %+v, %v", outs, err)
	}
	// A preview that is not a PNG is ignored.
	prev2 := &model.Attachment{Hash: "q", Name: "Architecture.png"}
	r.ExportURL = ""
	if _, err := r.Render(context.Background(), input(source(), map[*model.Attachment][]byte{prev2: []byte("<html>")})); err == nil || !strings.Contains(err.Error(), EnvExportURL) {
		t.Fatalf("err = %v", err)
	}
}

func TestExportServerAndCache(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil || r.Method != http.MethodPost || r.Form.Get("format") != "png" || r.Form.Get("xml") != diagram {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()
	r := isolated(t)
	r.ExportURL = srv.URL
	r.CacheDir = t.TempDir()
	for i := 0; i < 2; i++ {
		outs, err := r.Render(context.Background(), input(source(), nil))
		if err != nil || string(outs[0].Content) != string(png) || outs[0].MediaType != "image/png" {
			t.Fatalf("run %d: %+v, %v", i, outs, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("export server called %d times; the second render must come from the cache", calls.Load())
	}
}

func TestExportServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/html" {
			_, _ = w.Write([]byte("<html>oops</html>"))
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	r := isolated(t)
	r.ExportURL = srv.URL + "/fail"
	if _, err := r.Render(context.Background(), input(source(), nil)); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v", err)
	}
	r.ExportURL = srv.URL + "/html"
	if _, err := r.Render(context.Background(), input(source(), nil)); err == nil || !strings.Contains(err.Error(), "not a PNG") {
		t.Fatalf("err = %v", err)
	}
}

func TestDesktopCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake draw.io")
	}
	dir := t.TempDir()
	cli := filepath.Join(dir, "drawio")
	// The fake checks its arguments and writes a PNG to --output.
	script := `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in --output) out="$2"; shift;; esac
  shift
done
[ -n "$out" ] || { echo "no --output" >&2; exit 2; }
printf '\211PNG\r\n\032\nfromcli' > "$out"
`
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil { //nolint:gosec // test executable
		t.Fatal(err)
	}
	r := isolated(t)
	r.lookPath = func(name string) (string, error) {
		if name == "drawio" {
			return cli, nil
		}
		return "", errors.New("not found")
	}
	outs, err := r.Render(context.Background(), input(source(), nil))
	if err != nil || !strings.HasSuffix(string(outs[0].Content), "fromcli") {
		t.Fatalf("outs = %+v, %v", outs, err)
	}

	// A failing CLI reports its stderr.
	if err := os.WriteFile(cli, []byte("#!/bin/sh\necho 'cannot open display' >&2\nexit 1\n"), 0o700); err != nil { //nolint:gosec // test executable
		t.Fatal(err)
	}
	if _, err := r.Render(context.Background(), input(source(), nil)); err == nil || !strings.Contains(err.Error(), "cannot open display") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvExportURL, " http://export:8000/ ")
	t.Setenv(EnvCLI, "/opt/drawio")
	r := New("/data")
	if r.ExportURL != "http://export:8000/" || r.CLI != "/opt/drawio" || r.CacheDir != filepath.Join("/data", "cache", "render") {
		t.Fatalf("%+v", r)
	}
	if r.findCLI() != "/opt/drawio" {
		t.Fatal("configured CLI not used")
	}
}

type fileInfo struct{ os.FileInfo }

func (fileInfo) IsDir() bool { return false }

func TestFindsTheInstalledDesktopApp(t *testing.T) {
	env := map[string]string{
		"ProgramFiles": `C:\Program Files`, "LOCALAPPDATA": `C:\Users\u\AppData\Local`, "HOME": "/home/u",
	}
	for _, tc := range []struct{ goos, installed string }{
		{"windows", filepath.Join(`C:\Program Files`, "draw.io", "draw.io.exe")},
		{"windows", filepath.Join(`C:\Users\u\AppData\Local`, "Programs", "draw.io", "draw.io.exe")},
		{"darwin", "/Applications/draw.io.app/Contents/MacOS/draw.io"},
		{"darwin", filepath.Join("/home/u", "Applications", "draw.io.app", "Contents", "MacOS", "draw.io")},
		{"linux", "/opt/drawio/drawio"},
		{"linux", "/snap/bin/drawio"},
	} {
		r := isolated(t)
		r.goos = tc.goos
		r.getenv = func(k string) string { return env[k] }
		if got := r.findCLI(); got != "" {
			t.Fatalf("%s: found %q with nothing installed", tc.goos, got)
		}
		r.stat = func(p string) (os.FileInfo, error) {
			if p == tc.installed {
				return fileInfo{}, nil
			}
			return nil, errors.New("not found")
		}
		if got := r.findCLI(); got != tc.installed {
			t.Errorf("%s: findCLI = %q, want %q", tc.goos, got, tc.installed)
		}
	}
}

func TestNoRendererSaysWhereItLooked(t *testing.T) {
	r := isolated(t)
	r.goos = "darwin"
	_, err := r.Render(context.Background(), input(source(), nil))
	if err == nil || !strings.Contains(err.Error(), "/Applications/draw.io.app") || !strings.Contains(err.Error(), EnvCLI) {
		t.Fatalf("err = %v", err)
	}
}

func TestHeadlessLinuxUsesXvfb(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake xvfb-run")
	}
	dir := t.TempDir()
	xvfb := filepath.Join(dir, "xvfb-run")
	// The fake checks it wraps draw.io, then writes a PNG to --output.
	script := `#!/bin/sh
[ "$1" = "-a" ] && [ "$2" = "/opt/drawio/drawio" ] || { echo "bad args: $*" >&2; exit 2; }
out=""
while [ $# -gt 0 ]; do
  case "$1" in --output) out="$2"; shift;; esac
  shift
done
printf '\211PNG\r\n\032\nxvfb' > "$out"
`
	if err := os.WriteFile(xvfb, []byte(script), 0o700); err != nil { //nolint:gosec // test executable
		t.Fatal(err)
	}
	r := isolated(t)
	r.goos = "linux"
	r.CLI = "/opt/drawio/drawio"
	r.lookPath = func(name string) (string, error) {
		if name == "xvfb-run" {
			return xvfb, nil
		}
		return "", errors.New("not found")
	}
	outs, err := r.Render(context.Background(), input(source(), nil))
	if err != nil || !strings.HasSuffix(string(outs[0].Content), "xvfb") {
		t.Fatalf("outs = %+v, %v", outs, err)
	}

	// With a display, draw.io runs directly; without xvfb-run the error says why.
	r.getenv = func(k string) string { return map[string]string{"DISPLAY": ":0"}[k] }
	if r.headless() {
		t.Fatal("a display was set")
	}
	r.getenv = func(string) string { return "" }
	r.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	r.CLI = filepath.Join(dir, "missing-drawio")
	if _, err := r.Render(context.Background(), input(source(), nil)); err == nil || !strings.Contains(err.Error(), "xvfb") {
		t.Fatalf("err = %v", err)
	}
}

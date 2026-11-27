// Package drawio renders draw.io diagrams to PNG for mirrors (see
// docs/dev/renderers.md). The PNG comes from, in order:
//
//  1. a preview stored next to the diagram (Confluence's draw.io app keeps
//     "<name>.png" beside "<name>"); no rendering needed;
//  2. the render cache in the data directory;
//  3. an export server (TOLARIX_DRAWIO_EXPORT_URL, e.g. jgraph/export-server);
//  4. draw.io Desktop (TOLARIX_DRAWIO_CLI, "drawio" on PATH, or where its
//     installers put it on Windows, macOS and Linux).
package drawio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/render"
)

// Environment variables that enable rendering.
const (
	EnvExportURL = "TOLARIX_DRAWIO_EXPORT_URL"
	EnvCLI       = "TOLARIX_DRAWIO_CLI"
)

// version is part of the cache key: bump it when the output changes.
const version = "1"

const (
	mediaType = "application/vnd.jgraph.mxfile"
	maxSource = 32 << 20 // diagrams are XML; anything bigger is not one
	maxPNG    = 64 << 20
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// Renderer is the draw.io renderer.
type Renderer struct {
	// CacheDir keeps rendered PNGs by source hash ("" = no cache).
	CacheDir string
	// ExportURL is an export server endpoint ("" = none).
	ExportURL string
	// CLI is the draw.io Desktop executable ("" = look for one).
	CLI string
	// Timeout bounds one export (default 60s).
	Timeout time.Duration
	// HTTP is the client for the export server (default: a plain client).
	HTTP *http.Client

	lookPath func(string) (string, error)
	stat     func(string) (os.FileInfo, error)
	getenv   func(string) string
	goos     string
}

var _ render.Renderer = (*Renderer)(nil)

// New configures the renderer from the environment, caching under
// <dataDir>/cache/render.
func New(dataDir string) *Renderer {
	r := &Renderer{ExportURL: strings.TrimSpace(os.Getenv(EnvExportURL)), CLI: strings.TrimSpace(os.Getenv(EnvCLI))}
	if dataDir != "" {
		r.CacheDir = filepath.Join(dataDir, "cache", "render")
	}
	return r
}

func (r *Renderer) Name() string { return "draw.io" }

// Match accepts diagram sources: .drawio files or attachments a connector
// marked as diagram sources. Images with an embedded diagram
// (.drawio.png, .drawio.svg) already display and are left alone.
func (r *Renderer) Match(a *model.Attachment) bool {
	return a.Role == model.RoleDiagramSource || strings.EqualFold(path.Ext(a.Name), ".drawio") || a.MediaType == mediaType
}

// Render returns the PNG for in.Source.
func (r *Renderer) Render(ctx context.Context, in render.Input) ([]render.Output, error) {
	base := strings.TrimSuffix(in.Source.Name, path.Ext(in.Source.Name))
	if !strings.EqualFold(path.Ext(in.Source.Name), ".drawio") {
		base = in.Source.Name
	}
	out := func(b []byte) []render.Output {
		return []render.Output{{Name: base + ".png", MediaType: "image/png", Content: b}}
	}

	// 1. A preview next to the diagram.
	if b, ok := r.preview(in, base); ok {
		return out(b), nil
	}

	// 2. The cache.
	cache := r.cachePath(in.Source.Hash)
	if cache != "" {
		if b, err := os.ReadFile(cache); err == nil && isPNG(b) { //nolint:gosec // our cache
			return out(b), nil
		}
	}

	src, err := readAll(in.Open, maxSource)
	if err != nil {
		return nil, fmt.Errorf("read the diagram: %w", err)
	}
	var tried []string
	save := func(b []byte) []render.Output {
		if cache != "" {
			if err := os.MkdirAll(filepath.Dir(cache), 0o750); err == nil {
				_ = os.WriteFile(cache, b, 0o600) //nolint:gosec // the name is a sha256 under our cache dir
			}
		}
		return out(b)
	}

	// 3. The export server.
	if r.ExportURL != "" {
		b, err := r.exportServer(ctx, src)
		if err == nil {
			return save(b), nil
		}
		tried = append(tried, "export server: "+err.Error())
	}

	// 4. draw.io Desktop.
	if cli := r.findCLI(); cli != "" {
		b, err := r.desktop(ctx, cli, src)
		if err == nil {
			return save(b), nil
		}
		tried = append(tried, "draw.io Desktop: "+err.Error())
	}

	if len(tried) == 0 {
		return nil, fmt.Errorf("no draw.io renderer is configured: set %s to an export server (see docker-compose.yml), or install draw.io Desktop (looked on PATH and in %s) or set %s to its executable",
			EnvExportURL, strings.Join(r.installPaths(), ", "), EnvCLI)
	}
	return nil, errors.New(strings.Join(tried, "; "))
}

// preview finds "<base>.png" or "<base>.drawio.png" among the page's
// attachments.
func (r *Renderer) preview(in render.Input, base string) ([]byte, bool) {
	if in.OpenAny == nil {
		return nil, false
	}
	for _, a := range in.Siblings {
		if a.Hash == in.Source.Hash || a.Role == model.RoleRendered {
			continue
		}
		if !strings.EqualFold(a.Name, base+".png") && !strings.EqualFold(a.Name, base+".drawio.png") {
			continue
		}
		b, err := readAll(func() (io.ReadCloser, error) { return in.OpenAny(a) }, maxPNG)
		if err == nil && isPNG(b) {
			return b, true
		}
	}
	return nil, false
}

func (r *Renderer) cachePath(sourceHash string) string {
	if r.CacheDir == "" || sourceHash == "" {
		return ""
	}
	h := sha256.Sum256([]byte("drawio|" + version + "|" + sourceHash))
	k := hex.EncodeToString(h[:])
	return filepath.Join(r.CacheDir, k[:2], k+".png")
}

func (r *Renderer) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 60 * time.Second
}

// exportServer posts the diagram to a jgraph/export-server compatible
// endpoint (form fields format and xml).
func (r *Renderer) exportServer(ctx context.Context, src []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	form := url.Values{"format": {"png"}, "xml": {string(src)}, "border": {"10"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.ExportURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := r.HTTP
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPNG+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if !isPNG(b) || len(b) > maxPNG {
		return nil, errors.New("the answer is not a PNG image")
	}
	return b, nil
}

func (r *Renderer) env(key string) string {
	if r.getenv != nil {
		return r.getenv(key)
	}
	return os.Getenv(key)
}

func (r *Renderer) os() string {
	if r.goos != "" {
		return r.goos
	}
	return runtime.GOOS
}

func (r *Renderer) look(name string) (string, error) {
	if r.lookPath != nil {
		return r.lookPath(name)
	}
	return exec.LookPath(name)
}

// installPaths are where the draw.io Desktop installers put the executable
// (none of them adds it to PATH on Windows or macOS).
func (r *Renderer) installPaths() []string {
	var out []string
	add := func(base string, elem ...string) {
		if base != "" {
			out = append(out, filepath.Join(append([]string{base}, elem...)...))
		}
	}
	switch r.os() {
	case "windows":
		add(r.env("ProgramFiles"), "draw.io", "draw.io.exe")
		add(r.env("ProgramFiles(x86)"), "draw.io", "draw.io.exe")
		add(r.env("LOCALAPPDATA"), "Programs", "draw.io", "draw.io.exe") // per-user install
	case "darwin":
		out = append(out, "/Applications/draw.io.app/Contents/MacOS/draw.io")
		add(r.env("HOME"), "Applications", "draw.io.app", "Contents", "MacOS", "draw.io")
	case "linux":
		// .deb/.rpm, snap and flatpak; apps started from a desktop launcher
		// often lack /snap/bin or the flatpak exports on PATH.
		out = append(out, "/opt/drawio/drawio", "/snap/bin/drawio", "/var/lib/flatpak/exports/bin/com.jgraph.drawio.desktop")
		add(r.env("HOME"), ".local", "share", "flatpak", "exports", "bin", "com.jgraph.drawio.desktop")
	}
	return out
}

// findCLI returns the draw.io Desktop executable, or "".
func (r *Renderer) findCLI() string {
	if r.CLI != "" {
		return r.CLI
	}
	stat := r.stat
	if stat == nil {
		stat = os.Stat
	}
	for _, name := range []string{"drawio", "draw.io"} {
		if p, err := r.look(name); err == nil {
			return p
		}
	}
	for _, p := range r.installPaths() {
		if fi, err := stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// headless reports a Linux machine without a display, where Electron
// cannot start on its own.
func (r *Renderer) headless() bool {
	return r.os() == "linux" && r.env("DISPLAY") == "" && r.env("WAYLAND_DISPLAY") == ""
}

// desktop runs draw.io Desktop's command-line export.
func (r *Renderer) desktop(ctx context.Context, cli string, src []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	dir, err := os.MkdirTemp("", "tolarix-drawio-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, outFile := filepath.Join(dir, "diagram.drawio"), filepath.Join(dir, "diagram.png")
	if err := os.WriteFile(in, src, 0o600); err != nil {
		return nil, err
	}
	args := []string{"--export", "--format", "png", "--border", "10", "--output", outFile, in}
	if r.os() == "linux" {
		args = append(args, "--no-sandbox") // Electron refuses to start as root in containers otherwise
	}
	hint := ""
	if r.headless() {
		// Without a display Electron exits at once: use a virtual one.
		if xvfb, err := r.look("xvfb-run"); err == nil {
			args = append([]string{"-a", cli}, args...)
			cli = xvfb
		} else {
			hint = " (this machine has no display: install xvfb so xvfb-run is on PATH, or use an export server)"
		}
	}
	cmd := exec.CommandContext(ctx, cli, args...) //nolint:gosec // the executable is configured by the operator
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg != "" {
			return nil, fmt.Errorf("%w: %s%s", err, msg, hint)
		}
		return nil, fmt.Errorf("%w%s", err, hint)
	}
	b, err := os.ReadFile(outFile) //nolint:gosec // our temp file
	if err != nil {
		return nil, fmt.Errorf("no PNG was written%s: %w", hint, err)
	}
	if !isPNG(b) {
		return nil, errors.New("the output is not a PNG image")
	}
	return b, nil
}

func readAll(open func() (io.ReadCloser, error), limit int64) ([]byte, error) {
	rc, err := open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("the file is larger than %d MiB", limit>>20)
	}
	return b, nil
}

func isPNG(b []byte) bool { return bytes.HasPrefix(b, pngMagic) }

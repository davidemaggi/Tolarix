// Package webui embeds the built React app and serves it as a single-page app.
//
// The web build (`pnpm build` in /web) writes into ./dist. A fresh clone only
// has dist/.gitkeep, in which case a short "frontend not built" page is served.
package webui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // the embed directive guarantees dist exists
	}
	return sub
}

const notBuiltPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Tolarix</title></head>
<body style="font-family:system-ui;margin:3rem"><h1>Tolarix</h1>
<p>The web interface has not been built into this binary. Run <code>pnpm build</code> at the repository root, then rebuild the Go binary.</p>
</body></html>`

// Handler serves static files from fsys, falling back to index.html for
// client-side routes. Paths under /api/ are never handled here.
func Handler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if fi, err := fs.Stat(fsys, p); err == nil && !fi.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					// Vite emits content-hashed file names under assets/.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			// A missing file with an extension is a real 404, not a client route.
			if path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
		}
		serveIndex(w, r, fsys)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	w.Header().Set("Cache-Control", "no-cache")
	b, err := fs.ReadFile(fsys, "index.html")
	if errors.Is(err, fs.ErrNotExist) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(notBuiltPage))
		return
	}
	if err != nil {
		http.Error(w, "cannot read index.html", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(b)
}

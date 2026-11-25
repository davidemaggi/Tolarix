// Package buildinfo exposes version metadata injected at link time.
//
// Build with:
//
//	go build -ldflags "-X github.com/davidemaggi/tolarix/core/internal/buildinfo.Version=1.2.3 \
//	  -X github.com/davidemaggi/tolarix/core/internal/buildinfo.Commit=abc123 \
//	  -X github.com/davidemaggi/tolarix/core/internal/buildinfo.Date=2026-01-01T00:00:00Z"
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Set via -ldflags. Defaults apply to `go run` / `go build` without flags.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info is the version payload printed by `tolarix version` and returned by /healthz.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the build info, falling back to VCS data embedded by the Go toolchain.
func Get() Info {
	commit, date := Commit, Date
	if commit == "" || date == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					if commit == "" {
						commit = s.Value
					}
				case "vcs.time":
					if date == "" {
						date = s.Value
					}
				}
			}
		}
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return Info{
		Version:   Version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

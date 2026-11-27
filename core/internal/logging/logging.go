// Package logging configures the process-wide slog logger. Logs always go to
// stderr so that stdout stays clean for --json command output.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Options controls the logger.
type Options struct {
	Level  string // debug|info|warn|error
	Format string // text|json
}

// New builds a logger writing to w.
func New(w io.Writer, o Options) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(o.Level) {
	case "", "info":
		lvl = slog.LevelInfo
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q (use debug, info, warn or error)", o.Level)
	}
	ho := &slog.HandlerOptions{Level: lvl}
	switch strings.ToLower(o.Format) {
	case "", "text":
		return slog.New(slog.NewTextHandler(w, ho)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, ho)), nil
	default:
		return nil, fmt.Errorf("unknown log format %q (use text or json)", o.Format)
	}
}

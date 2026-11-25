// Package cli defines the tolarix command tree. Commands work directly on the
// database in the data directory; no running server is required.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/davidemaggi/tolarix/core/internal/datadir"
	"github.com/davidemaggi/tolarix/core/internal/logging"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

// Exit codes. Scripts and schedulers rely on these; never renumber them.
const (
	ExitOK          = 0 // success
	ExitFailure     = 1 // the command or the sync failed
	ExitUsage       = 2 // bad flags or arguments
	ExitPartial     = 3 // sync finished, but some pages failed
	ExitLocked      = 4 // the flow is already running elsewhere
	ExitNotFound    = 5 // the named flow/connection/run does not exist
	ExitConfigError = 6 // data directory, database or secret key problem
)

// ExitError carries an exit code up to main.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

func exitErr(code int, format string, a ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, a...)}
}

// globalOpts are the persistent flags shared by every command.
type globalOpts struct {
	dataDir   string
	logLevel  string
	logFormat string
}

// app is the per-invocation context built from global flags.
type app struct {
	opts   *globalOpts
	stdout io.Writer
	stderr io.Writer
	log    *slog.Logger
}

// resolveDataDir resolves and creates the data directory.
func (a *app) resolveDataDir() (string, error) {
	dir, err := datadir.Resolve(a.opts.dataDir)
	if err != nil {
		return "", &ExitError{Code: ExitConfigError, Err: err}
	}
	if err := datadir.Ensure(dir); err != nil {
		return "", &ExitError{Code: ExitConfigError, Err: err}
	}
	return dir, nil
}

// openStore resolves the data directory and opens the migrated database.
func (a *app) openStore(ctx context.Context) (*store.Store, string, error) {
	dir, err := a.resolveDataDir()
	if err != nil {
		return nil, "", err
	}
	s, err := store.Open(ctx, datadir.DatabasePath(dir), a.log)
	if err != nil {
		return nil, "", &ExitError{Code: ExitConfigError, Err: err}
	}
	return s, dir, nil
}

// NewRootCommand builds the command tree writing to the given streams.
func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	opts := &globalOpts{}
	a := &app{opts: opts, stdout: stdout, stderr: stderr}

	root := &cobra.Command{
		Use:           "tolarix",
		Short:         "Keep wikis in sync: one Master, many Mirrors",
		Long:          "Tolarix mirrors a Master wiki (Azure DevOps, Confluence, Obsidian) into one or more Mirror wikis, on a schedule or on demand.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			l, err := logging.New(stderr, logging.Options{
				Level:  firstNonEmpty(opts.logLevel, os.Getenv("TOLARIX_LOG_LEVEL")),
				Format: firstNonEmpty(opts.logFormat, os.Getenv("TOLARIX_LOG_FORMAT")),
			})
			if err != nil {
				return &ExitError{Code: ExitUsage, Err: err}
			}
			a.log = l
			slog.SetDefault(l)
			return nil
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &ExitError{Code: ExitUsage, Err: err}
	})

	pf := root.PersistentFlags()
	pf.StringVar(&opts.dataDir, "data-dir", "", "data directory (env "+datadir.EnvVar+"; default: OS app-data folder)")
	pf.StringVar(&opts.logLevel, "log-level", "", "log level: debug, info, warn, error (env TOLARIX_LOG_LEVEL; default info)")
	pf.StringVar(&opts.logFormat, "log-format", "", "log format: text or json (env TOLARIX_LOG_FORMAT; default text)")

	root.AddCommand(newVersionCommand(a), newServeCommand(a), newConnectionCommand(a), newFlowCommand(a), newRunsCommand(a),
		newAdminCommand(a), newHealthcheckCommand(a))
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := NewRootCommand(stdout, stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	fmt.Fprintln(stderr, "Error:", err)
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	// Cobra returns plain errors for unknown commands and bad arg counts.
	return ExitUsage
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

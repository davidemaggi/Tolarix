package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidemaggi/tolarix/core/internal/api"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

// ReadyLinePrefix starts the single stdout line printed once the server
// listens. The desktop shell parses it to learn the port.
const ReadyLinePrefix = "TOLARIX_READY "

type serveOpts struct {
	mode  string
	host  string
	port  int
	token string
}

func newServeCommand(a *app) *cobra.Command {
	o := &serveOpts{}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the web server and the built-in scheduler",
		Long: `Start the HTTP server that hosts the web interface and the API.

  --mode web      listens on all interfaces (default port 8080); admin login required. Used in Docker.
  --mode desktop  listens on 127.0.0.1 only; every request must carry --token. Started by the desktop app.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context(), a, o, cmd.Flags().Changed("port"))
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.mode, "mode", envOr("TOLARIX_MODE", "web"), "web or desktop (env TOLARIX_MODE)")
	f.StringVar(&o.host, "host", os.Getenv("TOLARIX_HOST"), "bind address (env TOLARIX_HOST; default 0.0.0.0 in web mode; desktop mode always uses 127.0.0.1)")
	f.IntVar(&o.port, "port", envInt("TOLARIX_PORT", 8080), "port; 0 picks a free port (env TOLARIX_PORT)")
	f.StringVar(&o.token, "token", "", "launch token required on every request (desktop mode; env TOLARIX_DESKTOP_TOKEN)")
	return cmd
}

func runServe(ctx context.Context, a *app, o *serveOpts, portSet bool) error {
	mode, ok := api.ParseMode(o.mode)
	if !ok {
		return exitErr(ExitUsage, "--mode must be web or desktop, got %q", o.mode)
	}
	host := o.host
	port := o.port
	token := firstNonEmpty(o.token, os.Getenv("TOLARIX_DESKTOP_TOKEN"))
	switch mode {
	case api.ModeDesktop:
		host = "127.0.0.1"
		if !portSet && os.Getenv("TOLARIX_PORT") == "" {
			port = 0
		}
		if len(token) < 16 {
			return exitErr(ExitUsage, "desktop mode needs --token with at least 16 characters")
		}
	case api.ModeWeb:
		if host == "" {
			host = "0.0.0.0"
		}
	}
	if port < 0 || port > 65535 {
		return exitErr(ExitUsage, "--port must be between 0 and 65535")
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, dir, err := a.openStore(ctx)
	if err != nil {
		return err
	}
	defer st.Close()

	if _, src, err := secrets.LoadKey(dir); err != nil {
		return &ExitError{Code: ExitConfigError, Err: err}
	} else if src == secrets.KeyFromGenerated {
		a.log.Warn("generated a new secret key; back it up together with the database, losing it means re-entering every connection secret",
			"file", secrets.KeyFileName)
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return exitErr(ExitFailure, "cannot listen on %s:%d: %w", host, port, err)
	}
	addr := ln.Addr().(*net.TCPAddr)

	srv := &http.Server{
		Handler: api.NewHandler(api.Config{
			Mode:         mode,
			DesktopToken: token,
			DB:           st,
			Logger:       a.log,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: run logs are streamed over long-lived SSE responses.
	}

	ready, _ := json.Marshal(map[string]any{"port": addr.Port, "mode": mode})
	fmt.Fprintf(a.stdout, "%s%s\n", ReadyLinePrefix, ready)
	a.log.Info("tolarix is listening", "mode", mode, "address", addr.String(), "data_dir", dir)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return exitErr(ExitFailure, "server stopped: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	a.log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return exitErr(ExitFailure, "shutdown: %w", err)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

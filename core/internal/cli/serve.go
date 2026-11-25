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
	"github.com/davidemaggi/tolarix/core/internal/auth"
	"github.com/davidemaggi/tolarix/core/internal/runner"
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

	key, src, err := secrets.LoadKey(dir)
	if err != nil {
		return &ExitError{Code: ExitConfigError, Err: err}
	}
	if src == secrets.KeyFromGenerated {
		a.log.Warn("generated a new secret key; back it up together with the database, losing it means re-entering every connection secret",
			"file", secrets.KeyFileName)
	}
	cipher, err := secrets.NewCipher(key)
	if err != nil {
		return &ExitError{Code: ExitConfigError, Err: err}
	}
	trusted, err := auth.ParseTrustedProxies(os.Getenv("TOLARIX_TRUSTED_PROXIES"))
	if err != nil {
		return exitErr(ExitConfigError, "TOLARIX_TRUSTED_PROXIES: %w", err)
	}
	cookieSecure, ok := api.ParseCookieSecure(os.Getenv("TOLARIX_COOKIE_SECURE"))
	if !ok {
		return exitErr(ExitConfigError, "TOLARIX_COOKIE_SECURE must be auto, true or false")
	}
	tz := firstNonEmpty(os.Getenv("TOLARIX_TIMEZONE"), os.Getenv("TZ"), "UTC")
	if _, err := time.LoadLocation(tz); err != nil {
		return exitErr(ExitConfigError, "unknown timezone %q (TOLARIX_TIMEZONE)", tz)
	}

	if n, err := st.MarkInterruptedRuns(ctx); err != nil {
		return err
	} else if n > 0 {
		a.log.Warn("marked runs interrupted by a previous stop as failed", "count", n)
	}

	authSvc := auth.New(st, a.log)
	if mode == api.ModeWeb {
		// The one-time password goes to stderr with the logs (docker logs).
		if err := authSvc.Bootstrap(ctx, a.stderr); err != nil {
			return exitErr(ExitConfigError, "create the admin account: %w", err)
		}
	}
	hub := runner.NewHub()
	rn := &runner.Runner{Store: st, Cipher: cipher, Log: a.log, Notify: hub.Notify, Renderers: renderers(dir)}
	bg := runner.NewBackground(rn)

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return exitErr(ExitFailure, "cannot listen on %s:%d: %w", host, port, err)
	}
	addr := ln.Addr().(*net.TCPAddr)

	// Request contexts derive from base; cancelling it on shutdown ends the
	// long-lived live-log streams so Shutdown does not wait for them.
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv := &http.Server{
		Handler: api.NewHandler(api.Config{
			Mode:         mode,
			DesktopToken: token,
			DB:           st,
			Logger:       a.log,
			API: &api.Backend{
				Store: st, Cipher: cipher, Auth: authSvc, Runner: rn, Background: bg, Hub: hub,
				TrustedProxies: trusted, CookieSecure: cookieSecure, Timezone: tz,
			},
		}),
		BaseContext:       func(net.Listener) context.Context { return base },
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: run logs are streamed over long-lived SSE responses.
	}
	srv.RegisterOnShutdown(cancelBase)

	ready, _ := json.Marshal(map[string]any{"port": addr.Port, "mode": mode})
	fmt.Fprintf(a.stdout, "%s%s\n", ReadyLinePrefix, ready)
	a.log.Info("tolarix is listening", "mode", mode, "address", addr.String(), "data_dir", dir)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = bg.Shutdown(stopCtx) // store the final status of runs started here
		if !errors.Is(err, http.ErrServerClosed) {
			return exitErr(ExitFailure, "server stopped: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	a.log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Cancel running syncs first so their final status is stored.
	if err := bg.Shutdown(shutdownCtx); err != nil {
		a.log.Error("runs did not stop in time", "error", err)
	}
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

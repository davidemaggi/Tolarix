package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidemaggi/tolarix/core/internal/auth"
)

func newAdminCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage the admin account of the web interface",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "reset-password",
		Short: "Set a new one-time admin password and sign out every session",
		Long: `Set a new one-time admin password, print it once, and sign out every
browser session. The password must be changed at the next sign-in.

In Docker: docker exec -it <container> /tolarix admin reset-password`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, _, err := a.openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()
			pw, err := auth.New(st, a.log).ResetPassword(cmd.Context())
			if err != nil {
				return err
			}
			auth.PrintPasswordBlock(a.stdout, "The admin password was reset. Every session was signed out.", pw)
			return nil
		},
	})
	return cmd
}

// newHealthcheckCommand checks a running server. The Docker image has no
// shell or curl, so its HEALTHCHECK runs this.
func newHealthcheckCommand(a *app) *cobra.Command {
	var url string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 if the local server answers /healthz (for container health checks)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if url == "" {
				url = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(envInt("TOLARIX_PORT", 8080))) + "/healthz"
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return exitErr(ExitUsage, "%w", err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return exitErr(ExitFailure, "server not reachable: %w", err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return exitErr(ExitFailure, "server unhealthy: HTTP %d", res.StatusCode)
			}
			fmt.Fprintln(a.stdout, "ok")
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", os.Getenv("TOLARIX_HEALTHCHECK_URL"), "health URL (default http://127.0.0.1:$TOLARIX_PORT/healthz)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "give up after this long")
	return cmd
}

package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidemaggi/tolarix/core/internal/runner"
	"github.com/davidemaggi/tolarix/core/internal/scheduler"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

func newRunDueCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "run-due",
		Short: "Run the flows whose schedule says they are due, then exit",
		Long: `Run every enabled flow whose scheduled time has come, one after the
other, and exit. Meant to be called often (e.g. every 5 minutes) by cron, a
systemd timer or Task Scheduler when Tolarix is not left running as a server.

A flow is due when a time of its schedule has passed since it last ran by
schedule; times missed while nothing was running count once, without
backfill. Flows with the built-in schedule switched off are run here too;
disabled flows and manual schedules never are. Each due time is claimed in
the database, so the server and several run-due processes never run the same
time twice.

Exit codes: 0 success or nothing due, 1 failure, 3 partial failure,
4 a due flow was already running (its time is skipped).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			rt, done, err := a.openRuntime(ctx)
			if err != nil {
				return err
			}
			defer done()
			list, err := rt.store.ListFlows(ctx)
			if err != nil {
				return err
			}
			now := time.Now()
			var flows []store.Flow
			for _, d := range scheduler.DueFlows(list, now, false) {
				ok, err := rt.store.ClaimSchedule(ctx, d.Flow.ID, d.Flow.ScheduleAnchor, now)
				if err != nil {
					return err
				}
				if ok { // otherwise the server (or another run-due) has it
					flows = append(flows, d.Flow)
				}
			}
			if len(flows) == 0 {
				if asJSON {
					return printJSON(a.stdout, []runOutput{})
				}
				fmt.Fprintln(a.stdout, "Nothing is due.")
				return nil
			}
			return a.runFlows(ctx, rt, flows, runner.TriggerRunDue, false, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the run results as JSON")
	return cmd
}

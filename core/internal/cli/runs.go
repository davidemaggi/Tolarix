package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

func newRunsCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "runs",
		Aliases: []string{"run-history"},
		Short:   "Show run history and reports",
	}
	cmd.AddCommand(newRunsListCommand(a), newRunsShowCommand(a))
	return cmd
}

func newRunsListCommand(a *app) *cobra.Command {
	var asJSON bool
	var flowName string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt, done, err := a.openRuntime(ctx)
			if err != nil {
				return err
			}
			defer done()
			flowID := ""
			if flowName != "" {
				f, err := rt.store.GetFlow(ctx, flowName)
				if err != nil {
					return notFoundErr("flow", flowName, err)
				}
				flowID = f.ID
			}
			runs, err := rt.store.ListRuns(ctx, flowID, limit)
			if err != nil {
				return err
			}
			if asJSON {
				if runs == nil {
					runs = []store.Run{}
				}
				return printJSON(a.stdout, runs)
			}
			if len(runs) == 0 {
				fmt.Fprintln(a.stdout, "No runs yet.")
				return nil
			}
			tw := table(a.stdout)
			fmt.Fprintln(tw, "RUN\tFLOW\tSTARTED\tDURATION\tSTATUS\tTRIGGER\tCHANGES")
			for _, r := range runs {
				var c engine.Counts
				_ = json.Unmarshal(r.Counts, &c)
				status := r.Status
				if r.DryRun {
					status += " (dry-run)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", shortID(r.ID), r.FlowName,
					r.StartedAt.Local().Format("2006-01-02 15:04:05"), duration(r), status, r.Trigger, c.Changes())
			}
			return tw.Flush()
		},
	}
	f := cmd.Flags()
	f.BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	f.StringVar(&flowName, "flow", "", "only runs of this flow")
	f.IntVar(&limit, "limit", 20, "how many runs to show")
	return cmd
}

func duration(r store.Run) string {
	if r.FinishedAt == nil {
		return "running"
	}
	return r.FinishedAt.Sub(r.StartedAt).Round(100 * time.Millisecond).String()
}

func newRunsShowCommand(a *app) *cobra.Command {
	var asJSON, logs bool
	cmd := &cobra.Command{
		Use:   "show <run-id>",
		Short: "Show a run's report: changes, warnings and errors",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rt, done, err := a.openRuntime(ctx)
			if err != nil {
				return err
			}
			defer done()
			r, err := rt.store.GetRun(ctx, args[0])
			if err != nil {
				return notFoundErr("run", args[0], err)
			}
			var events []store.RunEvent
			if logs || asJSON {
				if events, err = rt.store.RunEvents(ctx, r.ID, 0, 0); err != nil {
					return err
				}
			}
			if asJSON {
				return printJSON(a.stdout, struct {
					store.Run
					Events []store.RunEvent `json:"events"`
				}{r, events})
			}
			var rep engine.Report
			_ = json.Unmarshal(r.Report, &rep)
			w := a.stdout
			fmt.Fprintf(w, "Run %s of flow %q\n", r.ID, r.FlowName)
			fmt.Fprintf(w, "  Status:   %s", r.Status)
			if r.DryRun {
				fmt.Fprint(w, " (dry-run: nothing was written)")
			}
			fmt.Fprintln(w)
			fmt.Fprintf(w, "  Started:  %s (%s, trigger: %s)\n", r.StartedAt.Local().Format(time.DateTime), duration(r), r.Trigger)
			if r.Error != "" {
				fmt.Fprintf(w, "  Error:    %s\n", r.Error)
			}
			fmt.Fprintf(w, "  Pages in scope: %d\n", rep.Pages)
			for _, wn := range rep.Warnings {
				fmt.Fprintf(w, "  warning (Master): %s: %s\n", wn.PageID, wn.Message)
			}
			for _, t := range rep.Targets {
				fmt.Fprintf(w, "\nMirror %q\n", t.Name)
				if t.Error != "" {
					fmt.Fprintf(w, "  FAILED: %s\n", t.Error)
					continue
				}
				c := t.Counts
				fmt.Fprintf(w, "  %d created, %d updated, %d moved, %d deleted, %d archived, %d skipped, %d failed\n",
					c.Created, c.Updated, c.Moved, c.Deleted, c.Archived, c.Skipped(), c.Failed)
				for _, op := range t.Ops {
					if op.Kind == engine.OpUnchanged {
						continue
					}
					reason := ""
					if op.Reason != "" {
						reason = " — " + op.Reason
					}
					fmt.Fprintf(w, "  %-9s %s%s%s\n", op.Kind, describeOp(op), reason, errSuffix(op.Error))
				}
				for _, at := range t.Attachments {
					fmt.Fprintf(w, "  %-9s attachment %s%s\n", at.Kind, at.Key, errSuffix(at.Error))
				}
				for _, wn := range t.Warnings {
					fmt.Fprintf(w, "  warning: %s: %s\n", wn.PageID, wn.Message)
				}
			}
			if logs {
				fmt.Fprintln(w, "\nLog")
				for _, e := range events {
					fmt.Fprintf(w, "  %s %-5s %s %s\n", e.Time.Local().Format("15:04:05.000"), e.Level, e.Msg, string(e.Attrs))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON (includes the log)")
	cmd.Flags().BoolVar(&logs, "log", false, "also print the run's log lines")
	return cmd
}

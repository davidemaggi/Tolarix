package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/runner"
	"github.com/davidemaggi/tolarix/core/internal/schedule"
	"github.com/davidemaggi/tolarix/core/internal/scheduler"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

func newFlowCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "flow",
		Aliases: []string{"flows"},
		Short:   "List, run and import sync flows",
	}
	cmd.AddCommand(newFlowListCommand(a), newFlowRunCommand(a), newFlowImportCommand(a))
	return cmd
}

type flowView struct {
	store.Flow
	Master       string     `json:"master"`
	Mirrors      []string   `json:"mirrors"`
	LastRun      *store.Run `json:"lastRun,omitempty"`
	ScheduleText string     `json:"scheduleText"`
	NextRunAt    *time.Time `json:"nextRunAt,omitempty"`
}

func newFlowListCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List flows with their last run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt, done, err := a.openRuntime(ctx)
			if err != nil {
				return err
			}
			defer done()
			flows, err := rt.store.ListFlows(ctx)
			if err != nil {
				return err
			}
			names := map[string]string{}
			conns, _ := rt.store.ListConnections(ctx)
			for _, c := range conns {
				names[c.ID] = c.Name
			}
			views := make([]flowView, 0, len(flows))
			for _, f := range flows {
				v := flowView{Flow: f, Master: names[f.SourceConnectionID], Mirrors: []string{}}
				for _, t := range f.Targets {
					v.Mirrors = append(v.Mirrors, names[t.ConnectionID])
				}
				if runs, _ := rt.store.ListRuns(ctx, f.ID, 1); len(runs) == 1 {
					v.LastRun = &runs[0]
				}
				if sc, err := schedule.Parse(f.Schedule); err == nil {
					v.ScheduleText = sc.Describe(schedule.English, f.Timezone)
					if !sc.IsManual() && !f.ScheduleEnabled {
						v.ScheduleText += " (run-due only)"
					}
				}
				if next, ok := scheduler.NextRun(f); ok {
					v.NextRunAt = &next
				}
				views = append(views, v)
			}
			if asJSON {
				return printJSON(a.stdout, views)
			}
			if len(views) == 0 {
				fmt.Fprintln(a.stdout, "No flows yet. Create one in the web interface or with: tolarix flow import <file.yaml>")
				return nil
			}
			tw := table(a.stdout)
			fmt.Fprintln(tw, "NAME\tENABLED\tMASTER\tMIRRORS\tSCHEDULE\tNEXT RUN\tLAST RUN\tSTATUS\tID")
			for _, v := range views {
				last, status, next := "never", "-", "-"
				if v.NextRunAt != nil {
					next = v.NextRunAt.Local().Format("2006-01-02 15:04")
				}
				if v.LastRun != nil {
					last = v.LastRun.StartedAt.Local().Format("2006-01-02 15:04")
					status = v.LastRun.Status
					if v.LastRun.DryRun {
						status += " (dry-run)"
					}
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.Name, yesNo(v.Enabled), v.Master,
					strings.Join(v.Mirrors, ", "), v.ScheduleText, next, last, status, shortID(v.ID))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}

func newFlowRunCommand(a *app) *cobra.Command {
	var dryRun, all, asJSON bool
	cmd := &cobra.Command{
		Use:   "run [flow-name-or-id]",
		Short: "Run a flow now (or every enabled flow with --all)",
		Long: `Run a flow now and wait for it to finish.

Exit codes: 0 success, 1 failure, 3 partial failure (some pages failed),
4 the flow is already running elsewhere, 5 flow not found.
With --all the highest-priority code wins (4 > 1 > 3 > 0).`,
		Args: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return &ExitError{Code: ExitUsage, Err: errors.New("give a flow name or --all, not both")}
			}
			if !all && len(args) != 1 {
				return &ExitError{Code: ExitUsage, Err: errors.New("give the flow name or id, or --all")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			rt, done, err := a.openRuntime(ctx)
			if err != nil {
				return err
			}
			defer done()

			var flows []store.Flow
			if all {
				list, err := rt.store.ListFlows(ctx)
				if err != nil {
					return err
				}
				for _, f := range list {
					if f.Enabled {
						flows = append(flows, f)
					}
				}
				if len(flows) == 0 {
					fmt.Fprintln(a.stderr, "No enabled flows.")
				}
			} else {
				f, err := rt.store.GetFlow(ctx, args[0])
				if err != nil {
					return notFoundErr("flow", args[0], err)
				}
				flows = []store.Flow{f}
			}
			return a.runFlows(ctx, rt, flows, runner.TriggerCLI, dryRun, asJSON)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "show what would change without writing anything")
	f.BoolVar(&all, "all", false, "run every enabled flow, one after the other")
	f.BoolVar(&asJSON, "json", false, "print the run results as JSON")
	return cmd
}

type runOutput struct {
	Flow   string         `json:"flow"`
	Run    *store.Run     `json:"run,omitempty"`
	Report *engine.Report `json:"report,omitempty"`
	Error  string         `json:"error,omitempty"`
	Code   int            `json:"exitCode"`
}

// runFlows runs flows sequentially and maps outcomes to an exit code.
func (a *app) runFlows(ctx context.Context, rt *runtime, flows []store.Flow, trigger string, dryRun, asJSON bool) error {
	var outs []runOutput
	worst := ExitOK
	for _, f := range flows {
		res, err := rt.runner.RunFlow(ctx, f, trigger, dryRun)
		out := runOutput{Flow: f.Name}
		switch {
		case errors.Is(err, runner.ErrLocked):
			out.Error, out.Code = err.Error(), ExitLocked
		case err != nil:
			out.Error, out.Code = err.Error(), ExitFailure
		default:
			r := res.Run
			out.Run, out.Report = &r, res.Report
			out.Code = exitCodeFor(r.Status)
			if r.Error != "" {
				out.Error = r.Error
			}
		}
		worst = worseExit(worst, out.Code)
		outs = append(outs, out)
		if !asJSON {
			printRunSummary(a, out)
		}
		if ctx.Err() != nil {
			break
		}
	}
	if asJSON {
		if err := printJSON(a.stdout, outs); err != nil {
			return err
		}
	}
	if worst != ExitOK {
		return &ExitError{Code: worst, Err: errors.New(summaryError(outs))}
	}
	return nil
}

func exitCodeFor(status string) int {
	switch status {
	case store.RunSucceeded:
		return ExitOK
	case store.RunPartial:
		return ExitPartial
	default:
		return ExitFailure
	}
}

// worseExit orders codes by severity: locked > failure > partial > ok.
func worseExit(a, b int) int {
	rank := map[int]int{ExitOK: 0, ExitPartial: 1, ExitFailure: 2, ExitLocked: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func summaryError(outs []runOutput) string {
	var bad []string
	for _, o := range outs {
		if o.Code != ExitOK {
			status := "failed"
			if o.Run != nil {
				status = o.Run.Status
			} else if o.Code == ExitLocked {
				status = "already running"
			}
			bad = append(bad, fmt.Sprintf("%s: %s", o.Flow, status))
		}
	}
	return "not every flow succeeded (" + strings.Join(bad, "; ") + ")"
}

func printRunSummary(a *app, o runOutput) {
	w := a.stdout
	if o.Run == nil {
		fmt.Fprintf(w, "%s: %s\n", o.Flow, o.Error)
		return
	}
	mode := ""
	if o.Run.DryRun {
		mode = " (dry-run: nothing was written)"
	}
	fmt.Fprintf(w, "%s: %s%s, run %s\n", o.Flow, o.Run.Status, mode, shortID(o.Run.ID))
	if o.Report == nil {
		return
	}
	verb := map[bool]string{true: "would be ", false: ""}[o.Run.DryRun]
	for _, t := range o.Report.Targets {
		if t.Error != "" {
			fmt.Fprintf(w, "  %s: FAILED: %s\n", t.Name, t.Error)
			continue
		}
		c := t.Counts
		fmt.Fprintf(w, "  %s: %s%d created, %d updated, %d moved, %d deleted, %d archived, %d skipped, %d failed",
			t.Name, verb, c.Created, c.Updated, c.Moved, c.Deleted, c.Archived, c.Skipped(), c.Failed)
		if c.AttachmentsUploaded+c.AttachmentsDeleted > 0 {
			fmt.Fprintf(w, "; attachments: %d uploaded, %d deleted", c.AttachmentsUploaded, c.AttachmentsDeleted)
		}
		fmt.Fprintln(w)
		if o.Run.DryRun {
			for _, op := range t.Ops {
				if op.Kind == engine.OpUnchanged {
					continue
				}
				fmt.Fprintf(w, "    %-9s %s%s\n", op.Kind, describeOp(op), errSuffix(op.Error))
			}
		} else {
			for _, op := range t.Ops {
				if op.Error != "" {
					fmt.Fprintf(w, "    FAILED %s %s: %s\n", op.Kind, describeOp(op), op.Error)
				}
			}
		}
		if n := len(t.Warnings); n > 0 {
			fmt.Fprintf(w, "    %s (see: tolarix runs show %s)\n", plural(n, "warning"), shortID(o.Run.ID))
		}
	}
}

func describeOp(op engine.Op) string {
	switch {
	case op.From != "" && op.To != "" && op.From != op.To:
		return op.From + " -> " + op.To
	case op.To != "":
		return op.To
	default:
		return op.From
	}
}

func errSuffix(e string) string {
	if e == "" {
		return ""
	}
	return " (" + e + ")"
}

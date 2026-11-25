// Package runner executes a stored flow: it takes the flow's run lock,
// builds the connectors, runs the engine, streams log lines into the run's
// events and stores the final report. The CLI, the API and the scheduler all
// go through here, so they share the same locking and reporting.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/render"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

// Triggers recorded on runs.
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
	TriggerCLI      = "cli"
	TriggerRunDue   = "run-due"
)

// ErrLocked is returned when the flow is already running.
var ErrLocked = store.ErrLocked

// Runner runs flows.
type Runner struct {
	Store     *store.Store
	Cipher    *secrets.Cipher
	Log       *slog.Logger
	Renderers []render.Renderer
	// Holder identifies this process in the lock table (default host/pid).
	Holder string
	// StaleAfter: a lock without heartbeat for this long is abandoned (default 2m).
	StaleAfter time.Duration
	// HeartbeatEvery refreshes the lock (default 30s).
	HeartbeatEvery time.Duration
}

// Result is the outcome of RunFlow.
type Result struct {
	Run    store.Run
	Report *engine.Report
}

// LockedError says who holds the lock.
type LockedError struct {
	Flow string
	Info *store.LockInfo
}

func (e *LockedError) Error() string {
	if e.Info == nil {
		return fmt.Sprintf("flow %q is already running", e.Flow)
	}
	return fmt.Sprintf("flow %q is already running (run %s, started %s by %s)",
		e.Flow, e.Info.RunID, e.Info.AcquiredAt.Local().Format(time.DateTime), e.Info.Holder)
}

func (e *LockedError) Unwrap() error { return ErrLocked }

// RunFlow runs one flow now. A non-nil error means the run could not start
// (lock held, database error); sync failures are reported in Result.
func (r *Runner) RunFlow(ctx context.Context, flow store.Flow, trigger string, dryRun bool) (*Result, error) {
	log := r.logger().With("flow", flow.Name)
	runID := store.NewRunID()
	if info, err := r.Store.AcquireLock(ctx, flow.ID, runID, r.holder(), r.staleAfter()); err != nil {
		if errors.Is(err, store.ErrLocked) {
			return nil, &LockedError{Flow: flow.Name, Info: info}
		}
		return nil, fmt.Errorf("take the run lock: %w", err)
	}
	// Release with a fresh context: the run's may be cancelled by now.
	defer func() {
		if err := r.Store.ReleaseLock(context.Background(), flow.ID, runID); err != nil {
			log.Error("could not release the run lock", "error", err)
		}
	}()

	run, err := r.Store.CreateRun(ctx, runID, flow.ID, trigger, dryRun)
	if err != nil {
		return nil, fmt.Errorf("record the run: %w", err)
	}
	log = log.With("run_id", run.ID)
	ev := &eventLogger{store: r.Store, runID: run.ID, log: log}

	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go r.heartbeat(hbCtx, flow.ID, runID, log)

	mode := "sync"
	if dryRun {
		mode = "dry-run"
	}
	ev.Log(ctx, slog.LevelInfo, "run started", "mode", mode, "trigger", trigger)
	report, runErr := r.execute(ctx, flow, dryRun, ev)

	var status, msg string
	switch {
	case runErr != nil && errors.Is(runErr, context.Canceled):
		status, msg = store.RunCancelled, "cancelled"
	case runErr != nil:
		status, msg = store.RunFailed, runErr.Error()
	default:
		status = report.Status()
		if status == store.RunFailed {
			msg = "no mirror could be updated; see the errors in the report"
		}
	}
	if report == nil {
		report = &engine.Report{DryRun: dryRun}
	}
	lvl := slog.LevelInfo
	if status != store.RunSucceeded {
		lvl = slog.LevelError
	}
	ev.Log(ctx, lvl, "run finished", "status", status,
		"created", report.Counts.Created, "updated", report.Counts.Updated, "moved", report.Counts.Moved,
		"deleted", report.Counts.Deleted, "archived", report.Counts.Archived, "skipped", report.Counts.Skipped(),
		"failed", report.Counts.Failed)

	if err := r.Store.FinishRun(context.Background(), run.ID, status, report.Counts, report, msg); err != nil {
		return nil, fmt.Errorf("store the run report: %w", err)
	}
	final, err := r.Store.GetRun(context.Background(), run.ID)
	if err != nil {
		return nil, err
	}
	return &Result{Run: final, Report: report}, nil
}

func (r *Runner) execute(ctx context.Context, flow store.Flow, dryRun bool, ev *eventLogger) (*engine.Report, error) {
	srcConn, err := r.Store.GetConnection(ctx, flow.SourceConnectionID)
	if err != nil {
		return nil, fmt.Errorf("the Master connection of this flow is missing: %w", err)
	}
	c, err := r.build(ctx, srcConn)
	if err != nil {
		return nil, fmt.Errorf("the Master %q: %w", srcConn.Name, err)
	}
	src, ok := c.(connectors.Source)
	if !ok {
		return nil, fmt.Errorf("the Master %q: this wiki type cannot be used as a Master", srcConn.Name)
	}

	var targets []engine.Target
	var openFailures []engine.TargetReport
	for _, ft := range flow.Targets {
		conn, err := r.Store.GetConnection(ctx, ft.ConnectionID)
		name := ft.ConnectionID
		if err == nil {
			name = conn.Name
		}
		sess, err := r.openTarget(ctx, conn, ft, err)
		if err != nil {
			ev.Log(ctx, slog.LevelError, "mirror unavailable", "target", name, "error", err)
			openFailures = append(openFailures, engine.TargetReport{TargetID: ft.ID, Name: name, Error: err.Error()})
			continue
		}
		targets = append(targets, engine.Target{ID: ft.ID, Name: name, Session: sess})
	}

	eng := &engine.Engine{Mappings: r.Store, Log: ev, Renderers: r.Renderers}
	rep, err := eng.Run(ctx, engine.Request{
		Source: src, SourceName: srcConn.Name, Scope: flow.Scope, Options: flow.Options,
		Targets: targets, DryRun: dryRun,
	})
	if rep != nil {
		rep.Targets = append(rep.Targets, openFailures...)
	}
	return rep, err
}

func (r *Runner) openTarget(ctx context.Context, conn store.Connection, ft store.FlowTarget, lookupErr error) (connectors.Session, error) {
	if lookupErr != nil {
		return nil, fmt.Errorf("the mirror connection is missing: %w", lookupErr)
	}
	c, err := r.build(ctx, conn)
	if err != nil {
		return nil, err
	}
	t, ok := c.(connectors.Target)
	if !ok {
		return nil, errors.New("this wiki type cannot be used as a Mirror")
	}
	return t.Open(ctx, ft.Location)
}

// Build constructs the connector for a stored connection (secret decrypted).
func (r *Runner) Build(ctx context.Context, conn store.Connection) (connectors.Connector, error) {
	return r.build(ctx, conn)
}

func (r *Runner) build(ctx context.Context, conn store.Connection) (connectors.Connector, error) {
	sec, err := r.Store.ConnectionSecret(ctx, conn.ID, r.Cipher)
	if err != nil {
		return nil, fmt.Errorf("read the saved secret: %w", err)
	}
	return connectors.Build(connectors.Kind(conn.Kind), conn.Config, sec)
}

func (r *Runner) heartbeat(ctx context.Context, flowID, runID string, log *slog.Logger) {
	t := time.NewTicker(r.heartbeatEvery())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Store.HeartbeatLock(ctx, flowID, runID); err != nil && ctx.Err() == nil {
				log.Error("run lock heartbeat failed", "error", err)
			}
		}
	}
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Runner) holder() string {
	if r.Holder != "" {
		return r.Holder
	}
	h, _ := os.Hostname()
	return h + "/" + strconv.Itoa(os.Getpid())
}

func (r *Runner) staleAfter() time.Duration {
	if r.StaleAfter > 0 {
		return r.StaleAfter
	}
	return 2 * time.Minute
}

func (r *Runner) heartbeatEvery() time.Duration {
	if r.HeartbeatEvery > 0 {
		return r.HeartbeatEvery
	}
	return 30 * time.Second
}

// eventLogger writes engine log lines to the run's events and to slog.
type eventLogger struct {
	store *store.Store
	runID string
	log   *slog.Logger
}

func (l *eventLogger) Log(ctx context.Context, level slog.Level, msg string, attrs ...any) {
	l.log.Log(ctx, level, msg, attrs...)
	m := map[string]any{}
	for i := 0; i+1 < len(attrs); i += 2 {
		if k, ok := attrs[i].(string); ok {
			m[k] = fmt.Sprint(attrs[i+1])
		}
	}
	// Use a fresh context so the final lines of a cancelled run are kept.
	if _, err := l.store.AddRunEvent(context.Background(), l.runID, level.String(), msg, m); err != nil {
		l.log.Error("could not store a run log line", "error", err)
	}
}

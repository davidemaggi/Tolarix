// Package scheduler decides when flows are due and runs them: the built-in
// scheduler of "tolarix serve", and the helpers "tolarix run-due" and the
// API share.
//
// A flow's next run is the first schedule time after its anchor (see
// store.Flow.Anchor). When that time has passed the flow is due; whoever
// runs it first claims it by moving the anchor to the current time, in one
// conditional UPDATE, so the server and any number of "run-due" processes
// never start the same slot twice. Missed slots (server down) collapse into
// one run, without backfill.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/schedule"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

// Defaults of the built-in scheduler.
const (
	DefaultInterval = 20 * time.Second
	DefaultCap      = 2
)

// NextRun returns when the flow runs next by itself, and false when it
// never does (disabled flow, manual or invalid schedule). It ignores the
// built-in switch: "run-due" still runs flows with the built-in schedule off.
func NextRun(f store.Flow) (time.Time, bool) {
	if !f.Enabled {
		return time.Time{}, false
	}
	s, err := schedule.Parse(f.Schedule)
	if err != nil || s.IsManual() {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(f.Timezone)
	if err != nil {
		return time.Time{}, false
	}
	next := s.Next(f.Anchor(), loc, 1)
	if len(next) == 0 {
		return time.Time{}, false
	}
	return next[0], true
}

// Due is a flow whose scheduled run time has come.
type Due struct {
	Flow store.Flow
	At   time.Time // the schedule time that came (the oldest missed one)
}

// DueFlows lists the flows due at now, oldest first. builtIn limits it to
// flows with the built-in schedule on.
func DueFlows(flows []store.Flow, now time.Time, builtIn bool) []Due {
	var out []Due
	for _, f := range flows {
		if builtIn && !f.ScheduleEnabled {
			continue
		}
		if at, ok := NextRun(f); ok && !at.After(now) {
			out = append(out, Due{Flow: f, At: at})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Starter starts a scheduled run in the background and returns a channel
// closed when it ends. It returns an error wrapping store.ErrLocked when
// the flow is already running.
type Starter func(f store.Flow) (<-chan struct{}, error)

// Scheduler is the built-in scheduler. Fields must be set before Run.
type Scheduler struct {
	Store *store.Store
	Start Starter
	// Active counts the runs in progress in this process (manual ones
	// included); no scheduled run starts while it is at Cap.
	Active   func() int
	Log      *slog.Logger
	Now      func() time.Time // default time.Now
	Interval time.Duration    // between checks; default DefaultInterval
	Cap      int              // default DefaultCap

	once sync.Once
	wake chan struct{}
}

func (s *Scheduler) init() {
	s.once.Do(func() {
		s.wake = make(chan struct{}, 1)
		if s.Now == nil {
			s.Now = time.Now
		}
		if s.Interval <= 0 {
			s.Interval = DefaultInterval
		}
		if s.Cap <= 0 {
			s.Cap = DefaultCap
		}
		if s.Log == nil {
			s.Log = slog.New(slog.DiscardHandler)
		}
		if s.Active == nil {
			s.Active = func() int { return 0 }
		}
	})
}

// Wake asks for a check now (a flow was saved, a run ended). It never blocks.
func (s *Scheduler) Wake() {
	s.init()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run checks for due flows at start (so schedules missed while the server
// was down run once) and then every Interval or on Wake, until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	s.init()
	s.Log.Info("built-in scheduler started", "check_every", s.Interval.String(), "max_parallel", s.Cap)
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

// Tick starts the flows due now, oldest first, while fewer than Cap runs
// are active. Flows left over stay due and start on a later tick.
func (s *Scheduler) Tick(ctx context.Context) {
	s.init()
	flows, err := s.Store.ListFlows(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.Log.Error("scheduler: cannot read flows", "error", err)
		}
		return
	}
	now := s.Now()
	for _, d := range DueFlows(flows, now, true) {
		if ctx.Err() != nil || s.Active() >= s.Cap {
			return
		}
		ok, err := s.Store.ClaimSchedule(ctx, d.Flow.ID, d.Flow.ScheduleAnchor, now)
		if err != nil {
			s.Log.Error("scheduler: cannot claim the scheduled run", "flow", d.Flow.Name, "error", err)
			continue
		}
		if !ok {
			continue // started by "tolarix run-due", or the flow just changed
		}
		done, err := s.Start(d.Flow)
		switch {
		case errors.Is(err, store.ErrLocked):
			// One run per flow: this slot is skipped, the next one counts.
			s.Log.Info("scheduled run skipped: the flow is already running", "flow", d.Flow.Name, "due", d.At)
		case err != nil:
			s.Log.Error("scheduled run did not start", "flow", d.Flow.Name, "error", err)
		default:
			s.Log.Info("scheduled run started", "flow", d.Flow.Name, "due", d.At)
			go func() {
				<-done
				s.Wake() // a slot is free: start what waited for it
			}()
		}
	}
}

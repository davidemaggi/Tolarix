package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/store"
)

type fakeRuns struct {
	mu      sync.Mutex
	started []string
	running map[string]chan struct{}
	locked  map[string]bool
}

func (r *fakeRuns) start(f store.Flow) (<-chan struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locked[f.Name] {
		return nil, fmt.Errorf("run %s: %w", f.Name, store.ErrLocked)
	}
	r.started = append(r.started, f.Name)
	ch := make(chan struct{})
	r.running[f.Name] = ch
	return ch, nil
}

func (r *fakeRuns) active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

func (r *fakeRuns) finish(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	close(r.running[name])
	delete(r.running, name)
}

// take returns and clears the names started so far, sorted.
func (r *fakeRuns) take() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.started...)
	r.started = nil
	sort.Strings(out)
	return strings.Join(out, ",")
}

type fixture struct {
	st   *store.Store
	runs *fakeRuns
	s    *Scheduler
	mu   sync.Mutex
	now  time.Time
	src  string
	dst  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "tolarix.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	src, _ := st.SaveConnection(ctx, store.Connection{Name: "src", Kind: "obsidian"}, nil, nil)
	dst, _ := st.SaveConnection(ctx, store.Connection{Name: "dst", Kind: "obsidian"}, nil, nil)
	fx := &fixture{st: st, runs: &fakeRuns{running: map[string]chan struct{}{}, locked: map[string]bool{}},
		src: src.ID, dst: dst.ID, now: time.Now().Add(time.Hour)}
	fx.s = &Scheduler{Store: st, Start: fx.runs.start, Active: fx.runs.active, Now: fx.clock}
	return fx
}

func (fx *fixture) flow(t *testing.T, name, sched string, enabled, builtIn bool) store.Flow {
	t.Helper()
	f, err := fx.st.SaveFlow(context.Background(), store.Flow{
		Name: name, Enabled: enabled, ScheduleEnabled: builtIn, SourceConnectionID: fx.src,
		Schedule: json.RawMessage(sched), Timezone: "Europe/Rome",
		Targets: []store.FlowTarget{{ConnectionID: fx.dst, Location: json.RawMessage(`{}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (fx *fixture) clock() time.Time {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return fx.now
}

func (fx *fixture) advance(d time.Duration) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.now = fx.now.Add(d)
}

const every5 = `{"kind":"interval","every":5,"unit":"minutes"}`

func TestTickRunsDueFlowsOnceWithCap(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	fx.flow(t, "A", every5, true, true)
	fx.flow(t, "B", every5, true, true)
	fx.flow(t, "C", every5, true, true)
	fx.flow(t, "Manual", `{"kind":"manual"}`, true, true)
	fx.flow(t, "Disabled", every5, false, true)
	fx.flow(t, "BuiltInOff", every5, true, false)

	// An hour of missed slots: each due flow starts once, at most 2 at a time.
	fx.s.Tick(ctx)
	first := fx.runs.take()
	if len(strings.Split(first, ",")) != 2 || strings.Contains(first, "Manual") {
		t.Fatalf("first tick started %q", first)
	}
	fx.s.Tick(ctx)
	if got := fx.runs.take(); got != "" {
		t.Fatalf("at the cap nothing starts, got %q", got)
	}
	// A run ends: the flow that waited starts, and only it.
	fx.runs.finish(strings.Split(first, ",")[0])
	fx.s.Tick(ctx)
	second := fx.runs.take()
	if len(strings.Split(second, ",")) != 1 || strings.Contains(first, second) {
		t.Fatalf("second tick started %q after %q", second, first)
	}
	// No backfill: the anchors moved to now, nothing is due any more.
	for name := range fx.runs.running {
		fx.runs.finish(name)
	}
	fx.s.Tick(ctx)
	if got := fx.runs.take(); got != "" {
		t.Fatalf("missed slots ran again: %q", got)
	}
	// Five minutes later they are due again.
	fx.advance(5 * time.Minute)
	fx.s.Tick(ctx)
	if got := fx.runs.take(); len(strings.Split(got, ",")) != 2 {
		t.Fatalf("next slot started %q", got)
	}
}

func TestTickSkipsLockedAndClaimedFlows(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	fx.flow(t, "Busy", every5, true, true)
	other := fx.flow(t, "Other", every5, true, true)

	// "tolarix run-due" claimed Other first.
	if ok, _ := fx.st.ClaimSchedule(ctx, other.ID, other.ScheduleAnchor, fx.clock()); !ok {
		t.Fatal("claim")
	}
	// Busy is running elsewhere: its slot is consumed, not retried.
	fx.runs.locked["Busy"] = true
	fx.s.Tick(ctx)
	if got := fx.runs.take(); got != "" {
		t.Fatalf("started %q", got)
	}
	fx.runs.locked["Busy"] = false
	fx.s.Tick(ctx)
	if got := fx.runs.take(); got != "" {
		t.Fatalf("a skipped slot must not be retried, started %q", got)
	}
}

func TestDueFlowsAndNextRun(t *testing.T) {
	fx := newFixture(t)
	daily := fx.flow(t, "Daily", `{"kind":"daily","time":"07:30"}`, true, false)
	manual := fx.flow(t, "Manual", `{"kind":"manual"}`, true, true)
	off := fx.flow(t, "Off", every5, false, true)

	next, ok := NextRun(daily)
	rome, _ := time.LoadLocation("Europe/Rome")
	if !ok || next.In(rome).Format("15:04") != "07:30" || !next.After(daily.Anchor()) {
		t.Fatalf("next = %v %v", next, ok)
	}
	if _, ok := NextRun(manual); ok {
		t.Fatal("manual has no next run")
	}
	if _, ok := NextRun(off); ok {
		t.Fatal("a disabled flow has no next run")
	}
	later := next.Add(time.Second)
	flows := []store.Flow{daily, manual, off}
	if d := DueFlows(flows, later, false); len(d) != 1 || d[0].Flow.Name != "Daily" || !d[0].At.Equal(next) {
		t.Fatalf("run-due = %+v", d)
	}
	if d := DueFlows(flows, later, true); len(d) != 0 {
		t.Fatalf("built-in must skip flows with the switch off: %+v", d)
	}
	if d := DueFlows(flows, next.Add(-time.Second), false); len(d) != 0 {
		t.Fatalf("not due yet: %+v", d)
	}
}

func TestRunChecksAtStartAndOnWake(t *testing.T) {
	fx := newFixture(t)
	fx.s.Interval = time.Hour
	fx.flow(t, "A", every5, true, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { fx.s.Run(ctx); close(done) }()

	waitFor(t, func() bool { return fx.runs.active() == 1 }) // missed on startup: runs once
	fx.runs.finish("A")
	fx.flow(t, "B", every5, true, true)
	fx.advance(time.Hour)
	fx.s.Wake()
	waitFor(t, func() bool { return fx.runs.active() == 2 })
	cancel()
	<-done
	if got := fx.runs.take(); got != "A,A,B" {
		t.Fatalf("started %q", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

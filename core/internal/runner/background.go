package runner

import (
	"context"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/store"
)

// Background runs flows detached from the caller (an HTTP request, later
// the scheduler). Runs can be cancelled by id, and Shutdown cancels and
// waits for all of them so their final status is stored.
type Background struct {
	runner *Runner
	base   context.Context
	stop   context.CancelFunc

	mu     sync.Mutex
	active map[string]context.CancelFunc
	wg     sync.WaitGroup
}

// NewBackground returns a manager whose runs live until Shutdown.
func NewBackground(r *Runner) *Background {
	base, stop := context.WithCancel(context.Background())
	return &Background{runner: r, base: base, stop: stop, active: map[string]context.CancelFunc{}}
}

// Start begins a run and returns it as soon as it is recorded.
func (b *Background) Start(flow store.Flow, trigger string, dryRun bool) (store.Run, error) {
	ctx, cancel := context.WithCancel(b.base)
	h, err := b.runner.Start(ctx, flow, trigger, dryRun)
	if err != nil {
		cancel()
		return store.Run{}, err
	}
	b.mu.Lock()
	b.active[h.Run.ID] = cancel
	b.wg.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.wg.Done()
		<-h.Done()
		b.mu.Lock()
		delete(b.active, h.Run.ID)
		b.mu.Unlock()
		cancel()
	}()
	return h.Run, nil
}

// Cancel stops a run started by this manager. It returns false when the run
// is not running here (finished, or started by another process).
func (b *Background) Cancel(runID string) bool {
	b.mu.Lock()
	cancel, ok := b.active[runID]
	b.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Running reports whether a run is active in this process.
func (b *Background) Running(runID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.active[runID]
	return ok
}

// Shutdown cancels every run and waits for them (or for ctx).
func (b *Background) Shutdown(ctx context.Context) error {
	b.stop()
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Hub fans out "something changed for this run" signals to live viewers in
// the same process. Viewers re-read the database on each signal, so a
// dropped signal is harmless; they also poll, which covers runs started by
// another process (the CLI).
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[string]map[chan struct{}]struct{}{}} }

// Subscribe returns a channel signalled on changes to runID and a function
// to unsubscribe.
func (h *Hub) Subscribe(runID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[runID] == nil {
		h.subs[runID] = map[chan struct{}]struct{}{}
	}
	h.subs[runID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[runID], ch)
		if len(h.subs[runID]) == 0 {
			delete(h.subs, runID)
		}
		h.mu.Unlock()
	}
}

// Notify signals every subscriber of runID without blocking.
func (h *Hub) Notify(runID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[runID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

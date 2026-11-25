package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/api/oapi"
	"github.com/davidemaggi/tolarix/core/internal/runner"
	"github.com/davidemaggi/tolarix/core/internal/store"
)

const (
	defaultRunPage   = 50
	maxRunPage       = 200
	defaultEventPage = 1000
	maxEventPage     = 5000
	// sseBatch is how many log lines the stream reads per query.
	sseBatch = 500
	// sseKeepAlive keeps proxies from closing an idle stream.
	sseKeepAlive = 15 * time.Second
)

func runOut(r store.Run) oapi.Run {
	out := oapi.Run{
		Id: r.ID, FlowId: r.FlowID, FlowName: r.FlowName, Trigger: oapi.RunTrigger(r.Trigger), DryRun: r.DryRun,
		Status: oapi.RunStatus(r.Status), StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
	if len(r.Counts) > 0 {
		_ = json.Unmarshal(r.Counts, &out.Counts) // zero counts if malformed
	}
	if r.Error != "" {
		out.Error = ptr(r.Error)
	}
	return out
}

func runDetailOut(r store.Run) oapi.RunDetail {
	b := runOut(r)
	out := oapi.RunDetail{
		Id: b.Id, FlowId: b.FlowId, FlowName: b.FlowName, Trigger: b.Trigger, DryRun: b.DryRun,
		Status: b.Status, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt, Counts: b.Counts, Error: b.Error,
		Report: oapi.Report{DryRun: r.DryRun, Counts: b.Counts},
	}
	if len(r.Report) > 0 && string(r.Report) != "null" {
		_ = json.Unmarshal(r.Report, &out.Report)
	}
	// The contract says these arrays are always present.
	if out.Report.Targets == nil {
		out.Report.Targets = []oapi.TargetReport{}
	}
	for i := range out.Report.Targets {
		if out.Report.Targets[i].Ops == nil {
			out.Report.Targets[i].Ops = []oapi.Op{}
		}
	}
	return out
}

func runEventOut(e store.RunEvent) oapi.RunEvent {
	out := oapi.RunEvent{Id: e.ID, RunId: e.RunID, Time: e.Time, Level: eventLevel(e.Level), Msg: e.Msg, Attrs: map[string]string{}}
	var attrs map[string]any
	if json.Unmarshal(e.Attrs, &attrs) == nil {
		for k, v := range attrs {
			if s, ok := v.(string); ok {
				out.Attrs[k] = s
			} else {
				out.Attrs[k] = fmt.Sprint(v)
			}
		}
	}
	return out
}

// eventLevel maps slog level names ("INFO", "WARN+2", …) to the four API levels.
func eventLevel(l string) oapi.RunEventLevel {
	switch {
	case strings.HasPrefix(l, "ERROR"):
		return oapi.RunEventLevelERROR
	case strings.HasPrefix(l, "WARN"):
		return oapi.RunEventLevelWARN
	case strings.HasPrefix(l, "DEBUG"):
		return oapi.RunEventLevelDEBUG
	default:
		return oapi.RunEventLevelINFO
	}
}

func (s *server) StartRun(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	var in oapi.StartRunRequest
	if err := decodeJSON(r, &in); err != nil {
		s.writeErr(w, r, err)
		return
	}
	f, err := s.flow(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	dry := in.DryRun != nil && *in.DryRun
	run, err := s.b.Background.Start(f, runner.TriggerManual, dry)
	var le *runner.LockedError
	if errors.As(err, &le) {
		msg := "This flow is already running."
		if le.Info != nil {
			msg = fmt.Sprintf("This flow is already running (started %s by %s).",
				le.Info.AcquiredAt.UTC().Format(time.RFC3339), le.Info.Holder)
		}
		s.writeErr(w, r, errConflict("running", msg))
		return
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.log.Info("run started from the web interface", "flow", f.Name, "run_id", run.ID, "dry_run", dry)
	writeJSON(w, http.StatusAccepted, runOut(run))
}

func (s *server) ListRuns(w http.ResponseWriter, r *http.Request, params oapi.ListRunsParams) {
	q := store.RunQuery{Limit: defaultRunPage, DryRun: params.DryRun}
	if params.Limit != nil {
		if *params.Limit < 1 || *params.Limit > maxRunPage {
			s.writeErr(w, r, errBadRequest("invalid_parameter", "limit must be between 1 and %d.", maxRunPage))
			return
		}
		q.Limit = *params.Limit
	}
	if params.FlowId != nil {
		q.FlowID = *params.FlowId
	}
	if params.Status != nil {
		if !params.Status.Valid() {
			s.writeErr(w, r, errBadRequest("invalid_parameter", "Unknown status %q.", *params.Status))
			return
		}
		q.Status = string(*params.Status)
	}
	if params.Before != nil {
		q.Before = *params.Before
	}
	want := q.Limit
	q.Limit++ // one extra row tells whether there is a next page
	runs, err := s.b.Store.QueryRuns(r.Context(), q)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := oapi.RunList{Items: make([]oapi.Run, 0, min(len(runs), want))}
	for i, run := range runs {
		if i == want {
			out.NextBefore = ptr(runs[i-1].ID)
			break
		}
		out.Items = append(out.Items, runOut(run))
	}
	writeJSON(w, http.StatusOK, out)
}

// run looks a run up by id.
func (s *server) run(ctx context.Context, id string) (store.Run, error) {
	run, err := s.b.Store.GetRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return run, errNotFound("run")
	}
	return run, err
}

func (s *server) GetRun(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	run, err := s.run(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runDetailOut(run))
}

func (s *server) CancelRun(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	run, err := s.run(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if run.Status != store.RunRunning {
		s.writeErr(w, r, errConflict("not_running", "This run has already finished."))
		return
	}
	if !s.b.Background.Cancel(run.ID) {
		s.writeErr(w, r, errConflict("not_here",
			"This run was started by another process (for example the command line) and can only be stopped there."))
		return
	}
	s.log.Info("run cancellation requested", "run_id", run.ID, "flow", run.FlowName)
	w.WriteHeader(http.StatusAccepted)
}

func (s *server) ListRunEvents(w http.ResponseWriter, r *http.Request, id oapi.Id, params oapi.ListRunEventsParams) {
	run, err := s.run(r.Context(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	limit := defaultEventPage
	if params.Limit != nil {
		if *params.Limit < 1 || *params.Limit > maxEventPage {
			s.writeErr(w, r, errBadRequest("invalid_parameter", "limit must be between 1 and %d.", maxEventPage))
			return
		}
		limit = *params.Limit
	}
	var after int64
	if params.After != nil {
		after = *params.After
	}
	evs, err := s.b.Store.RunEvents(r.Context(), run.ID, after, limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := make([]oapi.RunEvent, 0, len(evs))
	for _, e := range evs {
		out = append(out, runEventOut(e))
	}
	writeJSON(w, http.StatusOK, out)
}

// StreamRun sends the run log as Server-Sent Events. It replays stored lines
// after Last-Event-ID, then follows new ones: in-process runs signal through
// the hub, runs of other processes (the CLI) are picked up by polling.
func (s *server) StreamRun(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	ctx := r.Context()
	run, err := s.run(ctx, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // long-lived response

	var after int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	// Subscribe before the first read so no signal is missed in between.
	signal, unsub := s.b.Hub.Subscribe(run.ID)
	defer unsub()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // nginx: do not buffer
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	if rc.Flush() != nil {
		return
	}

	poll := time.NewTicker(s.b.PollInterval)
	defer poll.Stop()
	keepAlive := time.NewTicker(sseKeepAlive)
	defer keepAlive.Stop()

	// drain writes every stored line after `after`.
	drain := func() error {
		for {
			evs, err := s.b.Store.RunEvents(ctx, run.ID, after, sseBatch)
			if err != nil {
				return err
			}
			for _, e := range evs {
				b, _ := json.Marshal(runEventOut(e))
				if _, err := fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", e.ID, b); err != nil {
					return err
				}
				after = e.ID
			}
			if err := rc.Flush(); err != nil {
				return err
			}
			if len(evs) < sseBatch {
				return nil
			}
		}
	}

	for {
		// Read the status first: lines are stored before the final status,
		// so draining after seeing "finished" cannot miss any.
		cur, err := s.b.Store.GetRun(ctx, run.ID)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Error("live log: read run", "run_id", run.ID, "error", err)
			}
			return
		}
		if err := drain(); err != nil {
			if ctx.Err() == nil {
				s.log.Debug("live log stream ended", "run_id", run.ID, "error", err)
			}
			return
		}
		if cur.Status != store.RunRunning {
			b, _ := json.Marshal(runOut(cur))
			_, _ = fmt.Fprintf(w, "event: end\ndata: %s\n\n", b)
			_ = rc.Flush()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-signal:
		case <-poll.C:
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Run statuses.
const (
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunPartial   = "partial"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
)

// Run is one execution of a flow.
type Run struct {
	ID         string          `json:"id"`
	FlowID     string          `json:"flowId"`
	FlowName   string          `json:"flowName,omitempty"`
	Trigger    string          `json:"trigger"`
	DryRun     bool            `json:"dryRun"`
	Status     string          `json:"status"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Counts     json.RawMessage `json:"counts"`
	Report     json.RawMessage `json:"report,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// RunEvent is one live log line of a run.
type RunEvent struct {
	ID    int64           `json:"id"`
	RunID string          `json:"runId"`
	Time  time.Time       `json:"time"`
	Level string          `json:"level"`
	Msg   string          `json:"msg"`
	Attrs json.RawMessage `json:"attrs"`
}

// NewRunID returns a fresh run id (taken before the lock, so the lock names its run).
func NewRunID() string { return newID() }

// CreateRun records a run as started. id may be empty.
func (s *Store) CreateRun(ctx context.Context, id, flowID, trigger string, dryRun bool) (Run, error) {
	if id == "" {
		id = newID()
	}
	r := Run{ID: id, FlowID: flowID, Trigger: trigger, DryRun: dryRun, Status: RunRunning}
	ts := now()
	r.StartedAt = parseTime(ts)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO runs (id, flow_id, trigger, dry_run, status, started_at) VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, flowID, trigger, boolInt(dryRun), r.Status, ts)
	return r, err
}

// FinishRun stores the final status, counts and report.
func (s *Store) FinishRun(ctx context.Context, id, status string, counts, report any, errMsg string) error {
	var e sql.NullString
	if errMsg != "" {
		e = sql.NullString{String: errMsg, Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET status = ?, finished_at = ?, counts = ?, report = ?, error = ? WHERE id = ?`,
		status, now(), mustJSON(counts), mustJSON(report), e, id)
	return err
}

const runCols = `r.id, r.flow_id, f.name, r.trigger, r.dry_run, r.status, r.started_at, r.finished_at, r.counts, r.error`

func scanRun(row interface{ Scan(...any) error }, withReport bool) (Run, error) {
	var r Run
	var started, counts string
	var finished, errMsg sql.NullString
	var report sql.NullString
	dest := []any{&r.ID, &r.FlowID, &r.FlowName, &r.Trigger, &r.DryRun, &r.Status, &started, &finished, &counts, &errMsg}
	if withReport {
		dest = append(dest, &report)
	}
	if err := row.Scan(dest...); err != nil {
		return r, err
	}
	r.StartedAt, r.FinishedAt = parseTime(started), parseNullTime(finished)
	r.Counts = json.RawMessage(counts)
	r.Error = errMsg.String
	if report.Valid {
		r.Report = json.RawMessage(report.String)
	}
	return r, nil
}

// ListRuns returns the latest runs, optionally for one flow.
func (s *Store) ListRuns(ctx context.Context, flowID string, limit int) ([]Run, error) {
	return s.QueryRuns(ctx, RunQuery{FlowID: flowID, Limit: limit})
}

// RunQuery filters the run history. Zero values mean "any".
type RunQuery struct {
	FlowID string
	Status string
	DryRun *bool
	// Before returns runs started strictly before this run (keyset paging).
	Before string
	Limit  int
}

// QueryRuns returns runs, newest first.
func (s *Store) QueryRuns(ctx context.Context, q RunQuery) ([]Run, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	query := `SELECT ` + runCols + ` FROM runs r JOIN flows f ON f.id = r.flow_id WHERE 1 = 1`
	args := []any{}
	if q.FlowID != "" {
		query += ` AND r.flow_id = ?`
		args = append(args, q.FlowID)
	}
	if q.Status != "" {
		query += ` AND r.status = ?`
		args = append(args, q.Status)
	}
	if q.DryRun != nil {
		query += ` AND r.dry_run = ?`
		args = append(args, boolInt(*q.DryRun))
	}
	if q.Before != "" {
		query += ` AND (r.started_at, r.id) < (SELECT started_at, id FROM runs WHERE id = ?)`
		args = append(args, q.Before)
	}
	query += ` ORDER BY r.started_at DESC, r.id DESC LIMIT ?`
	args = append(args, q.Limit)
	return s.collectRuns(ctx, query, args...)
}

// LatestRuns returns the most recent run of every flow that has one, by flow id.
func (s *Store) LatestRuns(ctx context.Context) (map[string]Run, error) {
	runs, err := s.collectRuns(ctx, `SELECT `+runCols+` FROM runs r JOIN flows f ON f.id = r.flow_id
		WHERE r.id = (SELECT r2.id FROM runs r2 WHERE r2.flow_id = r.flow_id ORDER BY r2.started_at DESC, r2.id DESC LIMIT 1)`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Run, len(runs))
	for _, r := range runs {
		out[r.FlowID] = r
	}
	return out, nil
}

func (s *Store) collectRuns(ctx context.Context, query string, args ...any) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun returns one run with its report. The short id shown by the CLI
// (the last 8+ characters) is accepted, as is a prefix of 8+ characters.
func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	r, err := scanRun(s.DB.QueryRowContext(ctx, `SELECT `+runCols+`, r.report FROM runs r JOIN flows f ON f.id = r.flow_id
		WHERE r.id = ? OR (length(?) >= 8 AND (r.id LIKE ? || '%' OR r.id LIKE '%' || ?)) ORDER BY r.id = ? DESC LIMIT 1`, id, id, id, id, id), true)
	return r, notFound(err)
}

// AddRunEvent appends a log line to a run.
func (s *Store) AddRunEvent(ctx context.Context, runID, level, msg string, attrs map[string]any) (int64, error) {
	a := "{}"
	if len(attrs) > 0 {
		a = mustJSON(attrs)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO run_events (run_id, ts, level, msg, attrs) VALUES (?, ?, ?, ?, ?)`,
		runID, now(), level, msg, a)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RunEvents lists log lines of a run after afterID (0 = from the start).
func (s *Store) RunEvents(ctx context.Context, runID string, afterID int64, limit int) ([]RunEvent, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, run_id, ts, level, msg, attrs FROM run_events
		WHERE run_id = ? AND id > ? ORDER BY id LIMIT ?`, runID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunEvent
	for rows.Next() {
		var e RunEvent
		var ts, attrs string
		if err := rows.Scan(&e.ID, &e.RunID, &ts, &e.Level, &e.Msg, &attrs); err != nil {
			return nil, err
		}
		e.Time, e.Attrs = parseTime(ts), json.RawMessage(attrs)
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkInterruptedRuns sets runs left "running" by a crashed process (no live lock) to failed.
func (s *Store) MarkInterruptedRuns(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE runs SET status = ?, finished_at = ?, error = 'interrupted: the process stopped during the run'
		WHERE status = ? AND id NOT IN (SELECT run_id FROM flow_locks)`, RunFailed, now(), RunRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

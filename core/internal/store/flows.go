package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/engine"
)

// Flow is one Master source + scope mirrored to one or more targets.
type Flow struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Enabled            bool            `json:"enabled"`
	SourceConnectionID string          `json:"sourceConnectionId"`
	Scope              engine.Scope    `json:"scope"`
	Options            engine.Options  `json:"options"`
	Schedule           json.RawMessage `json:"schedule"`
	ScheduleEnabled    bool            `json:"scheduleEnabled"`
	Timezone           string          `json:"timezone"`
	Targets            []FlowTarget    `json:"targets"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`

	// ScheduleAnchor is the stored anchor, compared as is by ClaimSchedule.
	// Use Anchor for its time. Set by the store only.
	ScheduleAnchor string `json:"-"`
}

// Anchor is the time from which the next scheduled run is counted.
func (f Flow) Anchor() time.Time {
	if t := parseTime(f.ScheduleAnchor); !t.IsZero() {
		return t
	}
	return f.UpdatedAt
}

// FlowTarget is one mirror of a flow.
type FlowTarget struct {
	ID           string          `json:"id"`
	ConnectionID string          `json:"connectionId"`
	Location     json.RawMessage `json:"location"`
	Position     int             `json:"position"`
}

const flowCols = `id, name, enabled, source_connection_id, scope, options, schedule, schedule_enabled, timezone, created_at, updated_at, schedule_anchor`

func scanFlow(row interface{ Scan(...any) error }) (Flow, error) {
	var f Flow
	var scope, opts, sched, created, updated string
	if err := row.Scan(&f.ID, &f.Name, &f.Enabled, &f.SourceConnectionID, &scope, &opts, &sched,
		&f.ScheduleEnabled, &f.Timezone, &created, &updated, &f.ScheduleAnchor); err != nil {
		return f, err
	}
	if err := json.Unmarshal([]byte(scope), &f.Scope); err != nil {
		return f, fmt.Errorf("flow %s: invalid scope: %w", f.Name, err)
	}
	if err := json.Unmarshal([]byte(opts), &f.Options); err != nil {
		return f, fmt.Errorf("flow %s: invalid options: %w", f.Name, err)
	}
	f.Schedule = json.RawMessage(sched)
	f.CreatedAt, f.UpdatedAt = parseTime(created), parseTime(updated)
	return f, nil
}

// SaveFlow inserts (ID empty) or updates a flow and replaces its targets.
// Targets keep their ID (and so their mappings) when passed back with it.
func (s *Store) SaveFlow(ctx context.Context, f Flow) (Flow, error) {
	if len(f.Targets) == 0 {
		return f, errors.New("a flow needs at least one mirror")
	}
	if f.Timezone == "" {
		f.Timezone = "UTC"
	}
	if f.Scope.Mode == "" {
		f.Scope.Mode = "all"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return f, err
	}
	defer func() { _ = tx.Rollback() }()

	ts := now()
	sched := rawOr(f.Schedule, `{"kind":"manual"}`)
	insert := f.ID == ""
	if insert {
		f.ID = newID()
		_, err = tx.ExecContext(ctx, `INSERT INTO flows (`+flowCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.ID, f.Name, boolInt(f.Enabled), f.SourceConnectionID, mustJSON(f.Scope), mustJSON(f.Options),
			sched, boolInt(f.ScheduleEnabled), f.Timezone, ts, ts, ts)
	} else {
		// A changed schedule, timezone or switch restarts the count from now,
		// so turning a schedule on never fires at once for times in the past.
		// The CASE reads the old column values.
		_, err = tx.ExecContext(ctx, `UPDATE flows SET name = ?, enabled = ?, source_connection_id = ?, scope = ?, options = ?,
			schedule_anchor = CASE WHEN schedule_anchor = '' OR schedule <> ? OR timezone <> ? OR enabled <> ? OR schedule_enabled <> ?
				THEN ? ELSE schedule_anchor END,
			schedule = ?, schedule_enabled = ?, timezone = ?, updated_at = ? WHERE id = ?`,
			f.Name, boolInt(f.Enabled), f.SourceConnectionID, mustJSON(f.Scope), mustJSON(f.Options),
			sched, f.Timezone, boolInt(f.Enabled), boolInt(f.ScheduleEnabled), ts,
			sched, boolInt(f.ScheduleEnabled), f.Timezone, ts, f.ID)
	}
	if isUniqueViolation(err) {
		return f, fmt.Errorf("a flow named %q %w", f.Name, ErrConflict)
	}
	if isForeignKeyViolation(err) {
		return f, errors.New("the Master connection does not exist")
	}
	if err != nil {
		return f, err
	}

	keep := map[string]bool{}
	for i := range f.Targets {
		t := &f.Targets[i]
		t.Position = i
		if t.ID == "" {
			t.ID = newID()
			_, err = tx.ExecContext(ctx, `INSERT INTO flow_targets (id, flow_id, connection_id, location, position, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ID, f.ID, t.ConnectionID, rawOr(t.Location, "{}"), i, ts, ts)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE flow_targets SET connection_id = ?, location = ?, position = ?, updated_at = ?
				WHERE id = ? AND flow_id = ?`, t.ConnectionID, rawOr(t.Location, "{}"), i, ts, t.ID, f.ID)
		}
		if isForeignKeyViolation(err) {
			return f, fmt.Errorf("mirror %d: the connection does not exist", i+1)
		}
		if err != nil {
			return f, err
		}
		keep[t.ID] = true
	}
	existing, err := queryStrings(ctx, tx, `SELECT id FROM flow_targets WHERE flow_id = ?`, f.ID)
	if err != nil {
		return f, err
	}
	for _, id := range existing {
		if keep[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM flow_targets WHERE id = ?`, id); err != nil {
			return f, err
		}
	}
	if err := tx.Commit(); err != nil {
		return f, err
	}
	return s.GetFlow(ctx, f.ID)
}

// GetFlow finds a flow by id or (case-insensitive) name, with its targets.
func (s *Store) GetFlow(ctx context.Context, idOrName string) (Flow, error) {
	f, err := scanFlow(s.DB.QueryRowContext(ctx,
		`SELECT `+flowCols+` FROM flows WHERE id = ? OR name = ? COLLATE NOCASE ORDER BY id = ? DESC LIMIT 1`,
		idOrName, idOrName, idOrName))
	if err != nil {
		return f, notFound(err)
	}
	f.Targets, err = s.flowTargets(ctx, f.ID)
	return f, err
}

// ListFlows returns all flows with their targets, ordered by name.
func (s *Store) ListFlows(ctx context.Context) ([]Flow, error) {
	out, err := s.scanFlows(ctx)
	if err != nil {
		return nil, err
	}
	// Targets are loaded after the flow rows are closed (one connection at a time).
	for i := range out {
		if out[i].Targets, err = s.flowTargets(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) scanFlows(ctx context.Context) ([]Flow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+flowCols+` FROM flows ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Flow
	for rows.Next() {
		f, err := scanFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryStrings(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *Store) flowTargets(ctx context.Context, flowID string) ([]FlowTarget, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, connection_id, location, position FROM flow_targets WHERE flow_id = ? ORDER BY position`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlowTarget
	for rows.Next() {
		var t FlowTarget
		var loc string
		if err := rows.Scan(&t.ID, &t.ConnectionID, &loc, &t.Position); err != nil {
			return nil, err
		}
		t.Location = json.RawMessage(loc)
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteFlow removes a flow, its targets, mappings and run history.
func (s *Store) DeleteFlow(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM flows WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimSchedule records that the scheduled run of a flow due since the anchor
// prev is being started, by moving the anchor to at. It returns false when
// another process (the server or "tolarix run-due") claimed it first, or the
// flow changed meanwhile. Moving the anchor to the claim time, not to the
// missed schedule time, is what makes missed runs happen once, without
// backfill.
func (s *Store) ClaimSchedule(ctx context.Context, flowID, prev string, at time.Time) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE flows SET schedule_anchor = ? WHERE id = ? AND schedule_anchor = ?`,
		at.UTC().Format(timeLayout), flowID, prev)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

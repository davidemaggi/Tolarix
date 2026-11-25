package store

import (
	"context"
	"errors"
	"time"
)

// ErrLocked means another process is running the flow.
var ErrLocked = errors.New("the flow is already running")

// LockInfo describes who holds a flow lock.
type LockInfo struct {
	RunID       string
	Holder      string
	AcquiredAt  time.Time
	HeartbeatAt time.Time
}

// AcquireLock takes the run lock for a flow. A lock whose heartbeat is
// older than staleAfter is considered abandoned (crashed process) and taken
// over. Returns ErrLocked with the current holder otherwise.
func (s *Store) AcquireLock(ctx context.Context, flowID, runID, holder string, staleAfter time.Duration) (*LockInfo, error) {
	ts := now()
	stale := time.Now().UTC().Add(-staleAfter).Format(timeLayout)
	// One statement: insert, or take over a stale lock. SQLite serialises writers.
	res, err := s.DB.ExecContext(ctx, `INSERT INTO flow_locks (flow_id, run_id, holder, acquired_at, heartbeat_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (flow_id) DO UPDATE SET run_id = excluded.run_id, holder = excluded.holder,
			acquired_at = excluded.acquired_at, heartbeat_at = excluded.heartbeat_at
		WHERE flow_locks.heartbeat_at < ?`, flowID, runID, holder, ts, ts, stale)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil, nil
	}
	info, err := s.LockHolder(ctx, flowID)
	if err != nil {
		return nil, err
	}
	return info, ErrLocked
}

// LockHolder returns the current lock of a flow, or nil.
func (s *Store) LockHolder(ctx context.Context, flowID string) (*LockInfo, error) {
	var li LockInfo
	var acq, hb string
	err := s.DB.QueryRowContext(ctx, `SELECT run_id, holder, acquired_at, heartbeat_at FROM flow_locks WHERE flow_id = ?`, flowID).
		Scan(&li.RunID, &li.Holder, &acq, &hb)
	if err != nil {
		if errors.Is(notFound(err), ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	li.AcquiredAt, li.HeartbeatAt = parseTime(acq), parseTime(hb)
	return &li, nil
}

// ActiveLocks returns every held lock by flow id (stale ones included: the
// caller decides, by HeartbeatAt, whether a run still looks alive).
func (s *Store) ActiveLocks(ctx context.Context) (map[string]LockInfo, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT flow_id, run_id, holder, acquired_at, heartbeat_at FROM flow_locks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]LockInfo{}
	for rows.Next() {
		var flowID, acq, hb string
		var li LockInfo
		if err := rows.Scan(&flowID, &li.RunID, &li.Holder, &acq, &hb); err != nil {
			return nil, err
		}
		li.AcquiredAt, li.HeartbeatAt = parseTime(acq), parseTime(hb)
		out[flowID] = li
	}
	return out, rows.Err()
}

// HeartbeatLock refreshes the lock; it fails if the lock was taken over.
func (s *Store) HeartbeatLock(ctx context.Context, flowID, runID string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE flow_locks SET heartbeat_at = ? WHERE flow_id = ? AND run_id = ?`, now(), flowID, runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("the run lock was lost")
	}
	return nil
}

// ReleaseLock removes the lock if this run still holds it.
func (s *Store) ReleaseLock(ctx context.Context, flowID, runID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM flow_locks WHERE flow_id = ? AND run_id = ?`, flowID, runID)
	return err
}

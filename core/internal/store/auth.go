package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Admin is the single web-mode account.
type Admin struct {
	Username           string
	PasswordHash       string
	MustChangePassword bool
	UpdatedAt          time.Time
}

// GetAdmin returns the admin account, or ErrNotFound before first start.
func (s *Store) GetAdmin(ctx context.Context) (Admin, error) {
	var a Admin
	var updated string
	err := s.DB.QueryRowContext(ctx, `SELECT username, password_hash, must_change_password, updated_at FROM admin_user WHERE id = 1`).
		Scan(&a.Username, &a.PasswordHash, &a.MustChangePassword, &updated)
	a.UpdatedAt = parseTime(updated)
	return a, notFound(err)
}

// CreateAdminIfMissing stores the admin account unless one exists. It
// reports whether it created it, so only the process that did prints the
// one-time password.
func (s *Store) CreateAdminIfMissing(ctx context.Context, passwordHash string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO admin_user (id, password_hash, must_change_password, updated_at)
		VALUES (1, ?, 1, ?) ON CONFLICT (id) DO NOTHING`, passwordHash, now())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// SetAdminPassword replaces the password hash (creating the account if
// needed). Every session except keepSessionHash is ended, so a reset or a
// change logs out every other browser.
func (s *Store) SetAdminPassword(ctx context.Context, passwordHash string, mustChange bool, keepSessionHash string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_user (id, password_hash, must_change_password, updated_at)
		VALUES (1, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET password_hash = excluded.password_hash,
		must_change_password = excluded.must_change_password, updated_at = excluded.updated_at`,
		passwordHash, boolInt(mustChange), now()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash <> ?`, keepSessionHash); err != nil {
		return err
	}
	return tx.Commit()
}

// Session is a server-side login session. Only a hash of the cookie value is stored.
type Session struct {
	IDHash     string
	CSRFToken  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (id_hash, csrf_token, created_at, last_seen_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		sess.IDHash, sess.CSRFToken, fmtTime(sess.CreatedAt), fmtTime(sess.LastSeenAt), fmtTime(sess.ExpiresAt))
	return err
}

// GetSession returns an unexpired session by its hash.
func (s *Store) GetSession(ctx context.Context, idHash string, at time.Time) (Session, error) {
	var sess Session
	var created, seen, expires string
	err := s.DB.QueryRowContext(ctx, `SELECT id_hash, csrf_token, created_at, last_seen_at, expires_at FROM sessions
		WHERE id_hash = ? AND expires_at > ?`, idHash, fmtTime(at)).Scan(&sess.IDHash, &sess.CSRFToken, &created, &seen, &expires)
	if err != nil {
		return sess, notFound(err)
	}
	sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt = parseTime(created), parseTime(seen), parseTime(expires)
	return sess, nil
}

// TouchSession records activity and moves the expiry.
func (s *Store) TouchSession(ctx context.Context, idHash string, seen, expires time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?`,
		fmtTime(seen), fmtTime(expires), idHash)
	return err
}

// DeleteSession ends a session (logout).
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteExpiredSessions removes sessions past their expiry.
func (s *Store) DeleteExpiredSessions(ctx context.Context, at time.Time) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, fmtTime(at))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// IsNotFound reports whether err is ErrNotFound (or sql.ErrNoRows).
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

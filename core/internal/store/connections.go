package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

// Connection is a configured wiki. The secret is never part of this struct;
// read it with ConnectionSecret.
type Connection struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Config    json.RawMessage `json:"config"`
	HasSecret bool            `json:"hasSecret"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

const connCols = `id, name, kind, config, secret_enc IS NOT NULL, created_at, updated_at`

func scanConnection(row interface{ Scan(...any) error }) (Connection, error) {
	var c Connection
	var cfg, created, updated string
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &cfg, &c.HasSecret, &created, &updated)
	c.Config = json.RawMessage(cfg)
	c.CreatedAt, c.UpdatedAt = parseTime(created), parseTime(updated)
	return c, err
}

// SaveConnection inserts (ID empty) or updates a connection. A nil secret
// keeps the stored one; an empty, non-nil secret clears it.
func (s *Store) SaveConnection(ctx context.Context, c Connection, secret *secrets.String, cipher *secrets.Cipher) (Connection, error) {
	ts := now()
	insert := c.ID == ""
	if insert {
		c.ID = newID()
	}
	var enc []byte
	if secret != nil && secret.IsSet() {
		if cipher == nil {
			return c, errors.New("no secret key available to encrypt the secret")
		}
		var err error
		if enc, err = cipher.Seal([]byte(secret.Reveal()), []byte(c.ID)); err != nil {
			return c, err
		}
	}
	var err error
	switch {
	case insert:
		_, err = s.DB.ExecContext(ctx, `INSERT INTO connections (id, name, kind, config, secret_enc, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, c.ID, c.Name, c.Kind, rawOr(c.Config, "{}"), enc, ts, ts)
	case secret == nil:
		_, err = s.DB.ExecContext(ctx, `UPDATE connections SET name = ?, kind = ?, config = ?, updated_at = ? WHERE id = ?`,
			c.Name, c.Kind, rawOr(c.Config, "{}"), ts, c.ID)
	default:
		_, err = s.DB.ExecContext(ctx, `UPDATE connections SET name = ?, kind = ?, config = ?, secret_enc = ?, updated_at = ? WHERE id = ?`,
			c.Name, c.Kind, rawOr(c.Config, "{}"), enc, ts, c.ID)
	}
	if isUniqueViolation(err) {
		return c, fmt.Errorf("a connection named %q %w", c.Name, ErrConflict)
	}
	if err != nil {
		return c, err
	}
	return s.GetConnection(ctx, c.ID)
}

// GetConnection finds a connection by id or (case-insensitive) name.
func (s *Store) GetConnection(ctx context.Context, idOrName string) (Connection, error) {
	c, err := scanConnection(s.DB.QueryRowContext(ctx,
		`SELECT `+connCols+` FROM connections WHERE id = ? OR name = ? COLLATE NOCASE ORDER BY id = ? DESC LIMIT 1`,
		idOrName, idOrName, idOrName))
	return c, notFound(err)
}

// ListConnections returns all connections ordered by name.
func (s *Store) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+connCols+` FROM connections ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConnectionSecret decrypts the stored secret ("" when none).
func (s *Store) ConnectionSecret(ctx context.Context, id string, cipher *secrets.Cipher) (secrets.String, error) {
	var enc []byte
	err := s.DB.QueryRowContext(ctx, `SELECT secret_enc FROM connections WHERE id = ?`, id).Scan(&enc)
	if err != nil {
		return secrets.String{}, notFound(err)
	}
	if len(enc) == 0 {
		return secrets.String{}, nil
	}
	if cipher == nil {
		return secrets.String{}, errors.New("no secret key available to decrypt the secret")
	}
	pt, err := cipher.Open(enc, []byte(id))
	if err != nil {
		return secrets.String{}, err
	}
	return secrets.New(string(pt)), nil
}

// DeleteConnection removes a connection not used by any flow.
func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM connections WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return errors.New("this connection is used by a flow; remove it from the flow first")
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

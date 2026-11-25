package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a looked-up row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on unique-constraint violations (e.g. duplicate names).
var ErrConflict = errors.New("already exists")

func newID() string { return uuid.Must(uuid.NewV7()).String() }

const timeLayout = time.RFC3339Nano

func now() string { return time.Now().UTC().Format(timeLayout) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeLayout, s)
	return t
}

func parseNullTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTime(s.String)
	return &t
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func rawOr(b json.RawMessage, def string) string {
	if len(b) == 0 || string(b) == "null" {
		return def
	}
	return string(b)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only plain structs are marshalled here
	}
	return string(b)
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Package store owns the SQLite database shared by the server, the scheduler
// and the CLI. It opens the file in WAL mode with a busy timeout so several
// processes can use it at once, and applies embedded migrations on open.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the database handle.
type Store struct {
	DB *sql.DB
}

// Open opens (creating if needed) the database at path and migrates it to the latest version.
func Open(ctx context.Context, path string, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := createPrivate(path); err != nil {
		return nil, fmt.Errorf("create database %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	s := &Store{DB: db}
	if err := s.migrate(ctx, log); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// createPrivate creates an empty database file readable by its owner only
// (it holds the encrypted secrets and the admin password hash). SQLite gives
// the -wal and -shm files the same permissions. An existing file is left as is.
func createPrivate(path string) error {
	if path == "" || path == ":memory:" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the configured database path
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// dsn builds a modernc.org/sqlite DSN. Pragmas are applied to every pooled
// connection, which matters for foreign_keys and busy_timeout.
func dsn(path string) string {
	q := url.Values{}
	for _, p := range []string{
		"busy_timeout(10000)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
	} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
}

func (s *Store) migrate(ctx context.Context, log *slog.Logger) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.DB, sub)
	if err != nil {
		return fmt.Errorf("prepare migrations: %w", err)
	}
	current, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	sources := p.ListSources()
	if n := len(sources); n > 0 && current > sources[n-1].Version {
		return fmt.Errorf("database schema version %d is newer than this Tolarix build supports (%d); upgrade Tolarix",
			current, sources[n-1].Version)
	}
	results, err := p.Up(ctx)
	if err != nil && !errors.Is(err, goose.ErrNoNextVersion) {
		return fmt.Errorf("migrate database: %w", err)
	}
	for _, r := range results {
		log.Info("applied database migration", "version", r.Source.Version, "file", r.Source.Path)
	}
	return nil
}

// SchemaVersion returns the current migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return 0, err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.DB, sub)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}

// Ping checks the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.DB.PingContext(ctx) }

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tolarix.db")
	s, err := Open(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, path := openTemp(t)

	v, err := s.SchemaVersion(ctx)
	if err != nil || v < 1 {
		t.Fatalf("version=%d err=%v", v, err)
	}
	for _, table := range []string{
		"settings", "admin_user", "sessions", "connections", "flows", "flow_targets",
		"page_mappings", "attachment_mappings", "runs", "run_events", "flow_locks",
	} {
		var n int
		if err := s.DB.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (err=%v)", table, err)
		}
	}
	_ = s.Close()

	s2, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	v2, _ := s2.SchemaVersion(ctx)
	if v2 != v {
		t.Fatalf("version changed on reopen: %d -> %d", v, v2)
	}
}

func TestPragmasApplyToEveryConnection(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	s.DB.SetMaxOpenConns(4)

	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := s.DB.Conn(ctx)
			if err != nil {
				errs <- err.Error()
				return
			}
			defer conn.Close()
			var fk int
			var mode string
			_ = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk)
			_ = conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode)
			if fk != 1 || mode != "wal" {
				errs <- fmt.Sprintf("foreign_keys=%d journal_mode=%s", fk, mode)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO flows (id, name, source_connection_id, created_at, updated_at)
		VALUES ('f1', 'flow', 'missing-conn', 'now', 'now')`)
	if err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestSingleAdminRow(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO admin_user (id, password_hash, updated_at) VALUES (1, 'h', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO admin_user (id, password_hash, updated_at) VALUES (2, 'h', 'now')`); err == nil {
		t.Fatal("a second admin row must be rejected")
	}
}

func TestNewDatabaseIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	_, path := openTemp(t)
	for _, p := range []string{path, path + "-wal"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %v, want 0600", filepath.Base(p), st.Mode().Perm())
		}
	}
}

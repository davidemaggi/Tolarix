package runner

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/davidemaggi/tolarix/core/internal/connectors/obsidian"
	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
	"github.com/davidemaggi/tolarix/core/internal/store"
	"github.com/davidemaggi/tolarix/core/internal/testkit"
)

type e2e struct {
	t      *testing.T
	st     *store.Store
	r      *Runner
	flow   store.Flow
	master string
	mirror string
}

func newE2E(t *testing.T, opts engine.Options) *e2e {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "tolarix.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	ci, _ := secrets.NewCipher(key)

	master := filepath.Join(dir, "master")
	mirror := filepath.Join(dir, "mirror")
	testkit.CopyDir(t, "../connectors/obsidian/testdata/decode/basic/vault", master)
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := func(p string) json.RawMessage { b, _ := json.Marshal(map[string]string{"path": p}); return b }
	src, err := st.SaveConnection(ctx, store.Connection{Name: "Notes", Kind: "obsidian", Config: cfg(master)}, nil, ci)
	if err != nil {
		t.Fatal(err)
	}
	dst, _ := st.SaveConnection(ctx, store.Connection{Name: "Team vault", Kind: "obsidian", Config: cfg(mirror)}, nil, ci)
	flow, err := st.SaveFlow(ctx, store.Flow{
		Name: "Notes to team", Enabled: true, SourceConnectionID: src.ID, Options: opts,
		Targets: []store.FlowTarget{{ConnectionID: dst.ID, Location: json.RawMessage(`{"folder":"Mirror"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &e2e{t: t, st: st, r: &Runner{Store: st, Cipher: ci, HeartbeatEvery: 10 * time.Millisecond}, flow: flow, master: master, mirror: mirror}
}

func (e *e2e) run(dry bool) *Result {
	e.t.Helper()
	res, err := e.r.RunFlow(context.Background(), e.flow, TriggerCLI, dry)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *e2e) write(rel, content string) {
	e.t.Helper()
	p := filepath.Join(e.master, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestObsidianToObsidianEndToEnd(t *testing.T) {
	e := newE2E(t, engine.Options{DeletePolicy: engine.ArchiveOnMirror})

	// 1. First sync creates everything, matching the golden mirror.
	res := e.run(false)
	if res.Run.Status != store.RunSucceeded {
		t.Fatalf("status %s: %s", res.Run.Status, res.Run.Error)
	}
	c := res.Report.Counts
	if c.Created != 10 || c.AttachmentsUploaded != 4 || c.Failed != 0 {
		t.Fatalf("first run counts %+v", c)
	}
	testkit.GoldenDir(t, "testdata/e2e/first", e.mirror)

	// 2. Nothing changed: nothing written.
	if c := e.run(false).Report.Counts; c.Changes() != 0 || c.Unchanged != 10 {
		t.Fatalf("second run counts %+v", c)
	}

	// 3. Edit, rename (same content => move), delete (=> archive).
	e.write("Projects/Beta.md", "# Beta\n\n## Status\n\nAmber.\n")
	if err := os.Rename(filepath.Join(e.master, "Notes", "Duplicate.md"), filepath.Join(e.master, "Notes", "Twin.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.master, "Archive", "Old Note.md")); err != nil {
		t.Fatal(err)
	}

	plan := e.run(true)
	if pc := plan.Report.Counts; pc.Updated != 2 || pc.Moved != 1 || pc.Archived != 1 || pc.Deleted != 1 {
		// Updated: Beta (edited) and Home (its links to the renamed and the
		// deleted note changed). Archived: Old Note. Deleted: the folder-only
		// page "Archive" that held it.
		t.Fatalf("dry-run plan %+v\n%+v", pc, plan.Report.Targets[0].Ops)
	}
	if _, err := os.Stat(filepath.Join(e.mirror, "Mirror", "Archived")); !os.IsNotExist(err) {
		t.Fatal("dry-run must not write")
	}

	res = e.run(false)
	if res.Run.Status != store.RunSucceeded {
		t.Fatalf("status %s", res.Run.Status)
	}
	testkit.GoldenDir(t, "testdata/e2e/after-changes", e.mirror)
	if c := e.run(false).Report.Counts; c.Changes() != 0 {
		t.Fatalf("not stable after changes: %+v", c)
	}

	// Run history and live events were recorded.
	runs, _ := e.st.ListRuns(context.Background(), e.flow.ID, 0)
	if len(runs) != 5 { // first, idempotent, dry-run, changes, stability check
		t.Fatalf("runs %d", len(runs))
	}
	evs, _ := e.st.RunEvents(context.Background(), runs[0].ID, 0, 0)
	if len(evs) < 3 || !strings.Contains(evs[len(evs)-1].Msg, "run finished") {
		t.Fatalf("events %+v", evs)
	}
}

func TestLockPreventsConcurrentRuns(t *testing.T) {
	e := newE2E(t, engine.Options{})
	ctx := context.Background()
	if _, err := e.st.AcquireLock(ctx, e.flow.ID, "other-run", "cron/42", time.Minute); err != nil {
		t.Fatal(err)
	}
	_, err := e.r.RunFlow(ctx, e.flow, TriggerCLI, false)
	var le *LockedError
	if !errors.As(err, &le) || !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "cron/42") {
		t.Fatalf("expected a lock error naming the holder, got %v", err)
	}
	if runs, _ := e.st.ListRuns(ctx, e.flow.ID, 0); len(runs) != 0 {
		t.Fatal("a refused run must not be recorded")
	}
	_ = e.st.ReleaseLock(ctx, e.flow.ID, "other-run")
	if res := e.run(false); res.Run.Status != store.RunSucceeded {
		t.Fatal(res.Run.Status)
	}
	if li, _ := e.st.LockHolder(ctx, e.flow.ID); li != nil {
		t.Fatal("lock not released after the run")
	}
}

func TestMissingMasterFailsTheRun(t *testing.T) {
	e := newE2E(t, engine.Options{})
	_ = os.RemoveAll(e.master)
	res := e.run(false)
	if res.Run.Status != store.RunFailed || !strings.Contains(res.Run.Error, "does not exist") {
		t.Fatalf("%s: %s", res.Run.Status, res.Run.Error)
	}
}

func TestMissingMirrorIsAFailedTarget(t *testing.T) {
	e := newE2E(t, engine.Options{})
	_ = os.RemoveAll(e.mirror)
	res := e.run(false)
	if res.Run.Status != store.RunFailed || len(res.Report.Targets) != 1 || res.Report.Targets[0].Error == "" {
		t.Fatalf("%s %+v", res.Run.Status, res.Report.Targets)
	}
}

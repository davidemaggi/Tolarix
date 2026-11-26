package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

func testCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	c, err := secrets.NewCipher(k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConnectionsCRUDAndSecrets(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	ci := testCipher(t)

	pat := secrets.New("my-pat")
	c, err := s.SaveConnection(ctx, Connection{Name: "Docs", Kind: "obsidian", Config: json.RawMessage(`{"path":"/v"}`)}, &pat, ci)
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasSecret || c.ID == "" {
		t.Fatalf("%+v", c)
	}
	if _, err := s.SaveConnection(ctx, Connection{Name: "docs", Kind: "obsidian"}, nil, ci); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name (case-insensitive): %v", err)
	}
	got, err := s.GetConnection(ctx, "DOCS")
	if err != nil || got.ID != c.ID {
		t.Fatalf("by name: %v %+v", err, got)
	}
	sec, err := s.ConnectionSecret(ctx, c.ID, ci)
	if err != nil || sec.Reveal() != "my-pat" {
		t.Fatalf("secret: %v", err)
	}
	// The secret is not readable with another key.
	if _, err := s.ConnectionSecret(ctx, c.ID, testCipher(t)); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("other key: %v", err)
	}
	// Update without a secret keeps it; an empty secret clears it.
	c.Name = "Docs 2"
	if c, err = s.SaveConnection(ctx, c, nil, ci); err != nil || !c.HasSecret || c.Name != "Docs 2" {
		t.Fatalf("update: %v %+v", err, c)
	}
	empty := secrets.New("")
	if c, err = s.SaveConnection(ctx, c, &empty, ci); err != nil || c.HasSecret {
		t.Fatalf("clear: %v %+v", err, c)
	}
	list, _ := s.ListConnections(ctx)
	if len(list) != 1 {
		t.Fatalf("list %d", len(list))
	}
	if err := s.DeleteConnection(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetConnection(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func seedFlow(t *testing.T, s *Store) Flow {
	t.Helper()
	ctx := context.Background()
	src, _ := s.SaveConnection(ctx, Connection{Name: "src", Kind: "obsidian"}, nil, nil)
	dst, _ := s.SaveConnection(ctx, Connection{Name: "dst", Kind: "obsidian"}, nil, nil)
	f, err := s.SaveFlow(ctx, Flow{
		Name: "Flow", Enabled: true, SourceConnectionID: src.ID,
		Options: engine.Options{DeletePolicy: engine.DeleteOnMirror, Banner: true},
		Targets: []FlowTarget{{ConnectionID: dst.ID, Location: json.RawMessage(`{"folder":"A"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFlowsSaveKeepsTargetIDs(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	f := seedFlow(t, s)
	if len(f.Targets) != 1 || f.Scope.Mode != "all" || f.Timezone != "UTC" || !f.Options.Banner {
		t.Fatalf("%+v", f)
	}
	tid := f.Targets[0].ID
	if err := s.UpsertMapping(ctx, tid, engine.Mapping{SourceID: "p", MirrorID: "m", MirrorKey: "k"}); err != nil {
		t.Fatal(err)
	}

	f.Targets = append(f.Targets, FlowTarget{ConnectionID: f.SourceConnectionID})
	f, err := s.SaveFlow(ctx, f)
	if err != nil || len(f.Targets) != 2 || f.Targets[0].ID != tid {
		t.Fatalf("%v %+v", err, f.Targets)
	}
	if ms, _ := s.LoadMappings(ctx, tid); len(ms) != 1 {
		t.Fatal("mappings must survive a flow edit")
	}
	// Dropping a target drops its mappings.
	f.Targets = f.Targets[1:]
	if f, err = s.SaveFlow(ctx, f); err != nil || len(f.Targets) != 1 {
		t.Fatal(err)
	}
	if ms, _ := s.LoadMappings(ctx, tid); len(ms) != 0 {
		t.Fatal("mappings of a removed target must be deleted")
	}
	if err := s.DeleteConnection(ctx, f.SourceConnectionID); err == nil {
		t.Fatal("a connection used by a flow must not be deletable")
	}
}

func TestMappingsUpsert(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	tid := seedFlow(t, s).Targets[0].ID
	m := engine.Mapping{SourceID: "a", MirrorID: "1", MirrorKey: "A.md", SourceHash: "s", BodyHash: "b", MirrorHash: "h"}
	_ = s.UpsertMapping(ctx, tid, m)
	m.MirrorKey, m.State = "Archived/A.md", engine.StateArchived
	_ = s.UpsertMapping(ctx, tid, m)
	ms, _ := s.LoadMappings(ctx, tid)
	if len(ms) != 1 || ms[0] != m {
		t.Fatalf("%+v", ms)
	}
	_ = s.UpsertAttachmentMapping(ctx, tid, "h1", "att/x.png")
	_ = s.UpsertAttachmentMapping(ctx, tid, "h1", "att/y.png")
	am, _ := s.LoadAttachmentMappings(ctx, tid)
	if am["h1"] != "att/y.png" {
		t.Fatalf("%v", am)
	}
	_ = s.DeleteAttachmentMapping(ctx, tid, "h1")
	_ = s.DeleteMapping(ctx, tid, "a")
	if ms, _ := s.LoadMappings(ctx, tid); len(ms) != 0 {
		t.Fatal("not deleted")
	}
}

func TestRunsAndEvents(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	f := seedFlow(t, s)
	r, err := s.CreateRun(ctx, "", f.ID, "cli", true)
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := s.AddRunEvent(ctx, r.ID, "INFO", "first", map[string]any{"pages": 3})
	_, _ = s.AddRunEvent(ctx, r.ID, "WARN", "second", nil)
	evs, _ := s.RunEvents(ctx, r.ID, id1, 0)
	if len(evs) != 1 || evs[0].Msg != "second" {
		t.Fatalf("%+v", evs)
	}
	if err := s.FinishRun(ctx, r.ID, RunSucceeded, engine.Counts{Created: 2}, map[string]any{"ok": true}, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRun(ctx, r.ID[:8])
	if err != nil || got.Status != RunSucceeded || got.FinishedAt == nil || !got.DryRun || got.FlowName != "Flow" {
		t.Fatalf("%v %+v", err, got)
	}
	var c engine.Counts
	_ = json.Unmarshal(got.Counts, &c)
	if c.Created != 2 || len(got.Report) == 0 {
		t.Fatalf("%s", got.Counts)
	}
	list, _ := s.ListRuns(ctx, f.ID, 10)
	if len(list) != 1 {
		t.Fatal(len(list))
	}
}

func TestLocks(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	f := seedFlow(t, s)
	if _, err := s.AcquireLock(ctx, f.ID, "run1", "host/1", time.Minute); err != nil {
		t.Fatal(err)
	}
	info, err := s.AcquireLock(ctx, f.ID, "run2", "host/2", time.Minute)
	if !errors.Is(err, ErrLocked) || info == nil || info.RunID != "run1" {
		t.Fatalf("%v %+v", err, info)
	}
	if err := s.HeartbeatLock(ctx, f.ID, "run1"); err != nil {
		t.Fatal(err)
	}
	// A stale lock is taken over.
	if _, err := s.AcquireLock(ctx, f.ID, "run3", "host/3", -time.Second); err != nil {
		t.Fatalf("stale takeover: %v", err)
	}
	if err := s.HeartbeatLock(ctx, f.ID, "run1"); err == nil {
		t.Fatal("old holder must notice the takeover")
	}
	_ = s.ReleaseLock(ctx, f.ID, "run1") // not the holder: no effect
	if li, _ := s.LockHolder(ctx, f.ID); li == nil || li.RunID != "run3" {
		t.Fatalf("%+v", li)
	}
	_ = s.ReleaseLock(ctx, f.ID, "run3")
	if li, _ := s.LockHolder(ctx, f.ID); li != nil {
		t.Fatal("not released")
	}
}

func TestMarkInterruptedRuns(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	f := seedFlow(t, s)
	r1, _ := s.CreateRun(ctx, "", f.ID, "cli", false)
	r2, _ := s.CreateRun(ctx, "", f.ID, "cli", false)
	_, _ = s.AcquireLock(ctx, f.ID, r2.ID, "me", time.Minute)
	if n, err := s.MarkInterruptedRuns(ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	got, _ := s.GetRun(ctx, r1.ID)
	if got.Status != RunFailed {
		t.Fatal(got.Status)
	}
}

func TestScheduleAnchorAndClaim(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	f := seedFlow(t, s)
	if f.ScheduleAnchor == "" || !f.Anchor().Equal(f.CreatedAt) {
		t.Fatalf("anchor on create = %q", f.ScheduleAnchor)
	}

	// Claiming moves the anchor; a second claimer with the old value loses.
	at := time.Now().Add(time.Minute)
	if ok, err := s.ClaimSchedule(ctx, f.ID, f.ScheduleAnchor, at); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, _ := s.ClaimSchedule(ctx, f.ID, f.ScheduleAnchor, at); ok {
		t.Fatal("a stale claim must fail")
	}
	got, _ := s.GetFlow(ctx, f.ID)
	if !got.Anchor().Equal(at.UTC().Truncate(time.Nanosecond)) {
		t.Fatalf("anchor = %v, want %v", got.Anchor(), at)
	}

	// Editing other fields keeps the anchor; changing the schedule resets it.
	got.Name = "Renamed"
	saved, err := s.SaveFlow(ctx, got)
	if err != nil || saved.ScheduleAnchor != got.ScheduleAnchor {
		t.Fatalf("rename changed the anchor: %v %q -> %q", err, got.ScheduleAnchor, saved.ScheduleAnchor)
	}
	for _, change := range []func(*Flow){
		func(f *Flow) { f.Schedule = json.RawMessage(`{"kind":"daily","time":"07:30"}`) },
		func(f *Flow) { f.Timezone = "Europe/Rome" },
		func(f *Flow) { f.Enabled = !f.Enabled },
		func(f *Flow) { f.ScheduleEnabled = !f.ScheduleEnabled },
	} {
		before := saved.ScheduleAnchor
		change(&saved)
		if saved, err = s.SaveFlow(ctx, saved); err != nil || saved.ScheduleAnchor == before {
			t.Fatalf("anchor not reset: %v %q", err, before)
		}
	}

	// Rows from before the anchor column fall back to updated_at, and get
	// an anchor on the next save.
	if _, err := s.DB.ExecContext(ctx, `UPDATE flows SET schedule_anchor = '' WHERE id = ?`, f.ID); err != nil {
		t.Fatal(err)
	}
	legacy, _ := s.GetFlow(ctx, f.ID)
	if !legacy.Anchor().Equal(legacy.UpdatedAt) {
		t.Fatal("empty anchor must fall back to updated_at")
	}
	if saved, _ = s.SaveFlow(ctx, legacy); saved.ScheduleAnchor == "" {
		t.Fatal("save must fill an empty anchor")
	}
}

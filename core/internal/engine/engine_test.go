package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

type harness struct {
	t      *testing.T
	src    *fakeSource
	mirror *fakeMirror
	store  *memStore
	opts   Options
	scope  Scope
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, src: newSource(), mirror: newMirror(), store: newStore()}
	h.src.page("home", "", "Home", "Welcome", 0)
	h.src.page("docs", "", "Docs", "All docs", 1)
	h.src.page("docs/a", "docs", "A", "Page A", 0)
	h.src.page("docs/b", "docs", "B", "Page B", 1)
	return h
}

func (h *harness) run(dry bool) *Report {
	h.t.Helper()
	sess, _ := h.mirror.Open(context.Background(), nil)
	e := &Engine{Mappings: h.store}
	rep, err := e.Run(context.Background(), Request{
		Source: h.src, SourceName: "Test Master", Scope: h.scope, Options: h.opts, DryRun: dry,
		Targets: []Target{{ID: "t1", Name: "mirror", Session: sess}},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return rep
}

func counts(c Counts) map[string]int {
	out := map[string]int{}
	v := reflect.ValueOf(c)
	for i := 0; i < v.NumField(); i++ {
		if n := int(v.Field(i).Int()); n != 0 {
			out[v.Type().Field(i).Name] = n
		}
	}
	return out
}

func expectCounts(t *testing.T, rep *Report, want map[string]int) {
	t.Helper()
	if got := counts(rep.Counts); !reflect.DeepEqual(got, want) {
		t.Fatalf("counts %v, want %v\nops: %+v", got, want, rep.Targets[0].Ops)
	}
}

func TestCreateThenIdempotent(t *testing.T) {
	h := newHarness(t)
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Created": 4})
	if rep.Status() != "succeeded" {
		t.Fatal(rep.Status())
	}
	if got := strings.Join(h.mirror.keys(), ","); got != "Docs.md,Docs/A.md,Docs/B.md,Home.md" {
		t.Fatalf("mirror %s", got)
	}

	writes := h.mirror.writes
	rep = h.run(false)
	expectCounts(t, rep, map[string]int{"Unchanged": 4})
	if h.mirror.writes != writes {
		t.Fatal("a repeated run must not write")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	h := newHarness(t)
	rep := h.run(true)
	expectCounts(t, rep, map[string]int{"Created": 4})
	if !rep.DryRun || len(h.mirror.keys()) != 0 || len(h.store.maps["t1"]) != 0 {
		t.Fatal("dry-run wrote something")
	}
	if ops := rep.Targets[0].Ops; len(ops) != 4 || ops[0].To == "" {
		t.Fatalf("plan should list the operations: %+v", ops)
	}
}

func TestUpdate(t *testing.T) {
	h := newHarness(t)
	h.run(false)
	h.src.tree.Pages["docs/a"].Body = "Page A, edited"
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Updated": 1, "Unchanged": 3})
	if !strings.Contains(h.mirror.pages["Docs/A.md"], "edited") {
		t.Fatal("not updated")
	}
}

func TestRenameWithNewIDIsAMove(t *testing.T) {
	h := newHarness(t)
	h.run(false)
	// Path-identity source: renaming changes the ID; body is unchanged.
	p := h.src.tree.Pages["docs/b"]
	delete(h.src.tree.Pages, "docs/b")
	p.ID, p.Title, p.Name = "docs/b2", "B renamed", "B renamed"
	h.src.tree.Add(p)

	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Moved": 1, "Unchanged": 3})
	if _, ok := h.mirror.pages["Docs/B.md"]; ok {
		t.Fatal("old page left behind")
	}
	if _, ok := h.mirror.pages["Docs/B renamed.md"]; !ok {
		t.Fatal("moved page missing")
	}
	if _, ok := h.store.maps["t1"]["docs/b"]; ok {
		t.Fatal("old mapping not removed")
	}
	if m := h.store.maps["t1"]["docs/b2"]; m.MirrorKey != "Docs/B renamed.md" {
		t.Fatalf("mapping %+v", m)
	}
	expectCounts(t, h.run(false), map[string]int{"Unchanged": 4})
}

func TestMoveUnderOtherParent(t *testing.T) {
	h := newHarness(t)
	h.run(false)
	h.src.tree.Pages["docs/a"].ParentID = "home"
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Moved": 1, "Unchanged": 3})
	if _, ok := h.mirror.pages["Home/A.md"]; !ok {
		t.Fatalf("mirror %v", h.mirror.keys())
	}
}

func TestDeletePolicies(t *testing.T) {
	for policy, wantKeys := range map[DeletePolicy]string{
		DeleteOnMirror:  "Docs.md,Docs/A.md,Home.md",
		ArchiveOnMirror: "Archived/Docs/B.md,Docs.md,Docs/A.md,Home.md",
		KeepOnMirror:    "Docs.md,Docs/A.md,Docs/B.md,Home.md",
	} {
		t.Run(string(policy), func(t *testing.T) {
			h := newHarness(t)
			h.opts.DeletePolicy = policy
			h.run(false)
			delete(h.src.tree.Pages, "docs/b")
			h.run(false)
			if got := strings.Join(h.mirror.keys(), ","); got != wantKeys {
				t.Fatalf("mirror %s want %s", got, wantKeys)
			}
			// The second run after the removal is stable.
			rep := h.run(false)
			if rep.Counts.Changes() != 0 {
				t.Fatalf("not stable: %v", counts(rep.Counts))
			}
		})
	}
}

func TestArchivedPageIsRestoredWhenItComesBack(t *testing.T) {
	h := newHarness(t)
	h.run(false)
	saved := h.src.tree.Pages["docs/b"]
	delete(h.src.tree.Pages, "docs/b")
	expectCounts(t, h.run(false), map[string]int{"Archived": 1, "Unchanged": 3})
	h.src.tree.Add(saved)
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Moved": 1, "Unchanged": 3})
	if _, ok := h.mirror.pages["Docs/B.md"]; !ok {
		t.Fatalf("not restored: %v", h.mirror.keys())
	}
}

func TestMirrorEditIsOverwrittenWithWarning(t *testing.T) {
	h := newHarness(t)
	h.run(false)
	h.mirror.pages["Home.md"] = "someone edited the mirror"
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Updated": 1, "Unchanged": 3})
	if !hasWarning(rep, "mirror-edited") {
		t.Fatal("expected a mirror-edited warning")
	}
}

func TestMirrorPageDeletedIsRecreated(t *testing.T) {
	h := newHarness(t)
	h.run(false)
	delete(h.mirror.pages, "Home.md")
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Created": 1, "Unchanged": 3})
	if !hasWarning(rep, "mirror-missing") {
		t.Fatal("expected a mirror-missing warning")
	}
}

func TestUnmanagedPageCollision(t *testing.T) {
	h := newHarness(t)
	h.mirror.pages["Home.md"] = "hand-written page"
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Created": 3, "Failed": 1})
	if rep.Status() != "partial" {
		t.Fatal(rep.Status())
	}
	if h.mirror.pages["Home.md"] != "hand-written page" {
		t.Fatal("unmanaged page must not be touched")
	}
	h.opts.AdoptExisting = true
	expectCounts(t, h.run(false), map[string]int{"Updated": 1, "Unchanged": 3})
	if !strings.Contains(h.mirror.pages["Home.md"], "Welcome") {
		t.Fatal("adopted page not overwritten")
	}
}

func TestFailedWriteIsRetriedNextRun(t *testing.T) {
	h := newHarness(t)
	h.mirror.failKey = "Docs/A.md"
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Created": 3, "Failed": 1})
	h.mirror.failKey = ""
	expectCounts(t, h.run(false), map[string]int{"Created": 1, "Unchanged": 3})
}

func TestAttachments(t *testing.T) {
	h := newHarness(t)
	att := h.src.attach(h.src.tree.Pages["docs/a"], "pic.png", "PNG1")
	rep := h.run(false)
	expectCounts(t, rep, map[string]int{"Created": 4, "AttachmentsUploaded": 1})
	if string(h.mirror.atts["att/pic.png"]) != "PNG1" {
		t.Fatal("attachment not uploaded")
	}
	if !strings.Contains(h.mirror.pages["Docs/A.md"], "att/pic.png") {
		t.Fatal("link not rewritten")
	}
	expectCounts(t, h.run(false), map[string]int{"Unchanged": 4})

	// Removing the reference deletes it only with the "delete" policy.
	p := h.src.tree.Pages["docs/a"]
	p.Attachments, p.Body = nil, "Page A"
	delete(h.src.tree.Attachments, att)
	expectCounts(t, h.run(false), map[string]int{"Updated": 1, "Unchanged": 3})
	if _, ok := h.mirror.atts["att/pic.png"]; !ok {
		t.Fatal("archive policy must keep attachments")
	}
	h.opts.DeletePolicy = DeleteOnMirror
	expectCounts(t, h.run(false), map[string]int{"Unchanged": 4, "AttachmentsDeleted": 1})
}

func TestAttachmentsDisabled(t *testing.T) {
	h := newHarness(t)
	h.src.attach(h.src.tree.Pages["home"], "pic.png", "PNG1")
	off := false
	h.opts.Attachments = &off
	expectCounts(t, h.run(false), map[string]int{"Created": 4})
	if len(h.mirror.atts) != 0 {
		t.Fatal("attachments copied although disabled")
	}
}

func TestBanner(t *testing.T) {
	h := newHarness(t)
	h.opts.Banner = true
	h.run(false)
	if !strings.Contains(h.mirror.pages["Home.md"], "mirrored from Test Master") {
		t.Fatalf("banner missing: %q", h.mirror.pages["Home.md"])
	}
}

func TestScopeSelectedReroots(t *testing.T) {
	h := newHarness(t)
	h.scope = Scope{Mode: "selected", Include: []string{"docs/a", "missing"}, Exclude: nil}
	rep := h.run(false)
	if got := strings.Join(h.mirror.keys(), ","); got != "A.md" {
		t.Fatalf("mirror %s", got)
	}
	if !hasSourceWarning(rep, "scope-missing") {
		t.Fatal("expected scope-missing warning")
	}
}

func TestScopeExclude(t *testing.T) {
	h := newHarness(t)
	h.scope = Scope{Mode: "all", Exclude: []string{"docs"}}
	h.run(false)
	if got := strings.Join(h.mirror.keys(), ","); got != "Home.md" {
		t.Fatalf("mirror %s", got)
	}
}

func TestPlaceholder(t *testing.T) {
	h := newHarness(t)
	ph := h.src.page("group", "", "Group", "", 2)
	ph.Placeholder = true
	h.src.page("group/x", "group", "X", "x", 0)
	expectCounts(t, h.run(false), map[string]int{"Created": 6})
	expectCounts(t, h.run(false), map[string]int{"Unchanged": 6})
}

func TestSourceErrorFailsRun(t *testing.T) {
	h := newHarness(t)
	h.src.err = context.DeadlineExceeded
	sess, _ := h.mirror.Open(context.Background(), nil)
	_, err := (&Engine{Mappings: h.store}).Run(context.Background(), Request{Source: h.src, Targets: []Target{{ID: "t1", Session: sess}}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStatus(t *testing.T) {
	r := &Report{Targets: []TargetReport{{Error: "x"}}}
	if r.Status() != "failed" {
		t.Fatal(r.Status())
	}
	r.Targets = append(r.Targets, TargetReport{})
	if r.Status() != "partial" {
		t.Fatal(r.Status())
	}
}

func hasWarning(rep *Report, code string) bool {
	for _, t := range rep.Targets {
		for _, w := range t.Warnings {
			if w.Code == code {
				return true
			}
		}
	}
	return false
}

func hasSourceWarning(rep *Report, code string) bool {
	for _, w := range rep.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

var _ connectors.Source = (*fakeSource)(nil)
var _ connectors.Target = (*fakeMirror)(nil)
var _ = model.NewTree

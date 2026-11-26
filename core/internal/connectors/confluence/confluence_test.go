package confluence

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/connectors/obsidian"
	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/engine/enginetest"
	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestParseURL(t *testing.T) {
	cases := []struct {
		in   string
		want Address
		err  string
	}{
		{"https://wiki.example.com/display/DOC", Address{BaseURL: "https://wiki.example.com", SpaceKey: "DOC"}, ""},
		{"https://wiki.example.com/confluence/display/DOC/Getting+Started", Address{BaseURL: "https://wiki.example.com/confluence", SpaceKey: "DOC", PageTitle: "Getting Started"}, ""},
		{"https://wiki.example.com/pages/viewpage.action?pageId=12345", Address{BaseURL: "https://wiki.example.com", PageID: "12345"}, ""},
		{"https://wiki.example.com/pages/viewpage.action?spaceKey=DOC&title=A+B", Address{BaseURL: "https://wiki.example.com", SpaceKey: "DOC", PageTitle: "A B"}, ""},
		{"https://wiki.example.com/spaces/viewspace.action?key=DOC", Address{BaseURL: "https://wiki.example.com", SpaceKey: "DOC"}, ""},
		{"https://wiki.example.com/spaces/DOC/pages/987/My+Page", Address{BaseURL: "https://wiki.example.com", SpaceKey: "DOC", PageID: "987"}, ""},
		{"https://wiki.example.com/spaces/DOC/overview", Address{BaseURL: "https://wiki.example.com", SpaceKey: "DOC"}, ""},
		{"https://wiki.example.com/spaces/DOC", Address{BaseURL: "https://wiki.example.com", SpaceKey: "DOC"}, ""},
		{"https://wiki.example.com/x/AbCd", Address{}, "short links"},
		{"wiki.example.com/display/DOC", Address{}, "not a web address"},
		{"https://wiki.example.com/dashboard.action", Address{}, "does not look like"},
		{"", Address{}, "missing"},
	}
	for _, c := range cases {
		got, err := ParseURL(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("ParseURL(%q) err = %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("ParseURL(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
}

func connect(t *testing.T, f *fakeConfluence, path string) *Confluence {
	t.Helper()
	c, err := New(Config{URL: f.url(path)}, secrets.New(f.token))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTestGivesFriendlyErrors(t *testing.T) {
	f := newFake(t)
	ctx := context.Background()
	if err := connect(t, f, "/display/DOC").Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if err := connect(t, f, "/display/NOPE").Test(ctx); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing space: %v", err)
	}
	bad, _ := New(Config{URL: f.url("/display/DOC")}, secrets.New("wrong"))
	if err := bad.Test(ctx); err == nil || !strings.Contains(err.Error(), "rejected the token") {
		t.Fatalf("bad token: %v", err)
	}
	if err := connect(t, f, "/display/DOC/Missing").Test(ctx); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("missing page: %v", err)
	}
	if _, err := New(Config{URL: f.url("/display/DOC")}, secrets.String{}); err == nil {
		t.Fatal("no token accepted")
	}
}

// seedSpace builds the DOC space used by the source tests.
func seedSpace(f *fakeConfluence) (home string) {
	home = f.addPage("Home", "", `<h1>Intro</h1><p>Welcome. See <ac:link><ri:page ri:content-title="Backend Guide"/></ac:link> and `+
		`<ac:link ac:anchor="GettingStarted"><ri:page ri:content-title="Backend Guide"/><ac:plain-text-link-body><![CDATA[setup]]></ac:plain-text-link-body></ac:link>.</p>`+
		`<p><ac:image><ri:attachment ri:filename="photo.png"/></ac:image></p>`+
		`<ac:structured-macro ac:name="drawio"><ac:parameter ac:name="diagramName">arch</ac:parameter></ac:structured-macro>`+
		`<ac:structured-macro ac:name="info"><ac:rich-text-body><p>Note <ac:link><ri:page ri:space-key="HR" ri:content-title="Holidays"/></ac:link></p></ac:rich-text-body></ac:structured-macro>`)
	f.addAtt(home, "photo.png", []byte("PNG-photo"))
	f.addAtt(home, "arch", []byte("<mxfile>arch</mxfile>"))
	f.addAtt(home, "arch.png", []byte("PNG-arch-preview"))
	guide := f.addPage("Backend Guide", home, `<h2>Getting Started</h2><p>Back to <ac:link><ri:page ri:content-title="Home"/></ac:link>.</p>`)
	f.addPage("Deploy", guide, `<p>Steps</p>`)
	f.addPage("FAQ", home, `<p>Questions</p>`)
	f.addPage("Other Root", "", `<p>Elsewhere</p>`)
	return home
}

func dumpTree(tree *model.Tree) string {
	var b strings.Builder
	tree.Walk(func(p *model.Page, depth int) {
		fmt.Fprintf(&b, "%s- %s (id=%s order=%d)\n", strings.Repeat("  ", depth), p.Title, p.ID, p.Order)
		for _, line := range strings.Split(strings.TrimRight(p.Body, "\n"), "\n") {
			fmt.Fprintf(&b, "%s    | %s\n", strings.Repeat("  ", depth), line)
		}
		for _, h := range p.Attachments {
			a := tree.Attachments[h]
			fmt.Fprintf(&b, "%s    @ %s %s role=%q type=%s\n", strings.Repeat("  ", depth), a.Name, h[:8], a.Role, a.MediaType)
		}
	})
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestSnapshotWholeSpace(t *testing.T) {
	f := newFake(t)
	seedSpace(f)
	c := connect(t, f, "/display/DOC")
	var ws connectors.Warnings
	tree, err := c.Snapshot(context.Background(), &ws)
	if err != nil {
		t.Fatal(err)
	}
	out := dumpTree(tree)
	for _, w := range ws.List {
		out += fmt.Sprintf("warning %s %s\n", w.PageID, w.Code)
	}
	golden(t, "snapshot-space.txt", out)

	// Attachments are readable through their SourceRef.
	for _, a := range tree.Attachments {
		rc, err := c.OpenAttachment(context.Background(), a.SourceRef)
		if err != nil {
			t.Fatalf("open %s: %v", a.Name, err)
		}
		h, _, _ := model.HashReader(rc)
		_ = rc.Close()
		if h != a.Hash {
			t.Fatalf("%s: hash mismatch", a.Name)
		}
	}
}

func TestSnapshotPageSubtreeAndBrowse(t *testing.T) {
	f := newFake(t)
	home := seedSpace(f)
	guide := f.byTitle("Backend Guide").ID
	c := connect(t, f, "/pages/viewpage.action?pageId="+guide)
	tree, err := c.Snapshot(context.Background(), connectors.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Pages) != 2 || tree.Pages[guide] == nil || tree.Pages[guide].ParentID != "" {
		t.Fatalf("subtree = %s", dumpTree(tree))
	}
	// The link to Home points outside the subtree: plain text.
	if body := tree.Pages[guide].Body; strings.Contains(body, "tolarix://") || !strings.Contains(body, "Home") {
		t.Fatalf("body = %q", body)
	}

	nodes, err := connect(t, f, "/display/DOC").Browse(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Title != "Home" || !nodes[0].HasChildren || nodes[1].HasChildren {
		t.Fatalf("roots = %+v", nodes)
	}
	kids, err := c.Browse(context.Background(), home)
	if err != nil || len(kids) != 2 || kids[0].Title != "Backend Guide" || !kids[0].HasChildren {
		t.Fatalf("kids = %+v, %v", kids, err)
	}
}

// ---- mirror (target) tests, through the real engine ----

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type mirrorHarness struct {
	t     *testing.T
	f     *fakeConfluence
	vault string
	src   *obsidian.Vault
	conf  *Confluence
	store *enginetest.MemStore
	loc   string // mirror location; default {"parent":"Mirror Root"}
}

func (h *mirrorHarness) run(opts engine.Options) *engine.TargetReport {
	h.t.Helper()
	ctx := context.Background()
	loc := h.loc
	if loc == "" {
		loc = `{"parent":"Mirror Root"}`
	}
	s, err := h.conf.Open(ctx, json.RawMessage(loc))
	if err != nil {
		h.t.Fatal(err)
	}
	e := &engine.Engine{Mappings: h.store}
	rep, err := e.Run(ctx, engine.Request{Source: h.src, SourceName: "Vault", Options: opts,
		Targets: []engine.Target{{ID: "t1", Name: "Confluence", Session: s}}})
	if err != nil {
		h.t.Fatal(err)
	}
	tr := &rep.Targets[0]
	if tr.Error != "" {
		h.t.Fatalf("target error: %s", tr.Error)
	}
	for _, op := range tr.Ops {
		if op.Error != "" {
			h.t.Fatalf("op %s %s: %s", op.Kind, op.SourceID, op.Error)
		}
	}
	for _, a := range tr.Attachments {
		if a.Error != "" {
			h.t.Fatalf("attachment %s: %s", a.Key, a.Error)
		}
	}
	return tr
}

func kinds(tr *engine.TargetReport) string {
	var out []string
	for _, op := range tr.Ops {
		out = append(out, string(op.Kind)+":"+op.To)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func (h *mirrorHarness) parentTitle(title string) string {
	p := h.f.byTitle(title)
	if p == nil {
		h.t.Fatalf("page %q not in Confluence", title)
	}
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	if parent := h.f.pages[p.Parent]; parent != nil {
		return parent.Title
	}
	return ""
}

func TestMirrorEndToEnd(t *testing.T) {
	f := newFake(t)
	f.addPage("Mirror Root", "", "<p>Mirrors go here</p>")
	f.addPage("Unrelated", "", "<p>Not ours</p>")
	vault := t.TempDir()
	writeFile(t, vault, "Guide.md", "# Guide\n\nSee [[Setup#Install]] and [[Notes]].\n\n> [!tip] Hint\n> Be nice.\n")
	writeFile(t, vault, "Guide/Setup.md", "## Install\n\n![[shot.png]]\n\n```go\nfmt.Println(\"hi\")\n```\n")
	writeFile(t, vault, "Guide/shot.png", "PNG-shot")
	writeFile(t, vault, "Other/Notes.md", "Some notes.\n")
	src, err := obsidian.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	h := &mirrorHarness{t: t, f: f, vault: vault, src: src, conf: connect(t, f, "/display/DOC"), store: enginetest.NewMemStore()}

	// Run 1: everything is created under Mirror Root.
	tr := h.run(engine.Options{})
	if got := kinds(tr); got != "create:Guide, create:Notes, create:Other, create:Setup" {
		t.Fatalf("run 1 ops = %s", got)
	}
	if h.parentTitle("Guide") != "Mirror Root" || h.parentTitle("Setup") != "Guide" || h.parentTitle("Notes") != "Other" {
		t.Fatal("wrong hierarchy")
	}
	guide := f.byTitle("Guide").Body
	for _, want := range []string{`<ac:link ac:anchor="Install"><ri:page ri:content-title="Setup"/>`, `ac:name="tip"`, `<ri:page ri:content-title="Notes"/>`} {
		if !strings.Contains(guide, want) {
			t.Errorf("Guide body lacks %s:\n%s", want, guide)
		}
	}
	if !strings.Contains(f.byTitle("Other").Body, `ac:name="children"`) {
		t.Errorf("placeholder body = %s", f.byTitle("Other").Body)
	}
	if setup := f.byTitle("Setup"); !strings.Contains(setup.Body, `<ri:attachment ri:filename="shot.png"/>`) || f.attsOf(setup.ID)["shot.png"] != "PNG-shot" {
		t.Errorf("attachment not on Setup: %s %v", setup.Body, f.attsOf(setup.ID))
	}

	// Run 2: nothing changed, nothing written.
	f.writes()
	tr = h.run(engine.Options{})
	if got := kinds(tr); strings.Contains(got, "create") || strings.Contains(got, "update") {
		t.Fatalf("run 2 ops = %s", got)
	}
	if w := f.writes(); len(w) != 0 {
		t.Fatalf("run 2 wrote %v", w)
	}

	// Someone edits the mirror: overwritten with a warning.
	f.mu.Lock()
	p := f.pages[f.byTitleLocked("Notes").ID]
	p.Body, p.Version = "<p>hacked</p>", p.Version+1
	f.mu.Unlock()
	tr = h.run(engine.Options{})
	if got := kinds(tr); !strings.Contains(got, "update:Notes") {
		t.Fatalf("run 3 ops = %s", got)
	}
	if !hasWarning(tr, "mirror-edited") || strings.Contains(f.byTitle("Notes").Body, "hacked") {
		t.Fatal("edit on the mirror not overwritten")
	}

	// Move Setup under Other: same title, new parent.
	if err := os.Rename(filepath.Join(vault, "Guide", "Setup.md"), filepath.Join(vault, "Other", "Setup.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(vault, "Guide", "shot.png"), filepath.Join(vault, "Other", "shot.png")); err != nil {
		t.Fatal(err)
	}
	h.run(engine.Options{})
	if h.parentTitle("Setup") != "Other" {
		t.Fatalf("Setup parent = %s", h.parentTitle("Setup"))
	}

	// Delete Notes from the vault: archived (default policy).
	if err := os.Remove(filepath.Join(vault, "Other", "Notes.md")); err != nil {
		t.Fatal(err)
	}
	tr = h.run(engine.Options{})
	if got := kinds(tr); !strings.Contains(got, "archive:") {
		t.Fatalf("run 5 ops = %s", got)
	}
	if f.byTitle("Notes (archived)") == nil || h.parentTitle("Notes (archived)") != "Archived – Mirror Root" || h.parentTitle("Archived – Mirror Root") != "Mirror Root" {
		t.Fatal("not archived where expected")
	}

	// Bring it back: restored from the archive under its old title.
	writeFile(t, vault, "Other/Notes.md", "Some notes.\n")
	tr = h.run(engine.Options{})
	if got := kinds(tr); !strings.Contains(got, "move:Notes") {
		t.Fatalf("run 6 ops = %s", got)
	}
	if h.parentTitle("Notes") != "Other" {
		t.Fatal("not restored")
	}

	// The unrelated page was never touched.
	if u := f.byTitle("Unrelated"); u.Version != 1 {
		t.Fatal("unrelated page modified")
	}
}

func TestMirrorTitleClashesAndConflicts(t *testing.T) {
	f := newFake(t)
	f.addPage("Mirror Root", "", "")
	f.addPage("Setup", "", "<p>someone else's page</p>")
	vault := t.TempDir()
	writeFile(t, vault, "A/Overview.md", "a")
	writeFile(t, vault, "B/Overview.md", "b")
	writeFile(t, vault, "Setup.md", "mine")
	src, _ := obsidian.Open(vault)
	h := &mirrorHarness{t: t, f: f, vault: vault, src: src, conf: connect(t, f, "/display/DOC"), store: enginetest.NewMemStore()}
	s, err := h.conf.Open(context.Background(), json.RawMessage(`{"parent":"Mirror Root"}`))
	if err != nil {
		t.Fatal(err)
	}
	e := &engine.Engine{Mappings: h.store}
	rep, err := e.Run(context.Background(), engine.Request{Source: src, Targets: []engine.Target{{ID: "t", Name: "c", Session: s}}})
	if err != nil {
		t.Fatal(err)
	}
	tr := rep.Targets[0]
	if f.byTitle("Overview") == nil || f.byTitle("Overview (B)") == nil {
		t.Fatalf("duplicate titles not disambiguated: %s", kinds(&tr))
	}
	if !hasWarning(&tr, "title-renamed") {
		t.Fatal("no title-renamed warning")
	}
	conflict := false
	for _, op := range tr.Ops {
		if op.Kind == engine.OpConflict && op.To == "Setup" {
			conflict = true
		}
	}
	if !conflict {
		t.Fatalf("an existing page with the same title must be a conflict: %s", kinds(&tr))
	}
}

func hasWarning(tr *engine.TargetReport, code string) bool {
	for _, w := range tr.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func (f *fakeConfluence) byTitleLocked(title string) *fakePage {
	for _, p := range f.pages {
		if p.Title == title {
			return p
		}
	}
	return nil
}

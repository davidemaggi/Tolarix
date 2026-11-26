package azuredevops

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
		err  bool
	}{
		{"https://dev.azure.com/org/Docs%20Project/_wiki/wikis/Docs.wiki", Address{BaseURL: "https://dev.azure.com/org", Project: "Docs Project", Wiki: "Docs.wiki"}, false},
		{"https://dev.azure.com/org/Docs%20Project/_wiki/wikis/Docs.wiki/12/Home", Address{BaseURL: "https://dev.azure.com/org", Project: "Docs Project", Wiki: "Docs.wiki"}, false},
		{"https://dev.azure.com/org/P/_git/repo?path=/docs/wiki&version=GBrelease/1", Address{BaseURL: "https://dev.azure.com/org", Project: "P", Repo: "repo", Folder: "docs/wiki", Branch: "release/1"}, false},
		{"https://dev.azure.com/org/P/_git/repo?path=/docs/Home.md", Address{BaseURL: "https://dev.azure.com/org", Project: "P", Repo: "repo", Folder: "docs"}, false},
		{"https://dev.azure.com/org/_git/repo", Address{BaseURL: "https://dev.azure.com/org", Project: "repo", Repo: "repo"}, false},
		{"https://org.visualstudio.com/P/_git/repo", Address{BaseURL: "https://org.visualstudio.com", Project: "P", Repo: "repo"}, false},
		{"https://org.visualstudio.com/DefaultCollection/P/_git/repo", Address{BaseURL: "https://org.visualstudio.com/DefaultCollection", Project: "P", Repo: "repo"}, false},
		{"https://tfs.example.com/tfs/Main%20Collection/P/_git/repo", Address{BaseURL: "https://tfs.example.com/tfs/Main%20Collection", Project: "P", Repo: "repo"}, false},
		{"https://dev.azure.com/org/P", Address{}, true},
		{"https://dev.azure.com/org/P/_wiki", Address{}, true},
		{"dev.azure.com/org/P/_git/repo", Address{}, true},
		{"", Address{}, true},
	}
	for _, c := range cases {
		got, err := ParseURL(c.in)
		if (err != nil) != c.err {
			t.Errorf("%q: err = %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q:\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

func TestParseProjectURL(t *testing.T) {
	base := "https://dev.azure.com/org"
	for in, want := range map[string]Address{
		base + "/P/_wiki":                         {BaseURL: base, Project: "P"},
		base + "/P/_wiki/wikis/P.wiki":            {BaseURL: base, Project: "P", Wiki: "P.wiki"},
		base + "/P/_wiki/wikis/P.wiki/3/Home?x=1": {BaseURL: base, Project: "P", Wiki: "P.wiki"},
		"https://org.visualstudio.com/P/_wiki/":   {BaseURL: "https://org.visualstudio.com", Project: "P"},
	} {
		got, err := ParseProjectURL(in)
		if err != nil || got != want {
			t.Errorf("%q: got %+v, %v", in, got, err)
		}
	}
	for _, in := range []string{base + "/P/_git/repo", base + "/_wiki", base + "/P"} {
		if _, err := ParseProjectURL(in); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}

func TestNames(t *testing.T) {
	for _, title := range []string{"Getting Started", "Q&A: what? why*", "50% off - now", "a/b\\c", ".hidden.", "C# <tips> | \"quotes\"", "CON", "nul"} {
		enc := EncodeName(title)
		if strings.ContainsAny(enc, " /\\:*?<>|\"#") {
			t.Errorf("EncodeName(%q) = %q keeps an unsafe character", title, enc)
		}
		if got := DecodeName(enc); got != title {
			t.Errorf("round trip %q -> %q -> %q", title, enc, got)
		}
	}
	if got := EncodeName("Getting Started - Part 1"); got != "Getting-Started-%2D-Part-1" {
		t.Errorf("EncodeName = %q", got)
	}
	if got := EncodeName("Aux"); got != "%41ux" {
		t.Errorf("EncodeName(Aux) = %q, want a name Windows accepts", got)
	}
	if got := slug("Install & Run (v2)!"); got != "install-%26-run-%28v2%29%21" {
		t.Errorf("slug = %q", got)
	}
	hs := []string{"Install & Run", "Other"}
	for _, a := range []string{"install-&-run", "install--run", "INSTALL-&-RUN"} {
		if h, ok := headingForSlug(hs, a); !ok || h != "Install & Run" {
			t.Errorf("headingForSlug(%q) = %q, %v", a, h, ok)
		}
	}
	names := []string{"b", "C", "a", "Z"}
	sortByOrder(names, []string{"Z", "missing", "b"})
	if strings.Join(names, ",") != "Z,b,a,C" {
		t.Errorf("sortByOrder = %v", names)
	}
}

func TestBlocks(t *testing.T) {
	canonical := "intro\n\n" + model.TOCMarker + "\n\n```mermaid\ngraph TD\n  A-->B\n```\n\n> [!warning] Careful\n> text\n\n```md\n" + model.TOCMarker + "\n```\n"
	wiki := encodeBlocks(canonical)
	want := "intro\n\n[[_TOC_]]\n\n::: mermaid\ngraph TD\n  A-->B\n:::\n\n> **Warning:** Careful\n> text\n\n```md\n" + model.TOCMarker + "\n```\n"
	if wiki != want {
		t.Fatalf("encode:\n%s\nwant:\n%s", wiki, want)
	}
	var warned []string
	back := decodeBlocks(wiki+"\n[[_TOSP_]]\n", func(code, _ string) { warned = append(warned, code) })
	if !strings.Contains(back, "```mermaid\ngraph TD\n  A-->B\n```") || !strings.Contains(back, "\n"+model.TOCMarker+"\n") || strings.Contains(back, "_TOSP_") {
		t.Fatalf("decode:\n%s", back)
	}
	if len(warned) != 1 || warned[0] != "subpages-list" {
		t.Fatalf("warnings = %v", warned)
	}
	// An unclosed fence is kept to the end.
	if got := encodeBlocks("```mermaid\ngraph"); got != "::: mermaid\ngraph\n:::" {
		t.Fatalf("unclosed = %q", got)
	}
	if got := decodeImageSizes("![a](/x.png =300x) ![b](y.png =20x30) ![c](z.png =x40)"); got != `![a](/x.png "width=300") ![b](y.png "width=20x30") ![c](z.png)` {
		t.Fatalf("sizes = %q", got)
	}
}

func connectProject(t *testing.T, f *fakeADO, rest string) *Repo {
	t.Helper()
	r, err := NewProject(Config{URL: f.url(rest)}, secrets.New(f.token))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func connect(t *testing.T, f *fakeADO, rest string) *Repo {
	t.Helper()
	r, err := New(Config{URL: f.url(rest)}, secrets.New(f.token))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func addWiki(f *fakeADO) {
	w := wikiInfo{Name: "Docs.wiki", Type: "codeWiki", RepositoryID: fakeRepoID, MappedPath: "/wiki"}
	w.Versions = append(w.Versions, struct {
		Version string `json:"version"`
	}{"main"})
	f.wikis["Docs.wiki"] = w
	// The fake has one repository: the project wiki lives on its wikiMaster branch.
	pw := wikiInfo{Name: "Project.wiki", Type: "projectWiki", RepositoryID: fakeRepoID, MappedPath: "/"}
	pw.Versions = append(pw.Versions, struct {
		Version string `json:"version"`
	}{projectBranch})
	f.wikis["Project.wiki"] = pw
}

func seedWiki(f *fakeADO) {
	addWiki(f)
	f.commitFiles("main", map[string]string{
		"/README.md":                       "outside the wiki",
		"/wiki/.order":                     "Home\nGuide\n",
		"/wiki/Home.md":                    "---\nowner: docs\n---\n[[_TOC_]]\n\n# Welcome\n\nSee [setup](/Guide/Getting-Started#install-%26-run) and [the guide](Guide).\n\n[[_TOSP_]]\n",
		"/wiki/Guide.md":                   "## Guide\n\n::: mermaid\ngraph TD\n  A-->B\n:::\n\n![diagram](/.attachments/diagram-1.png =300x)\n\n[up](../outside.md) [nope](/Missing-Page)\n",
		"/wiki/Guide/.order":               "Getting-Started\nFAQ\n",
		"/wiki/Guide/Getting-Started.md":   "## Install & Run\n\nBack to [home](/Home) or [faq](./FAQ.md#top).\n\n[local](#install-%26-run)\n",
		"/wiki/Guide/FAQ.md":               "## Top\n\nQ&A %2D here.\n",
		"/wiki/Ops/Runbooks/Restart.md":    "Restart [guide](../../Guide.md).\n",
		"/wiki/.attachments/diagram-1.png": "PNG-diagram",
	})
}

func dumpTree(tree *model.Tree) string {
	var b strings.Builder
	tree.Walk(func(p *model.Page, depth int) {
		ind := strings.Repeat("  ", depth)
		fmt.Fprintf(&b, "%s- %s (id=%s order=%d placeholder=%v)\n", ind, p.Title, p.ID, p.Order, p.Placeholder)
		for _, m := range p.Meta {
			fmt.Fprintf(&b, "%s    meta %s=%v\n", ind, m.Key, m.Value)
		}
		for _, line := range strings.Split(strings.TrimRight(p.Body, "\n"), "\n") {
			fmt.Fprintf(&b, "%s    | %s\n", ind, line)
		}
		for _, h := range p.Attachments {
			a := tree.Attachments[h]
			fmt.Fprintf(&b, "%s    @ %s %s type=%s\n", ind, a.Name, h[:8], a.MediaType)
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

func TestTestGivesFriendlyErrors(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	ctx := context.Background()
	if err := connect(t, f, "/_wiki/wikis/Docs.wiki").Test(ctx); err != nil {
		t.Fatal(err)
	}
	bad, _ := New(Config{URL: f.url("/_git/" + fakeRepo)}, secrets.New("wrong"))
	if err := bad.Test(ctx); err == nil || !strings.Contains(err.Error(), "token was rejected") {
		t.Fatalf("bad token: %v", err)
	}
	if err := connect(t, f, "/_git/nope").Test(ctx); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing repo: %v", err)
	}
	if err := connect(t, f, "/_wiki/wikis/Project.wiki").Test(ctx); err == nil || !strings.Contains(err.Error(), `"Azure DevOps Project wiki"`) {
		t.Fatalf("project wiki on a code wiki connection: %v", err)
	}
	if err := connectProject(t, f, "/_wiki/wikis/Docs.wiki").Test(ctx); err == nil || !strings.Contains(err.Error(), `"Azure DevOps Code wiki"`) {
		t.Fatalf("code wiki on a project wiki connection: %v", err)
	}
	if _, err := NewProject(Config{URL: f.url("/_git/" + fakeRepo)}, secrets.New(f.token)); err == nil || !strings.Contains(err.Error(), "repository address") {
		t.Fatalf("repository address on a project wiki connection: %v", err)
	}
	delete(f.wikis, "Project.wiki")
	if err := connectProject(t, f, "/_wiki").Test(ctx); err == nil || !strings.Contains(err.Error(), "no project wiki yet") {
		t.Fatalf("project without a wiki: %v", err)
	}
}

// TestProjectWiki reads and mirrors a project wiki found from the bare
// …/_wiki address: the wiki root is the repository root on wikiMaster.
func TestProjectWiki(t *testing.T) {
	f := newFake(t)
	addWiki(f)
	f.commitFiles(projectBranch, map[string]string{
		"/.order":                "Home\nTeam\n",
		"/Home.md":               "# Home\n\nSee [the team](/Team).\n",
		"/Team.md":               "Team page ![logo](/.attachments/logo.png)\n",
		"/.attachments/logo.png": "PNG-logo",
	})
	r := connectProject(t, f, "/_wiki")
	tree, err := r.Snapshot(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := dumpTree(tree); !strings.Contains(got, "- Home (id=Home order=0") || !strings.Contains(got, "- Team (id=Team order=1") || !strings.Contains(got, "@ logo.png") {
		t.Fatalf("snapshot:\n%s", got)
	}

	h := newHarness(t, f)
	h.repo = r
	h.run(engine.Options{})
	if got := f.file(projectBranch, "/Mirror/Guide.md"); !strings.Contains(got, "[[_TOC_]]") {
		t.Fatalf("Mirror/Guide.md on %s = %q", projectBranch, got)
	}
	if f.file("main", "/wiki/Mirror/Guide.md") != "" {
		t.Fatal("the code wiki branch must not change")
	}
}

func TestSnapshotWiki(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	r := connect(t, f, "/_wiki/wikis/Docs.wiki")
	var ws connectors.Warnings
	tree, err := r.Snapshot(context.Background(), &ws)
	if err != nil {
		t.Fatal(err)
	}
	out := dumpTree(tree)
	var warns []string
	for _, w := range ws.List {
		warns = append(warns, fmt.Sprintf("warning %s %s\n", w.PageID, w.Code))
	}
	sort.Strings(warns)
	golden(t, "snapshot-wiki.txt", out+strings.Join(warns, ""))

	for _, a := range tree.Attachments {
		rc, err := r.OpenAttachment(context.Background(), a.SourceRef)
		if err != nil {
			t.Fatal(err)
		}
		h, _, _ := model.HashReader(rc)
		_ = rc.Close()
		if h != a.Hash {
			t.Fatalf("%s: hash mismatch", a.Name)
		}
	}

	// The same wiki addressed as a repository folder.
	tree2, err := connect(t, f, "/_git/"+fakeRepo+"?path=/wiki").Snapshot(context.Background(), connectors.Discard)
	if err != nil || len(tree2.Pages) != len(tree.Pages) {
		t.Fatalf("repo address: %v, %d pages", err, len(tree2.Pages))
	}
	if _, err := connect(t, f, "/_git/"+fakeRepo+"?path=/nope").Snapshot(context.Background(), connectors.Discard); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing folder: %v", err)
	}
}

func TestBrowse(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	r := connect(t, f, "/_wiki/wikis/Docs.wiki")
	nodes, err := r.Browse(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range nodes {
		got = append(got, fmt.Sprintf("%s:%s:%v", n.ID, n.Title, n.HasChildren))
	}
	if strings.Join(got, ",") != "Home:Home:false,Guide:Guide:true,Ops:Ops:true" {
		t.Fatalf("roots = %v", got)
	}
	kids, err := r.Browse(context.Background(), "Guide")
	if err != nil || len(kids) != 2 || kids[0].Title != "Getting Started" || kids[0].ID != "Guide/Getting-Started" {
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

type harness struct {
	t        *testing.T
	f        *fakeADO
	vault    string
	src      *obsidian.Vault
	repo     *Repo
	store    *enginetest.MemStore
	location string // flow target location JSON (default: folder Mirror)
}

func newHarness(t *testing.T, f *fakeADO) *harness {
	vault := t.TempDir()
	writeFile(t, vault, "Guide.md", "# Guide\n\n<!-- tolarix:toc -->\n\nSee [[Getting Started#Install]] and [[Notes]].\n\n> [!tip] Hint\n> Be nice.\n\n```mermaid\ngraph TD\n```\n")
	writeFile(t, vault, "Guide/Getting Started.md", "## Install\n\n![[shot.png|300]]\n")
	writeFile(t, vault, "Guide/shot.png", "PNG-shot")
	writeFile(t, vault, "Other/Notes.md", "Some notes.\n")
	src, err := obsidian.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, f: f, vault: vault, src: src, repo: connect(t, f, "/_wiki/wikis/Docs.wiki"), store: enginetest.NewMemStore()}
}

func (h *harness) runRaw(opts engine.Options) *engine.TargetReport {
	h.t.Helper()
	ctx := context.Background()
	loc := h.location
	if loc == "" {
		loc = `{"folder":"Mirror"}`
	}
	s, err := h.repo.Open(ctx, json.RawMessage(loc))
	if err != nil {
		h.t.Fatal(err)
	}
	e := &engine.Engine{Mappings: h.store}
	rep, err := e.Run(ctx, engine.Request{Source: h.src, SourceName: "Vault", Options: opts,
		Targets: []engine.Target{{ID: "t1", Name: "ADO", Session: s}}})
	if err != nil {
		h.t.Fatal(err)
	}
	return &rep.Targets[0]
}

func (h *harness) run(opts engine.Options) *engine.TargetReport {
	h.t.Helper()
	tr := h.runRaw(opts)
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

func hasWarning(tr *engine.TargetReport, code string) bool {
	for _, w := range tr.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func changeList(pushes [][]pushChange) string {
	var out []string
	for _, p := range pushes {
		var cs []string
		for _, c := range p {
			s := c.ChangeType + " " + c.Item.Path
			if c.SourceServerItem != "" {
				s += " <- " + c.SourceServerItem
			}
			cs = append(cs, s)
		}
		out = append(out, strings.Join(cs, "\n"))
	}
	return strings.Join(out, "\n--\n")
}

func TestMirrorEndToEnd(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	h := newHarness(t, f)

	// Run 1: one push with every page, the attachment and the .order files.
	tr := h.run(engine.Options{})
	if got := kinds(tr); got != "create:Mirror/Guide.md, create:Mirror/Guide/Getting-Started.md, create:Mirror/Other/, create:Mirror/Other/Notes.md" {
		t.Fatalf("run 1 ops = %s", got)
	}
	pushes := f.takePushes()
	if len(pushes) != 1 {
		t.Fatalf("run 1 pushes = %d", len(pushes))
	}
	golden(t, "mirror-push1.txt", changeList(pushes))
	golden(t, "mirror-guide.md", f.file("main", "/wiki/Mirror/Guide.md"))
	golden(t, "mirror-getting-started.md", f.file("main", "/wiki/Mirror/Guide/Getting-Started.md"))
	if f.file("main", "/wiki/Mirror/.attachments/shot.png") != "PNG-shot" {
		t.Fatal("attachment not pushed")
	}
	if got := f.file("main", "/wiki/Mirror/.order"); got != "Guide\nOther" {
		t.Fatalf(".order = %q", got)
	}
	if f.file("main", "/wiki/Home.md") == "" || f.file("main", "/README.md") == "" {
		t.Fatal("files outside the target were touched")
	}

	// The mirror reads back as a wiki with the same pages.
	back, err := connect(t, f, "/_wiki/wikis/Docs.wiki").Snapshot(context.Background(), connectors.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if p := back.Pages["Mirror/Guide/Getting-Started"]; p == nil || p.Title != "Getting Started" || len(p.Attachments) != 1 ||
		!strings.Contains(back.Pages["Mirror/Guide"].Body, model.PageLink("Mirror/Guide/Getting-Started", "Install")) {
		t.Fatalf("read back: %s", dumpTree(back))
	}

	// Run 2: nothing changed, nothing pushed.
	tr = h.run(engine.Options{})
	if got := kinds(tr); strings.Contains(got, "create") || strings.Contains(got, "update") {
		t.Fatalf("run 2 ops = %s", got)
	}
	if p := f.takePushes(); len(p) != 0 {
		t.Fatalf("run 2 pushed:\n%s", changeList(p))
	}

	// Someone edits the mirror in the wiki: overwritten with a warning.
	f.commitFiles("main", map[string]string{"/wiki/Mirror/Other/Notes.md": "hacked"})
	tr = h.run(engine.Options{})
	if got := kinds(tr); !strings.Contains(got, "update:Mirror/Other/Notes.md") || !hasWarning(tr, "mirror-edited") {
		t.Fatalf("run 3 ops = %s", got)
	}
	if got := f.file("main", "/wiki/Mirror/Other/Notes.md"); got != "Some notes.\n" {
		t.Fatalf("Notes = %q", got)
	}
	f.takePushes()

	// Move Getting Started under Other: delete + add, links follow.
	if err := os.Rename(filepath.Join(h.vault, "Guide", "Getting Started.md"), filepath.Join(h.vault, "Other", "Getting Started.md")); err != nil {
		t.Fatal(err)
	}
	h.run(engine.Options{})
	if f.file("main", "/wiki/Mirror/Guide/Getting-Started.md") != "" || f.file("main", "/wiki/Mirror/Other/Getting-Started.md") == "" {
		t.Fatalf("not moved: %v", f.paths("main", "/wiki/Mirror"))
	}
	if !strings.Contains(f.file("main", "/wiki/Mirror/Guide.md"), "(/Mirror/Other/Getting-Started#install)") {
		t.Fatalf("link not updated:\n%s", f.file("main", "/wiki/Mirror/Guide.md"))
	}
	if f.file("main", "/wiki/Mirror/Guide/.order") != "" {
		t.Fatal("stale .order left in an empty folder")
	}
	f.takePushes()

	// Delete Notes from the vault: archived with a git rename.
	if err := os.Remove(filepath.Join(h.vault, "Other", "Notes.md")); err != nil {
		t.Fatal(err)
	}
	tr = h.run(engine.Options{})
	if got := kinds(tr); !strings.Contains(got, "archive:") {
		t.Fatalf("run 5 ops = %s", got)
	}
	if got := changeList(f.takePushes()); !strings.Contains(got, "rename /wiki/Mirror/Archived/Other/Notes.md <- /wiki/Mirror/Other/Notes.md") {
		t.Fatalf("archive push:\n%s", got)
	}

	// Bring it back: restored from the archive.
	writeFile(t, h.vault, "Other/Notes.md", "Some notes.\n")
	tr = h.run(engine.Options{})
	if got := kinds(tr); !strings.Contains(got, "move:Mirror/Other/Notes.md") {
		t.Fatalf("run 6 ops = %s", got)
	}
	if f.file("main", "/wiki/Mirror/Other/Notes.md") == "" || f.file("main", "/wiki/Mirror/Archived/Other/Notes.md") != "" {
		t.Fatalf("not restored: %v", f.paths("main", "/wiki/Mirror"))
	}
}

func TestMirrorPushRejectedSavesNothing(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	h := newHarness(t, f)
	f.rejectPush = "TF402455: Pushes to this branch are not permitted; you must use a pull request to update this branch."
	tr := h.runRaw(engine.Options{})
	if !strings.Contains(tr.Error, "branch policies") {
		t.Fatalf("error = %q", tr.Error)
	}
	for _, op := range tr.Ops {
		if !strings.Contains(op.Error, "not saved") {
			t.Fatalf("op %s %s error = %q", op.Kind, op.To, op.Error)
		}
	}
	if n := h.store.Len(); n != 0 {
		t.Fatalf("%d mappings saved after a rejected push", n)
	}
	// The next run creates everything again.
	tr = h.run(engine.Options{})
	if got := kinds(tr); !strings.Contains(got, "create:Mirror/Guide.md") {
		t.Fatalf("retry ops = %s", got)
	}
}

func TestMirrorPushConflict(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	h := newHarness(t, f)
	f.beforePush = func() { f.commitFiles("main", map[string]string{"/wiki/Home.md": "changed meanwhile"}) }
	tr := h.runRaw(engine.Options{})
	if !strings.Contains(tr.Error, "run the flow again") {
		t.Fatalf("error = %q", tr.Error)
	}
	if f.file("main", "/wiki/Mirror/Guide.md") != "" {
		t.Fatal("conflicting push applied")
	}
	h.run(engine.Options{})
	if f.file("main", "/wiki/Mirror/Guide.md") == "" {
		t.Fatal("retry did not push")
	}
}

func TestMirrorIntoEmptyRepository(t *testing.T) {
	f := newFake(t)
	h := newHarness(t, f)
	h.repo = connect(t, f, "/_git/"+fakeRepo)
	h.run(engine.Options{})
	if f.file("main", "/Mirror/Guide.md") == "" {
		t.Fatalf("branch not created: %v", f.paths("main", "/"))
	}
	if p := f.takePushes(); len(p) != 1 {
		t.Fatalf("pushes = %d", len(p))
	}
}

func TestMirrorSplitsLargePushes(t *testing.T) {
	old := maxPushBytes
	maxPushBytes = 1000 // room for a few small files per push
	t.Cleanup(func() { maxPushBytes = old })
	f := newFake(t)
	seedWiki(f)
	h := newHarness(t, f)
	h.run(engine.Options{})
	if p := f.takePushes(); len(p) < 2 {
		t.Fatalf("pushes = %d, want several", len(p))
	}
	if f.file("main", "/wiki/Mirror/Other/Notes.md") == "" || f.file("main", "/wiki/Mirror/.attachments/shot.png") == "" {
		t.Fatalf("missing files: %v", f.paths("main", "/wiki/Mirror"))
	}
	// Nothing left to do afterwards.
	tr := h.run(engine.Options{})
	if got := kinds(tr); strings.Contains(got, "create") || strings.Contains(got, "update") {
		t.Fatalf("rerun ops = %s", got)
	}
}

func TestMirrorToAnotherBranch(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	h := newHarness(t, f)
	h.location = `{"folder":"Mirror","branch":"wiki/mirror"}`
	base := f.branches["main"]

	// A dry-run creates nothing.
	s, err := h.repo.Open(context.Background(), json.RawMessage(h.location))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&engine.Engine{Mappings: h.store}).Run(context.Background(), engine.Request{Source: h.src, DryRun: true,
		Targets: []engine.Target{{ID: "t1", Name: "ADO", Session: s}}}); err != nil {
		t.Fatal(err)
	}
	if len(f.refsCreated) != 0 || len(f.takePushes()) != 0 {
		t.Fatalf("dry-run wrote: %v", f.refsCreated)
	}

	// The first run creates the branch from main, then pushes on top of it.
	h.run(engine.Options{})
	if strings.Join(f.refsCreated, ",") != "wiki/mirror" {
		t.Fatalf("refs created = %v", f.refsCreated)
	}
	if f.file("wiki/mirror", "/wiki/Mirror/Guide.md") == "" || f.file("wiki/mirror", "/wiki/Home.md") == "" {
		t.Fatalf("branch content: %v", f.paths("wiki/mirror", "/"))
	}
	if f.branches["main"] != base || f.file("main", "/wiki/Mirror/Guide.md") != "" {
		t.Fatal("main was changed")
	}

	// Later runs push to the existing branch.
	writeFile(t, h.vault, "Other/Notes.md", "Changed.\n")
	h.run(engine.Options{})
	if len(f.refsCreated) != 1 || f.file("wiki/mirror", "/wiki/Mirror/Other/Notes.md") != "Changed.\n" {
		t.Fatalf("second run: refs %v, notes %q", f.refsCreated, f.file("wiki/mirror", "/wiki/Mirror/Other/Notes.md"))
	}
}

func TestMirrorBranchCreatedMeanwhile(t *testing.T) {
	f := newFake(t)
	seedWiki(f)
	h := newHarness(t, f)
	h.location = `{"folder":"Mirror","branch":"wiki/mirror"}`
	// Someone creates the branch between Tolarix reading and writing.
	ctx := context.Background()
	s, err := h.repo.Open(ctx, json.RawMessage(h.location))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	f.commitFiles("wiki/mirror", map[string]string{"/x.md": "x"})
	if err := s.PutAttachment(ctx, "Mirror/.attachments/a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, true); err == nil || !strings.Contains(err.Error(), "run the flow again") {
		t.Fatalf("finish = %v", err)
	}
}

func TestBranchLocation(t *testing.T) {
	for in, want := range map[string]string{"wiki/mirror": "wiki/mirror", " refs/heads/docs ": "docs", "release-1.0": "release-1.0"} {
		if got, err := CleanBranch(in); err != nil || got != want {
			t.Errorf("CleanBranch(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"a b", "a..b", "/a", "a/", "a.lock", "-a", "a:b", "a/.b", "a@{1}", "a\\b", "@"} {
		if _, err := CleanBranch(in); err == nil {
			t.Errorf("CleanBranch(%q) accepted", in)
		}
	}
	f := newFake(t)
	seedWiki(f)
	ctx := context.Background()
	if _, err := connect(t, f, "/_wiki/wikis/Docs.wiki").Open(ctx, json.RawMessage(`{"branch":"bad name"}`)); err == nil {
		t.Fatal("bad branch accepted")
	}
	spec, _ := connectors.Lookup(Kind)
	pspec, _ := connectors.Lookup(ProjectKind)
	if len(spec.LocationFields) != 2 || len(pspec.LocationFields) != 1 {
		t.Fatalf("location fields: %v / %v", spec.LocationFields, pspec.LocationFields)
	}
}

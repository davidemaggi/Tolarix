package folder

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/connectors/filetree"
	"github.com/davidemaggi/tolarix/core/internal/connectors/obsidian"
	"github.com/davidemaggi/tolarix/core/internal/engine"
	"github.com/davidemaggi/tolarix/core/internal/engine/enginetest"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

var update = flag.Bool("update", false, "rewrite golden files")

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

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return string(b)
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// dump lists every file under root with its content, skipping .git.
func dump(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(paths)
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("== " + p + " ==\n" + readFile(t, root, p))
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

type harness struct {
	t      *testing.T
	vault  string
	out    string
	src    *obsidian.Vault
	target connectors.Target
	store  *enginetest.MemStore
}

func newHarness(t *testing.T, kind connectors.Kind) *harness {
	t.Helper()
	vault := t.TempDir()
	writeFile(t, vault, "Guide.md", "# Guide\n\n<!-- tolarix:toc -->\n\nSee [[Getting Started#Install]] and [[Notes]].\n\n```mermaid\ngraph TD\n```\n")
	writeFile(t, vault, "Guide/Getting Started.md", "## Install\n\n![[shot.png|300]]\n\nBack to [[Guide]], or [[#Install|here]].\n")
	writeFile(t, vault, "Guide/shot.png", "PNG-shot")
	writeFile(t, vault, "Other/Notes.md", "---\ntags: [a]\n---\nSome notes.\n")
	writeFile(t, vault, "What is it?.md", "Questions.\n")
	src, err := obsidian.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	// Files Tolarix does not manage stay untouched.
	writeFile(t, out, "Mirror/README.md", "mine\n")
	writeFile(t, out, ".git/config", "[core]\n")
	writeFile(t, out, "Mirror/.git/HEAD", "ref\n")

	raw, _ := json.Marshal(Config{Path: out})
	c, err := connectors.Build(kind, raw, secrets.String{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, vault: vault, out: out, src: src, target: c.(connectors.Target), store: enginetest.NewMemStore()}
}

func (h *harness) run() *engine.TargetReport {
	h.t.Helper()
	ctx := context.Background()
	s, err := h.target.Open(ctx, json.RawMessage(`{"folder":"Mirror"}`))
	if err != nil {
		h.t.Fatal(err)
	}
	e := &engine.Engine{Mappings: h.store}
	rep, err := e.Run(ctx, engine.Request{Source: h.src, SourceName: "Vault",
		Targets: []engine.Target{{ID: "t1", Name: "Folder", Session: s}}})
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

func TestExportEndToEnd(t *testing.T) {
	cases := []struct {
		kind            connectors.Kind
		golden          string
		notes, archived string // the Notes page, and where it is archived
		gone            string // folder removed once Notes is archived
		renamed         bool   // "What is it?" cannot keep its name
	}{
		{AzureDevOpsKind, "azure-devops.txt", "Mirror/Other/Notes.md", "Mirror/Archived/Other/Notes.md", "Mirror/Other", false},
		{GitHubKind, "github-wiki.txt", "Mirror/Other/Notes.md", "Mirror/Archived/Other/Notes.md", "Mirror/Other", true},
		{MarkdownKind, "markdown.txt", "Mirror/Other/Notes.md", "Mirror/Archived/Other/Notes.md", "Mirror/Other", true},
	}
	for _, c := range cases {
		t.Run(string(c.kind), func(t *testing.T) {
			h := newHarness(t, c.kind)

			tr := h.run()
			if hasWarning(tr, "renamed") != c.renamed {
				t.Errorf("renamed warning = %v, want %v", !c.renamed, c.renamed)
			}
			golden(t, c.golden, dump(t, h.out))
			notes := readFile(t, h.out, c.notes)

			// Nothing changed: nothing written.
			if got := kinds(h.run()); strings.Contains(got, "create") || strings.Contains(got, "update") {
				t.Fatalf("run 2 ops = %s", got)
			}

			// Someone edits the export: overwritten, with a warning.
			writeFile(t, h.out, c.notes, "hacked")
			tr = h.run()
			if got := kinds(tr); !strings.Contains(got, "update:"+c.notes) || !hasWarning(tr, "mirror-edited") {
				t.Fatalf("run 3 ops = %s", got)
			}
			if got := readFile(t, h.out, c.notes); got != notes {
				t.Fatalf("not restored: %q", got)
			}

			// Deleted in the Master: archived, and the emptied folder goes.
			if err := os.Remove(filepath.Join(h.vault, "Other", "Notes.md")); err != nil {
				t.Fatal(err)
			}
			if got := kinds(h.run()); !strings.Contains(got, "archive:") {
				t.Fatalf("run 4 ops = %s", got)
			}
			if exists(h.out, c.notes) || readFile(t, h.out, c.archived) != notes || exists(h.out, c.gone) {
				t.Fatalf("not archived:\n%s", dump(t, h.out))
			}

			if readFile(t, h.out, "Mirror/README.md") != "mine\n" || !exists(h.out, ".git/config") || !exists(h.out, "Mirror/.git/HEAD") {
				t.Fatal("files Tolarix does not manage were touched")
			}
		})
	}
}

func TestGitHubKeepsSomeoneElsesSidebar(t *testing.T) {
	h := newHarness(t, GitHubKind)
	writeFile(t, h.out, "Mirror/_Sidebar.md", "my sidebar\n")
	h.run()
	if got := readFile(t, h.out, "Mirror/_Sidebar.md"); got != "my sidebar\n" {
		t.Fatalf("sidebar overwritten: %q", got)
	}
}

func TestMarkdownPageGainsChildren(t *testing.T) {
	h := newHarness(t, MarkdownKind)
	h.run()
	writeFile(t, h.vault, "What is it?/Answer.md", "42\n")
	if got := kinds(h.run()); !strings.Contains(got, "move:Mirror/What is it-/index.md") {
		t.Fatalf("ops = %s", got)
	}
	if exists(h.out, "Mirror/What is it-.md") || readFile(t, h.out, "Mirror/What is it-/Answer.md") == "" {
		t.Fatalf("not moved:\n%s", dump(t, h.out))
	}
}

func TestTestAndOpenErrors(t *testing.T) {
	ctx := context.Background()
	missing, err := New(Config{Path: filepath.Join(t.TempDir(), "nope")}, func() filetree.Format { return &Markdown{} })
	if err != nil {
		t.Fatal(err)
	}
	if err := missing.Test(ctx); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing folder: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, filepath.Dir(file), "file", "x")
	f, _ := New(Config{Path: file}, func() filetree.Format { return &Markdown{} })
	if err := f.Test(ctx); err == nil || !strings.Contains(err.Error(), "not a folder") {
		t.Fatalf("file: %v", err)
	}
	if _, err := New(Config{}, func() filetree.Format { return &Markdown{} }); err == nil {
		t.Fatal("empty path accepted")
	}
	ok, _ := New(Config{Path: t.TempDir()}, func() filetree.Format { return &Markdown{} })
	if _, err := ok.Open(ctx, json.RawMessage(`{"folder":"../out"}`)); err == nil {
		t.Fatal("escape accepted")
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{
		"Getting Started": "Getting-Started", "What is it?": "What-is-it", "a/b: c": "ab-c",
		"_Sidebar": "Sidebar", "CON": "CON_", "  ": "Untitled", ".hidden": "hidden",
	} {
		if got := GitHubName(in); got != want {
			t.Errorf("GitHubName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Getting Started": "Getting Started", "What is it?": "What is it-", "a/b: c": "a-b- c",
		"[draft] <x>": "(draft) (x)", "nul": "nul_", ".env": "env", "end.": "end",
	} {
		if got := MarkdownName(in); got != want {
			t.Errorf("MarkdownName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range [][3]string{
		{"A/B", "A/B/c.md", "c.md"}, {"A/B", "A/x.md", "../x.md"}, {"", "A/x.md", "A/x.md"},
		{"A", "B/C/x.png", "../B/C/x.png"}, {"A/B", "A/B", "../B"},
	} {
		if got := relPath(c[0], c[1]); got != c[2] {
			t.Errorf("relPath(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	if got := slug("Install & Run 2.0"); got != "install--run-20" {
		t.Errorf("slug = %q", got)
	}
}

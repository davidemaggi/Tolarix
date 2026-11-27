package filetree

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func files(t *testing.T, root string) string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && p != root {
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				rel += "/"
			}
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestDirTreeCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "out/a.md", "A")
	write(t, dir, "out/sub/b.md", "B")
	write(t, dir, "out/old/c.md", "C")
	write(t, dir, "out/.git/HEAD", "ref")
	write(t, dir, "other.md", "outside")

	tr := NewTree(Dir{Path: dir}, "out")
	if err := tr.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tr.Paths(), " "); got != "out/a.md out/old/c.md out/sub/b.md" {
		t.Fatalf("loaded %s", got)
	}
	if id, _ := tr.ID("out/a.md"); id != BlobID([]byte("A")) {
		t.Fatal("blob id differs from git's")
	}

	tr.Put("out/a.md", []byte("A")) // same content: unchanged
	tr.Put("out/new/d.md", []byte("D"))
	if err := tr.PutReader("out/sub/img.png", strings.NewReader("PNG")); err != nil {
		t.Fatal(err)
	}
	tr.Move("out/old/c.md", "out/moved/c.md")
	tr.Delete("out/sub/b.md")

	changes, summary := tr.Diff(ctx)
	var got []string
	for _, c := range changes {
		got = append(got, c.Type+" "+c.Path)
	}
	if strings.Join(got, ", ") != "delete out/sub/b.md, rename out/moved/c.md, add out/new/d.md, add out/sub/img.png" ||
		summary != "2 added, 0 changed, 1 moved, 1 removed" {
		t.Fatalf("diff = %v (%s)", got, summary)
	}
	if err := tr.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	tr.Close()
	if got := files(t, dir); got != "other.md out/ out/.git/ out/.git/HEAD out/a.md out/moved/ out/moved/c.md out/new/ out/new/d.md out/sub/ out/sub/img.png" {
		t.Fatalf("files = %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out/moved/c.md")); string(b) != "C" {
		t.Fatalf("moved content = %q", b)
	}
	if changes, _ := tr.Diff(ctx); len(changes) != 0 {
		t.Fatalf("second diff = %v", changes)
	}

	// Emptied folders go, the target folder itself stays.
	tr.Delete("out/a.md")
	tr.Delete("out/moved/c.md")
	tr.Delete("out/new/d.md")
	tr.Delete("out/sub/img.png")
	if err := tr.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := files(t, dir); got != "other.md out/ out/.git/ out/.git/HEAD" {
		t.Fatalf("after deletes: %s", got)
	}
}

func TestDirMissingRoot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tr := NewTree(Dir{Path: dir}, "new/folder")
	if err := tr.Load(ctx); err != nil || len(tr.Paths()) != 0 {
		t.Fatalf("load = %v, %v", err, tr.Paths())
	}
	tr.Put("new/folder/x.md", []byte("X"))
	if err := tr.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "new/folder/x.md")); string(b) != "X" {
		t.Fatal("not written")
	}
}

type nopFormat struct{ Format }

func (nopFormat) Finalize(context.Context, *Tree) error { return nil }
func (nopFormat) Hidden(rel string) bool                { return DotHidden(rel) }

func TestSessionFinishWithoutCommitWritesNothing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "keep.md", "K")
	s := NewSession(Dir{Path: dir}, "", nopFormat{})
	inv, err := s.Inventory(ctx)
	if err != nil || len(inv) != 1 {
		t.Fatalf("inventory = %v, %v", inv, err)
	}
	if err := s.DeletePage(ctx, inv["keep.md"]); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAttachment(ctx, "a.png", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := files(t, dir); got != "keep.md" {
		t.Fatalf("files = %s", got)
	}
}

func TestInRoot(t *testing.T) {
	for _, c := range []struct {
		root, key string
		ok        bool
	}{
		{"out", "out/a.md", true}, {"out", "out/a/", true}, {"", "a.md", true},
		{"out", "other/a.md", false}, {"out", "out/../a.md", false}, {"", "../a.md", false},
		{"", "/etc/passwd", false}, {"out", "out//a.md", false},
	} {
		if err := InRoot(c.root, c.key); (err == nil) != c.ok {
			t.Errorf("InRoot(%q, %q) = %v", c.root, c.key, err)
		}
	}
}

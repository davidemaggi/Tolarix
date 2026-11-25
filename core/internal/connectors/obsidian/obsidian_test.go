package obsidian

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/testkit"
)

// Decode golden tests: testdata/decode/<case>/vault -> expected.yaml + warnings.txt
func TestDecodeGolden(t *testing.T) {
	cases, _ := filepath.Glob("testdata/decode/*")
	if len(cases) == 0 {
		t.Fatal("no decode cases")
	}
	for _, dir := range cases {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			v, err := Open(filepath.Join(dir, "vault"))
			if err != nil {
				t.Fatal(err)
			}
			var ws connectors.Warnings
			tree, err := v.Snapshot(context.Background(), &ws)
			if err != nil {
				t.Fatal(err)
			}
			testkit.Golden(t, filepath.Join(dir, "expected.yaml"), testkit.TreeYAML(t, tree))
			testkit.Golden(t, filepath.Join(dir, "warnings.txt"), testkit.WarningsText(ws.List))
		})
	}
}

// Encode golden tests: testdata/encode/<case>/input.yaml (+ location.json,
// optional app.json) -> expected/ (files written into an empty vault) + warnings.txt.
// Attachment bytes are the attachment name, so the output is self-describing.
func TestEncodeGolden(t *testing.T) {
	cases, _ := filepath.Glob("testdata/encode/*")
	if len(cases) == 0 {
		t.Fatal("no encode cases")
	}
	for _, dir := range cases {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			ctx := context.Background()
			tree := testkit.LoadTree(t, filepath.Join(dir, "input.yaml"))
			out := t.TempDir()
			if b, err := os.ReadFile(filepath.Join(dir, "app.json")); err == nil {
				_ = os.MkdirAll(filepath.Join(out, ".obsidian"), 0o755)
				_ = os.WriteFile(filepath.Join(out, ".obsidian", "app.json"), b, 0o644)
			}
			loc, _ := os.ReadFile(filepath.Join(dir, "location.json"))
			v, _ := Open(out)
			s, err := v.Open(ctx, json.RawMessage(loc))
			if err != nil {
				t.Fatal(err)
			}
			var ws connectors.Warnings
			l := s.Layout(tree, &ws)
			tree.Walk(func(p *model.Page, _ int) {
				content, err := s.Encode(tree, p, l, &ws)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.WritePage(ctx, connectors.PageWrite{Placement: l.Pages[p.ID], Content: content, Placeholder: p.Placeholder}); err != nil {
					t.Fatal(err)
				}
			})
			for hash, key := range l.Attachments {
				if err := s.PutAttachment(ctx, key, bytes.NewReader([]byte(tree.Attachments[hash].Name))); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Finish(ctx, true); err != nil {
				t.Fatal(err)
			}
			_ = os.RemoveAll(filepath.Join(out, ".obsidian"))
			testkit.GoldenDir(t, filepath.Join(dir, "expected"), out)
			testkit.Golden(t, filepath.Join(dir, "warnings.txt"), testkit.WarningsText(ws.List))
		})
	}
}

func TestSessionMoveArchiveDelete(t *testing.T) {
	ctx := context.Background()
	out := t.TempDir()
	v, _ := Open(out)
	s, err := v.Open(ctx, json.RawMessage(`{"folder":"Mirror"}`))
	if err != nil {
		t.Fatal(err)
	}
	write := func(existing *connectors.MirrorItem, key, content string) connectors.MirrorItem {
		t.Helper()
		it, err := s.WritePage(ctx, connectors.PageWrite{Existing: existing, Placement: connectors.Placement{Key: key}, Content: []byte(content)})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	a := write(nil, "Mirror/A/Deep.md", "one")
	moved := write(&a, "Mirror/B.md", "two")
	if moved.Key != "Mirror/B.md" || moved.Hash == a.Hash {
		t.Fatalf("move: %+v", moved)
	}
	inv, _ := s.Inventory(ctx)
	if _, ok := inv["Mirror/A/Deep.md"]; ok {
		t.Fatal("old file still there")
	}
	arch, err := s.ArchivePage(ctx, moved)
	if err != nil || arch.Key != "Mirror/Archived/B.md" {
		t.Fatalf("archive: %+v %v", arch, err)
	}
	c := write(nil, "Mirror/C.md", "c")
	if err := s.DeletePage(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, true); err != nil {
		t.Fatal(err)
	}
	got := testkit.ReadDir(t, out)
	if len(got) != 1 || string(got["Mirror/Archived/B.md"]) != "two" {
		t.Fatalf("final vault: %v", keys(got))
	}
}

func TestSessionRejectsEscapes(t *testing.T) {
	ctx := context.Background()
	v, _ := Open(t.TempDir())
	if _, err := v.Open(ctx, json.RawMessage(`{"folder":"../outside"}`)); err == nil {
		t.Fatal("location escape accepted")
	}
	s, _ := v.Open(ctx, nil)
	if _, err := s.WritePage(ctx, connectors.PageWrite{Placement: connectors.Placement{Key: "../evil.md"}, Content: []byte("x")}); err == nil {
		t.Fatal("key escape accepted")
	}
}

func TestBrowse(t *testing.T) {
	v, _ := Open("testdata/decode/basic/vault")
	nodes, err := v.Browse(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range nodes {
		got[n.ID] = n.HasChildren
	}
	if hc, ok := got["Projects"]; !ok || !hc {
		t.Errorf("Projects should be listed with children: %v", got)
	}
	if hc, ok := got["Home"]; !ok || hc {
		t.Errorf("Home should be a leaf: %v", got)
	}
	if _, ok := got[".trash"]; ok {
		t.Error("hidden folders must not be listed")
	}
}

func TestTestErrors(t *testing.T) {
	v, _ := Open(filepath.Join(t.TempDir(), "missing"))
	if err := v.Test(context.Background()); err == nil {
		t.Fatal("missing vault accepted")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

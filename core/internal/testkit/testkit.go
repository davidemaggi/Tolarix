// Package testkit holds helpers shared by tests: golden files and a readable
// YAML form of the canonical tree used as converter fixtures.
//
// Regenerate golden files with:  go test ./... -update
package testkit

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Update rewrites golden files instead of comparing them.
var Update = flag.Bool("update", false, "rewrite golden files")

// Golden compares got with the file at path (or rewrites it with -update).
func Golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *Update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil { //nolint:gosec // test fixtures
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixtures
	if err != nil {
		t.Fatalf("missing golden file %s (run go test -update): %v", path, err)
	}
	if !bytes.Equal(normalizeEOL(want), normalizeEOL(got)) {
		t.Errorf("%s differs from golden file (run go test -update to accept):\n%s", path, diff(string(want), string(got)))
	}
}

// GoldenDir compares the files under dir with the golden directory.
func GoldenDir(t *testing.T, goldenDir, dir string) {
	t.Helper()
	got := ReadDir(t, dir)
	if *Update {
		_ = os.RemoveAll(goldenDir)
		for rel, b := range got {
			if strings.HasSuffix(rel, "/") {
				Golden(t, filepath.Join(goldenDir, filepath.FromSlash(rel), ".keep"), nil)
				continue
			}
			Golden(t, filepath.Join(goldenDir, filepath.FromSlash(rel)), b)
		}
		return
	}
	want := ReadDir(t, goldenDir)
	for rel, w := range want {
		g, ok := got[rel]
		if !ok {
			t.Errorf("missing file %s", rel)
			continue
		}
		if !bytes.Equal(normalizeEOL(w), normalizeEOL(g)) {
			t.Errorf("%s differs from golden:\n%s", rel, diff(string(w), string(g)))
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			t.Errorf("unexpected file %s", rel)
		}
	}
}

// ReadDir returns every file under dir keyed by slash path; empty
// directories are listed with a trailing "/" and nil content.
func ReadDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == dir {
				return filepath.SkipAll
			}
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != dir && isEmptyDir(p) {
				out[rel+"/"] = nil
			}
			return nil
		}
		if d.Name() == ".keep" {
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // test fixtures
		out[rel] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// isEmptyDir treats a directory holding only ".keep" as empty: golden
// directories store empty folders that way because git keeps no empty dirs.
func isEmptyDir(p string) bool {
	entries, _ := os.ReadDir(p)
	for _, e := range entries {
		if e.Name() != ".keep" {
			return false
		}
	}
	return true
}

// CopyDir copies src into dst (used to start tests from a fixture vault).
func CopyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if d.Name() == ".keep" {
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // test fixtures
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644) //nolint:gosec // test fixtures
	})
	if err != nil {
		t.Fatal(err)
	}
}

func normalizeEOL(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

func diff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	n := max(len(wl), len(gl))
	shown := 0
	for i := 0; i < n && shown < 40; i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			fmt.Fprintf(&b, "line %d:\n  want: %q\n  got:  %q\n", i+1, w, g)
			shown++
		}
	}
	return b.String()
}

// TreeDoc is the YAML fixture form of a canonical tree.
type TreeDoc struct {
	Pages       []PageDoc       `yaml:"pages"`
	Attachments []AttachmentDoc `yaml:"attachments,omitempty"`
}

type PageDoc struct {
	ID          string           `yaml:"id"`
	Parent      string           `yaml:"parent,omitempty"`
	Name        string           `yaml:"name,omitempty"`
	Title       string           `yaml:"title"`
	Order       int              `yaml:"order"`
	Placeholder bool             `yaml:"placeholder,omitempty"`
	Meta        []model.MetaItem `yaml:"meta,omitempty"`
	Attachments []string         `yaml:"attachments,omitempty"`
	Body        string           `yaml:"body,omitempty"`
}

type AttachmentDoc struct {
	Hash      string `yaml:"hash"`
	Name      string `yaml:"name"`
	Size      int64  `yaml:"size"`
	MediaType string `yaml:"mediaType"`
	SourceRef string `yaml:"sourceRef,omitempty"`
}

// TreeYAML renders a tree deterministically (page walk order, attachments by name).
func TreeYAML(t *testing.T, tree *model.Tree) []byte {
	t.Helper()
	var doc TreeDoc
	tree.Walk(func(p *model.Page, _ int) {
		doc.Pages = append(doc.Pages, PageDoc{
			ID: p.ID, Parent: p.ParentID, Name: p.Name, Title: p.Title, Order: p.Order,
			Placeholder: p.Placeholder, Meta: p.Meta, Attachments: p.Attachments, Body: p.Body,
		})
	})
	for _, a := range tree.Attachments {
		doc.Attachments = append(doc.Attachments, AttachmentDoc{a.Hash, a.Name, a.Size, a.MediaType, a.SourceRef})
	}
	sort.Slice(doc.Attachments, func(i, j int) bool {
		if doc.Attachments[i].Name != doc.Attachments[j].Name {
			return doc.Attachments[i].Name < doc.Attachments[j].Name
		}
		return doc.Attachments[i].Hash < doc.Attachments[j].Hash
	})
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// LoadTree reads a YAML tree fixture.
func LoadTree(t *testing.T, path string) *model.Tree {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test fixtures
	if err != nil {
		t.Fatal(err)
	}
	var doc TreeDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	tree := model.NewTree()
	for _, p := range doc.Pages {
		name := p.Name
		if name == "" {
			name = p.Title
		}
		tree.Add(&model.Page{
			ID: p.ID, ParentID: p.Parent, Name: name, Title: p.Title, Order: p.Order,
			Placeholder: p.Placeholder, Meta: p.Meta, Attachments: p.Attachments, Body: p.Body,
		})
	}
	for _, a := range doc.Attachments {
		tree.Attachments[a.Hash] = &model.Attachment{Hash: a.Hash, Name: a.Name, Size: a.Size, MediaType: a.MediaType, SourceRef: a.SourceRef}
	}
	return tree
}

// WarningsText renders warnings one per line, sorted, for golden files.
func WarningsText(ws []connectors.Warning) []byte {
	lines := make([]string, 0, len(ws))
	for _, w := range ws {
		lines = append(lines, fmt.Sprintf("%s [%s] %s", w.PageID, w.Code, w.Message))
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return []byte("(no warnings)\n")
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// JSON pretty-prints v for golden files.
func JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

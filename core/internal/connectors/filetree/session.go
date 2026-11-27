package filetree

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// ArchiveFolder holds archived mirror pages, inside the target folder.
const ArchiveFolder = "Archived"

// Format is a folder layout: file names, page encoding and generated files.
// A Format value holds the state of one session (Layout runs first).
type Format interface {
	// Layout places the tree under root (relative to the store root). Page
	// keys are "dir/Name.md" for pages and "dir/Name/" for folder-only pages.
	Layout(tree *model.Tree, root string, w connectors.Warner) *connectors.Layout
	// Encode renders a page.
	Encode(p *model.Page, l *connectors.Layout, w connectors.Warner) ([]byte, error)
	// Hidden reports paths (relative to root) that are not pages or page
	// folders: attachment folders, generated files.
	Hidden(rel string) bool
	// Finalize updates generated files (navigation, ordering) once every
	// page write is done, before the tree is committed.
	Finalize(ctx context.Context, t *Tree) error
}

// Session is a connectors.Session over a Tree. Every write is buffered and
// applied in Finish.
type Session struct {
	tree   *Tree
	format Format
}

var (
	_ connectors.Session   = (*Session)(nil)
	_ connectors.Buffering = (*Session)(nil)
)

// NewSession starts a session writing root inside store.
func NewSession(store Store, root string, f Format) *Session {
	return &Session{tree: NewTree(store, root), format: f}
}

// Tree is the session's virtual folder (for tests and tools).
func (s *Session) Tree() *Tree { return s.tree }

func (s *Session) BuffersWrites() bool { return true }

func (s *Session) Layout(tree *model.Tree, w connectors.Warner) *connectors.Layout {
	return s.format.Layout(tree, s.tree.root, w)
}

func (s *Session) Encode(_ *model.Tree, p *model.Page, l *connectors.Layout, w connectors.Warner) ([]byte, error) {
	if p.Placeholder {
		return nil, nil
	}
	return s.format.Encode(p, l, w)
}

func (s *Session) ContentHash(content []byte) string { return BlobID(content) }

func (s *Session) rel(p string) string {
	return strings.TrimPrefix(strings.TrimPrefix(p, s.tree.root), "/")
}

// Inventory lists pages ("x.md") and folders ("x/") under the target folder.
func (s *Session) Inventory(ctx context.Context) (map[string]connectors.MirrorItem, error) {
	if err := s.tree.Load(ctx); err != nil {
		return nil, err
	}
	out := map[string]connectors.MirrorItem{}
	for p, id := range s.tree.base {
		if s.format.Hidden(s.rel(p)) {
			continue
		}
		if IsMD(p) {
			out[p] = connectors.MirrorItem{ID: p, Key: p, Hash: id}
		}
		for d := ParentDir(p); d != "" && d != s.tree.root; d = ParentDir(d) {
			out[d+"/"] = connectors.MirrorItem{ID: d + "/", Key: d + "/"}
		}
	}
	return out, nil
}

func (s *Session) HasAttachment(ctx context.Context, key string) (bool, error) {
	if err := s.tree.Load(ctx); err != nil {
		return false, err
	}
	_, ok := s.tree.cur[key]
	return ok, nil
}

func (s *Session) WritePage(ctx context.Context, w connectors.PageWrite) (connectors.MirrorItem, error) {
	if err := s.tree.Load(ctx); err != nil {
		return connectors.MirrorItem{}, err
	}
	key := w.Placement.Key
	if err := InRoot(s.tree.root, key); err != nil {
		return connectors.MirrorItem{}, err
	}
	// Moved, or a page that became folder-only: drop the old file.
	if w.Existing != nil && w.Existing.Key != key && !strings.HasSuffix(w.Existing.Key, "/") {
		s.tree.Delete(w.Existing.Key)
	}
	if w.Placeholder {
		return connectors.MirrorItem{ID: key, Key: key}, nil
	}
	id := s.tree.Put(key, w.Content)
	return connectors.MirrorItem{ID: key, Key: key, Hash: id}, nil
}

func (s *Session) DeletePage(ctx context.Context, item connectors.MirrorItem) error {
	if err := s.tree.Load(ctx); err != nil {
		return err
	}
	if !strings.HasSuffix(item.Key, "/") {
		s.tree.Delete(item.Key) // a folder disappears with its last file
	}
	return nil
}

// ArchivePage moves a page under <target>/Archived/, keeping its relative path.
func (s *Session) ArchivePage(ctx context.Context, item connectors.MirrorItem) (connectors.MirrorItem, error) {
	if err := s.tree.Load(ctx); err != nil {
		return item, err
	}
	if strings.HasSuffix(item.Key, "/") {
		return item, nil
	}
	archive := JoinRel(s.tree.root, ArchiveFolder)
	if strings.HasPrefix(item.Key, archive+"/") {
		return item, nil
	}
	key := JoinRel(archive, s.rel(item.Key))
	id, ok := s.tree.Move(item.Key, key)
	if !ok {
		return item, fmt.Errorf("archive %s: the page is no longer in the target folder", item.Key)
	}
	return connectors.MirrorItem{ID: key, Key: key, Hash: id}, nil
}

func (s *Session) PutAttachment(ctx context.Context, key string, r io.Reader) error {
	if err := s.tree.Load(ctx); err != nil {
		return err
	}
	if err := InRoot(s.tree.root, key); err != nil {
		return err
	}
	return s.tree.PutReader(key, r)
}

func (s *Session) DeleteAttachment(ctx context.Context, key string) error {
	if err := s.tree.Load(ctx); err != nil {
		return err
	}
	s.tree.Delete(key)
	return nil
}

// Finish updates the generated files and applies every change at once.
func (s *Session) Finish(ctx context.Context, commit bool) error {
	defer s.tree.Close()
	if !commit || !s.tree.loaded {
		return nil
	}
	if err := s.format.Finalize(ctx, s.tree); err != nil {
		return err
	}
	return s.tree.Commit(ctx)
}

// ---- path helpers shared by formats ----

// JoinRel joins two slash paths, either of which may be empty.
func JoinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	if name == "" {
		return dir
	}
	return dir + "/" + name
}

// ParentDir is the folder of a slash path ("" at the top).
func ParentDir(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// TrimMD drops a ".md" extension.
func TrimMD(p string) string {
	if IsMD(p) {
		return p[:len(p)-3]
	}
	return p
}

// IsMD reports a Markdown file name.
func IsMD(p string) bool { return strings.EqualFold(path.Ext(p), ".md") }

// DotHidden reports whether any segment of rel starts with "." (.git,
// .attachments, .order…).
func DotHidden(rel string) bool {
	for _, s := range strings.Split(rel, "/") {
		if strings.HasPrefix(s, ".") {
			return true
		}
	}
	return false
}

// UniqueName returns base, or base with sep and a number, unused in used
// (case-insensitively), and marks it used.
func UniqueName(base, sep string, used map[string]bool) string {
	name := base
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s%s(%d)", base, sep, i)
	}
	used[strings.ToLower(name)] = true
	return name
}

// WindowsReserved reports device names Windows refuses as file names, with
// or without an extension ("CON", "nul.md", "COM1").
func WindowsReserved(name string) bool {
	base := strings.ToUpper(strings.TrimSpace(name))
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = strings.TrimSpace(base[:i])
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

// FirstNonEmpty returns the first value that is not blank.
func FirstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

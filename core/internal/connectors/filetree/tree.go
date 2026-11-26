// Package filetree is the shared write path of file-based targets whose
// output is a folder of Markdown files: an Azure DevOps wiki repository, or a
// folder on this machine in one of the supported layouts.
//
// A Tree is a virtual copy of the target folder. Writes change only the
// copy; Commit computes the difference with what was loaded and hands it to
// a Store in one go (one git push, or one pass over the local folder). A
// Session drives a Tree for the sync engine, with a Format deciding file
// names, links and generated files.
package filetree

import (
	"context"
	"crypto/sha1" //nolint:gosec // git object ids are SHA-1 by definition
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// Change is one file change. Paths are relative to the store root.
type Change struct {
	Type    string // add | edit | delete | rename
	Path    string
	From    string // rename source
	Size    int64
	Content func() ([]byte, error) // add and edit
}

// Store is where a target folder lives.
type Store interface {
	// Load lists the files under root (relative to the store root, ""
	// = everything) as path -> blob id. A missing root is an empty folder.
	Load(ctx context.Context, root string) (map[string]string, error)
	// Read returns the content of a loaded file.
	Read(ctx context.Context, path, id string) ([]byte, error)
	// Apply writes changes (deletes, then renames, then writes). summary
	// describes them in a few words, for a commit message.
	Apply(ctx context.Context, root string, changes []Change, summary string) error
}

// BlobID is the git blob id of content. Every store uses it as the file
// hash, so hashes stay comparable whatever the store.
func BlobID(content []byte) string {
	h := sha1.New() //nolint:gosec // see import
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// FileBlobID is BlobID of a file's content, streamed.
func FileBlobID(name string, size int64) (string, error) {
	f, err := os.Open(name) //nolint:gosec // callers pass files they own
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New() //nolint:gosec // see import
	fmt.Fprintf(h, "blob %d\x00", size)
	n, err := io.Copy(h, f)
	if err != nil {
		return "", err
	}
	if n != size {
		return "", fmt.Errorf("%s changed while reading it", name)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// entry is a file in the tree's current view.
type entry struct {
	id      string
	content []byte // new content held in memory (pages, generated files)
	spool   string // new content spooled to disk (attachments)
	size    int64
	origin  string // unchanged content: its path when loaded
}

// Tree is a virtual copy of a target folder. Paths are relative to the store
// root and use forward slashes.
type Tree struct {
	store Store
	root  string

	loaded bool
	base   map[string]string // path -> blob id when loaded
	cur    map[string]*entry // path -> file after this session's writes
	tmp    string
}

// NewTree returns a tree for root inside store. Nothing is read until the
// first call that needs it.
func NewTree(store Store, root string) *Tree {
	return &Tree{store: store, root: root}
}

// Root is the target folder, relative to the store root.
func (t *Tree) Root() string { return t.root }

// Load reads the folder once.
func (t *Tree) Load(ctx context.Context) error {
	if t.loaded {
		return nil
	}
	base, err := t.store.Load(ctx, t.root)
	if err != nil {
		return err
	}
	t.base, t.cur = map[string]string{}, map[string]*entry{}
	for p, id := range base {
		t.base[p] = id
		t.cur[p] = &entry{id: id, origin: p}
	}
	t.loaded = true
	return nil
}

// Base lists the files as loaded (path -> blob id). Do not modify it.
func (t *Tree) Base() map[string]string { return t.base }

// Paths lists the files in the current view, sorted.
func (t *Tree) Paths() []string {
	out := make([]string, 0, len(t.cur))
	for p := range t.cur {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ID returns the blob id of a file in the current view.
func (t *Tree) ID(p string) (string, bool) {
	e, ok := t.cur[p]
	if !ok {
		return "", false
	}
	return e.id, true
}

// Read returns the current content of a file.
func (t *Tree) Read(ctx context.Context, p string) ([]byte, error) {
	e, ok := t.cur[p]
	if !ok {
		return nil, fmt.Errorf("%s: %w", p, os.ErrNotExist)
	}
	return t.contentOf(ctx, e)()
}

// Put sets a file's content and returns its blob id. Writing the content a
// file already has keeps it unchanged.
func (t *Tree) Put(p string, content []byte) string {
	if content == nil {
		content = []byte{} // an empty file is still new content
	}
	id := BlobID(content)
	if e, ok := t.cur[p]; ok && e.id == id {
		return id
	}
	t.cur[p] = &entry{id: id, content: content, size: int64(len(content))}
	return id
}

// PutReader sets a file's content from r, spooled to a temporary file.
func (t *Tree) PutReader(p string, r io.Reader) error {
	if t.tmp == "" {
		dir, err := os.MkdirTemp("", "tolarix-tree-*")
		if err != nil {
			return err
		}
		t.tmp = dir
	}
	f, err := os.CreateTemp(t.tmp, "file-*")
	if err != nil {
		return err
	}
	size, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	id, err := FileBlobID(f.Name(), size)
	if err != nil {
		return err
	}
	t.cur[p] = &entry{id: id, spool: f.Name(), size: size}
	return nil
}

// Delete removes a file from the current view.
func (t *Tree) Delete(p string) { delete(t.cur, p) }

// Move renames a file in the current view and returns its blob id.
func (t *Tree) Move(from, to string) (string, bool) {
	e, ok := t.cur[from]
	if !ok {
		return "", false
	}
	delete(t.cur, from)
	t.cur[to] = e
	return e.id, true
}

// Diff compares the current view with what was loaded: deletes, then
// renames, then writes, each sorted by path.
func (t *Tree) Diff(ctx context.Context) ([]Change, string) {
	renamed := map[string]bool{}
	var dels, renames, writes []Change
	var added, edited int
	for _, p := range t.Paths() {
		e := t.cur[p]
		baseID, inBase := t.base[p]
		if inBase && baseID == e.id {
			continue
		}
		if e.content == nil && e.spool == "" && e.origin != "" && !inBase {
			if _, still := t.cur[e.origin]; !still && !renamed[e.origin] {
				renamed[e.origin] = true
				renames = append(renames, Change{Type: "rename", From: e.origin, Path: p})
				continue
			}
		}
		c := Change{Type: "add", Path: p, Size: e.size, Content: t.contentOf(ctx, e)}
		if inBase {
			c.Type = "edit"
			edited++
		} else {
			added++
		}
		writes = append(writes, c)
	}
	var removed []string
	for p := range t.base {
		if _, ok := t.cur[p]; !ok && !renamed[p] {
			removed = append(removed, p)
		}
	}
	sort.Strings(removed)
	for _, p := range removed {
		dels = append(dels, Change{Type: "delete", Path: p})
	}
	all := append(append(dels, renames...), writes...)
	summary := fmt.Sprintf("%d added, %d changed, %d moved, %d removed", added, edited, len(renames), len(dels))
	return all, summary
}

// Commit applies the difference to the store. The tree then matches the
// store, so a second Commit is a no-op.
func (t *Tree) Commit(ctx context.Context) error {
	if !t.loaded {
		return nil
	}
	changes, summary := t.Diff(ctx)
	if len(changes) == 0 {
		return nil
	}
	if err := t.store.Apply(ctx, t.root, changes, summary); err != nil {
		return err
	}
	t.base = map[string]string{}
	for p, e := range t.cur {
		t.base[p] = e.id
		*e = entry{id: e.id, origin: p}
	}
	return nil
}

// Close removes temporary files.
func (t *Tree) Close() {
	if t.tmp != "" {
		_ = os.RemoveAll(t.tmp)
		t.tmp = ""
	}
}

func (t *Tree) contentOf(ctx context.Context, e *entry) func() ([]byte, error) {
	switch {
	case e.content != nil:
		return func() ([]byte, error) { return e.content, nil }
	case e.spool != "":
		return func() ([]byte, error) { return os.ReadFile(e.spool) }
	default:
		return func() ([]byte, error) {
			b, err := t.store.Read(ctx, e.origin, e.id)
			if err == nil && BlobID(b) != e.id {
				err = fmt.Errorf("the file %s changed while reading it", e.origin)
			}
			return b, err
		}
	}
}

// InRoot reports an error when key is not a clean path inside root.
func InRoot(root, key string) error {
	if root != "" && !strings.HasPrefix(key, root+"/") {
		return fmt.Errorf("refusing to write outside the target folder: %q", key)
	}
	if c := path.Clean(key); c != strings.TrimSuffix(key, "/") || c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") {
		return fmt.Errorf("invalid path %q", key)
	}
	return nil
}

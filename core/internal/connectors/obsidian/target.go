package obsidian

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// session writes into one folder of a vault. Writes go straight to disk
// (atomically per file); Finish removes folders this session emptied.
type session struct {
	vault      string // absolute vault path
	root       string // vault-relative target folder ("" = vault root)
	attSetting string
	touched    map[string]bool // vault-relative dirs that may have become empty
}

var _ connectors.Session = (*session)(nil)

func (s *session) Layout(tree *model.Tree, w connectors.Warner) *connectors.Layout {
	return layout(tree, s.root, s.attSetting, w)
}

func (s *session) Encode(_ *model.Tree, p *model.Page, l *connectors.Layout, w connectors.Warner) ([]byte, error) {
	return encode(p, l, w)
}

func (s *session) ContentHash(content []byte) string { return model.HashBytes(content) }

// Inventory lists notes (".md" keys) and folders ("dir/" keys) under the
// target folder, attachments excluded.
func (s *session) Inventory(ctx context.Context) (map[string]connectors.MirrorItem, error) {
	out := map[string]connectors.MirrorItem{}
	base := abs(s.vault, s.root)
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == base {
				return filepath.SkipAll
			}
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if p == base {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(s.vault, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out[rel+"/"] = connectors.MirrorItem{ID: rel + "/", Key: rel + "/"}
			return nil
		}
		if !strings.EqualFold(path.Ext(rel), ".md") {
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // inside the configured vault
		if err != nil {
			return err
		}
		out[rel] = connectors.MirrorItem{ID: rel, Key: rel, Hash: model.HashBytes(b)}
		return nil
	})
	return out, err
}

func (s *session) path(key string) (string, error) {
	rel, err := cleanRel(key)
	if err != nil {
		return "", fmt.Errorf("refusing to write outside the vault: %q", key)
	}
	return abs(s.vault, rel), nil
}

func (s *session) WritePage(ctx context.Context, w connectors.PageWrite) (connectors.MirrorItem, error) {
	if err := ctx.Err(); err != nil {
		return connectors.MirrorItem{}, err
	}
	key := w.Placement.Key
	dst, err := s.path(key)
	if err != nil {
		return connectors.MirrorItem{}, err
	}
	if w.Placeholder {
		if err := os.MkdirAll(dst, 0o755); err != nil { //nolint:gosec // vault folders must stay readable by Obsidian
			return connectors.MirrorItem{}, err
		}
		// A note that became a folder-only page: remove the old note file.
		if w.Existing != nil && !strings.HasSuffix(w.Existing.Key, "/") {
			if err := s.removeFile(w.Existing.Key); err != nil {
				return connectors.MirrorItem{}, err
			}
		}
		return connectors.MirrorItem{ID: key, Key: key}, nil
	}
	// Move: rename the old note first so a failed write leaves it at its new place.
	if w.Existing != nil && w.Existing.Key != key && !strings.HasSuffix(w.Existing.Key, "/") {
		src, err := s.path(w.Existing.Key)
		if err != nil {
			return connectors.MirrorItem{}, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // see above
			return connectors.MirrorItem{}, err
		}
		if err := os.Rename(src, dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return connectors.MirrorItem{}, fmt.Errorf("move %s to %s: %w", w.Existing.Key, key, err)
		}
		s.touch(parentDir(w.Existing.Key))
	}
	if err := writeFileAtomic(dst, w.Content); err != nil {
		return connectors.MirrorItem{}, err
	}
	return connectors.MirrorItem{ID: key, Key: key, Hash: model.HashBytes(w.Content)}, nil
}

func (s *session) DeletePage(_ context.Context, item connectors.MirrorItem) error {
	if strings.HasSuffix(item.Key, "/") {
		s.touch(strings.TrimSuffix(item.Key, "/"))
		return nil // removed in Finish once empty
	}
	return s.removeFile(item.Key)
}

func (s *session) removeFile(key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.touch(parentDir(key))
	return nil
}

// ArchivePage moves a note under <root>/Archived/, keeping its relative path.
func (s *session) ArchivePage(ctx context.Context, item connectors.MirrorItem) (connectors.MirrorItem, error) {
	if strings.HasSuffix(item.Key, "/") {
		return item, s.DeletePage(ctx, item)
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(item.Key, s.root), "/")
	archiveRoot := joinRel(s.root, ArchiveFolder)
	if strings.HasPrefix(item.Key, archiveRoot+"/") {
		return item, nil // already archived
	}
	key := joinRel(archiveRoot, rel)
	src, err := s.path(item.Key)
	if err != nil {
		return item, err
	}
	dst, err := s.path(key)
	if err != nil {
		return item, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // see above
		return item, err
	}
	if err := os.Rename(src, dst); err != nil {
		return item, fmt.Errorf("archive %s: %w", item.Key, err)
	}
	s.touch(parentDir(item.Key))
	return connectors.MirrorItem{ID: key, Key: key, Hash: item.Hash}, nil
}

func (s *session) HasAttachment(_ context.Context, key string) (bool, error) {
	p, err := s.path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (s *session) PutAttachment(_ context.Context, key string, r io.Reader) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return writeFileAtomic(p, b)
}

func (s *session) DeleteAttachment(_ context.Context, key string) error {
	return s.removeFile(key)
}

// Finish removes folders emptied during the session (deepest first), never
// the target folder itself.
func (s *session) Finish(_ context.Context, _ bool) error {
	dirs := make([]string, 0, len(s.touched))
	for d := range s.touched {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		for cur := d; cur != "" && s.inRoot(cur); cur = parentDir(cur) {
			p, err := s.path(cur)
			if err != nil {
				break
			}
			if err := os.Remove(p); err != nil { // fails (and stops) when not empty
				break
			}
		}
	}
	return nil
}

// inRoot reports whether dir is strictly inside the target folder.
func (s *session) inRoot(dir string) bool {
	return s.root == "" || strings.HasPrefix(dir, s.root+"/")
}

func (s *session) touch(dir string) {
	if dir != "" && dir != s.root {
		s.touched[dir] = true
	}
}

func writeFileAtomic(dst string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // see above
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tolarix-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, dst); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

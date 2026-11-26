package filetree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Dir is a Store on the local disk, rooted at an absolute folder.
type Dir struct {
	Path string
}

var _ Store = Dir{}

func (d Dir) abs(rel string) string { return filepath.Join(d.Path, filepath.FromSlash(rel)) }

// Load hashes every file under root. .git folders are skipped (a wiki
// repository cloned into the target), and so are symbolic links.
func (d Dir) Load(ctx context.Context, root string) (map[string]string, error) {
	out := map[string]string{}
	start := d.abs(root)
	err := filepath.WalkDir(start, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if p == start && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.IsDir() {
			if e.Name() == ".git" && p != start {
				return fs.SkipDir
			}
			return nil
		}
		if !e.Type().IsRegular() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(d.Path, p)
		if err != nil {
			return err
		}
		id, err := FileBlobID(p, info.Size())
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = id
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot read the folder %s: %w", start, err)
	}
	return out, nil
}

func (d Dir) Read(_ context.Context, p, _ string) ([]byte, error) {
	return os.ReadFile(d.abs(p))
}

// Apply deletes, renames and writes files, then removes the folders it
// emptied (never root itself). Each file is written atomically.
func (d Dir) Apply(ctx context.Context, root string, changes []Change, _ string) error {
	emptied := map[string]bool{}
	for _, c := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, p := range []string{c.Path, c.From} {
			if p != "" {
				if err := InRoot(root, p); err != nil {
					return err
				}
			}
		}
		switch c.Type {
		case "delete":
			if err := os.Remove(d.abs(c.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			emptied[path.Dir(c.Path)] = true
		case "rename":
			if err := os.MkdirAll(filepath.Dir(d.abs(c.Path)), 0o755); err != nil { //nolint:gosec // exported folders are meant to be shared
				return err
			}
			if err := os.Rename(d.abs(c.From), d.abs(c.Path)); err != nil {
				return err
			}
			emptied[path.Dir(c.From)] = true
		case "add", "edit":
			b, err := c.Content()
			if err != nil {
				return err
			}
			if err := writeFileAtomic(d.abs(c.Path), b); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown change %q", c.Type)
		}
	}
	for dir := range emptied {
		for dir != "." && dir != "" && dir != root && (root == "" || strings.HasPrefix(dir, root+"/")) {
			if os.Remove(d.abs(dir)) != nil { // fails when not empty: stop
				break
			}
			dir = path.Dir(dir)
		}
	}
	return nil
}

func writeFileAtomic(dst string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // see above
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), ".tolarix-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644) //nolint:gosec // exported wiki pages are meant to be shared
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

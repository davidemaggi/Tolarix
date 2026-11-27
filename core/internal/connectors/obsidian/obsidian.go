package obsidian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

// Kind is the registry key.
const Kind connectors.Kind = "obsidian"

func init() {
	connectors.Register(connectors.Spec{
		Kind:        Kind,
		DisplayName: "Obsidian vault",
		CanRead:     true,
		CanWrite:    true,
		Fields:      []connectors.Field{{Name: "path", Type: "path", Required: true}},
		LocationFields: []connectors.Field{
			{Name: "folder", Type: "string"},
		},
		New: func(raw json.RawMessage, _ secrets.String) (connectors.Connector, error) {
			c, err := parseConfig(raw)
			if err != nil {
				return nil, err
			}
			p, err := filepath.Abs(c.Path)
			if err != nil {
				return nil, err
			}
			return &Vault{path: p}, nil
		},
	})
}

// Vault is an Obsidian vault on disk; it is both a Source and a Target.
type Vault struct{ path string }

var (
	_ connectors.Source = (*Vault)(nil)
	_ connectors.Target = (*Vault)(nil)
)

// Open builds a Vault for path (used by tests and tools).
func Open(path string) (*Vault, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return &Vault{path: p}, nil
}

func (v *Vault) Test(_ context.Context) error {
	fi, err := os.Stat(v.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("the folder %s does not exist. Check the path; in Docker, check that the vault is mounted as a volume", v.path)
	case err != nil:
		return fmt.Errorf("cannot open %s: %w", v.path, err)
	case !fi.IsDir():
		return fmt.Errorf("%s is a file, not a folder. Enter the vault folder", v.path)
	}
	if _, err := os.ReadDir(v.path); err != nil {
		return fmt.Errorf("cannot read the folder %s; check its permissions: %w", v.path, err)
	}
	return nil
}

func (v *Vault) Snapshot(ctx context.Context, w connectors.Warner) (*model.Tree, error) {
	if err := v.Test(ctx); err != nil {
		return nil, err
	}
	return snapshot(ctx, v.path, w)
}

func (v *Vault) OpenAttachment(_ context.Context, ref string) (io.ReadCloser, error) {
	rel, err := cleanRel(ref)
	if err != nil || rel == "" {
		return nil, fmt.Errorf("invalid attachment reference %q", ref)
	}
	return os.Open(abs(v.path, rel)) //nolint:gosec // inside the configured vault
}

// Browse lists notes and folders directly under parentID.
func (v *Vault) Browse(_ context.Context, parentID string) ([]connectors.Node, error) {
	rel, err := cleanRel(parentID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs(v.path, rel))
	if err != nil {
		return nil, err
	}
	byID := map[string]*connectors.Node{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		var id string
		switch {
		case e.IsDir():
			id = joinRel(rel, name)
		case strings.EqualFold(filepath.Ext(name), ".md"):
			id = joinRel(rel, trimMD(name))
		default:
			continue
		}
		n := byID[id]
		if n == nil {
			n = &connectors.Node{ID: id, Title: trimMD(name)}
			byID[id] = n
		}
		if e.IsDir() {
			n.HasChildren = true
		}
	}
	out := make([]connectors.Node, 0, len(byID))
	for _, n := range byID {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out, nil
}

func (v *Vault) Open(_ context.Context, location json.RawMessage) (connectors.Session, error) {
	loc, err := parseLocation(location)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(v.path); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("the vault folder %s is not available", v.path)
	}
	return &session{
		vault:      v.path,
		root:       loc.Folder,
		attSetting: readAppConfig(v.path).AttachmentFolderPath,
		touched:    map[string]bool{},
	}, nil
}

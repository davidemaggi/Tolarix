// Package folder exports mirrors to a folder on this machine, in the layout
// of a wiki (Azure DevOps, GitHub) or as plain Markdown, instead of writing
// to the wiki itself. The folder can then be committed, published or
// reviewed with other tools.
//
// Folders are Mirrors only. Each run writes its changes in one pass at the
// end, like a git-based target: a failed run leaves the folder as it was.
package folder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/connectors/azuredevops"
	"github.com/davidemaggi/tolarix/core/internal/connectors/filetree"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

// Registry keys, one per layout.
const (
	AzureDevOpsKind connectors.Kind = "folder-azure-devops"
	GitHubKind      connectors.Kind = "folder-github-wiki"
	MarkdownKind    connectors.Kind = "folder-markdown"
)

var formats = []struct {
	kind connectors.Kind
	name string
	new  func() filetree.Format
}{
	{AzureDevOpsKind, "Local folder: Azure DevOps wiki", func() filetree.Format { return azuredevops.NewFormat() }},
	{GitHubKind, "Local folder: GitHub wiki", func() filetree.Format { return &GitHub{} }},
	{MarkdownKind, "Local folder: Markdown", func() filetree.Format { return &Markdown{} }},
}

func init() {
	for _, f := range formats {
		newFormat := f.new
		connectors.Register(connectors.Spec{
			Kind:           f.kind,
			DisplayName:    f.name,
			CanWrite:       true,
			Fields:         []connectors.Field{{Name: "path", Type: "path", Required: true}},
			LocationFields: []connectors.Field{{Name: "folder", Type: "string"}},
			New: func(raw json.RawMessage, _ secrets.String) (connectors.Connector, error) {
				var c Config
				if len(raw) > 0 {
					if err := json.Unmarshal(raw, &c); err != nil {
						return nil, fmt.Errorf("invalid folder connection settings: %w", err)
					}
				}
				return New(c, newFormat)
			},
		})
	}
}

// Config is the stored connection configuration.
type Config struct {
	// Path is the export folder on disk (in Docker: a mounted volume path).
	Path string `json:"path"`
}

// Location is where a flow target writes inside the folder.
type Location struct {
	// Folder is relative to the export folder ("" = the folder itself).
	Folder string `json:"folder,omitempty"`
}

// Folder is a configured export folder; it is a Target.
type Folder struct {
	path      string
	newFormat func() filetree.Format
}

var _ connectors.Target = (*Folder)(nil)

// New builds a folder connection writing with newFormat's layout.
func New(c Config, newFormat func() filetree.Format) (*Folder, error) {
	if strings.TrimSpace(c.Path) == "" {
		return nil, errors.New("the folder is missing: enter the folder on this machine to export into")
	}
	p, err := filepath.Abs(c.Path)
	if err != nil {
		return nil, err
	}
	return &Folder{path: p, newFormat: newFormat}, nil
}

// Test checks that the folder exists and can be written.
func (f *Folder) Test(_ context.Context) error {
	fi, err := os.Stat(f.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("the folder %s does not exist. Create it first; in Docker, check that it is mounted as a volume", f.path)
	case err != nil:
		return fmt.Errorf("cannot open %s: %w", f.path, err)
	case !fi.IsDir():
		return fmt.Errorf("%s is a file, not a folder. Enter a folder", f.path)
	}
	probe, err := os.CreateTemp(f.path, ".tolarix-test-*")
	if err != nil {
		return fmt.Errorf("cannot write in the folder %s; check its permissions: %w", f.path, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// Open starts a write session in a subfolder ("" = the folder itself).
func (f *Folder) Open(ctx context.Context, location json.RawMessage) (connectors.Session, error) {
	var loc Location
	if len(location) > 0 && string(location) != "null" {
		if err := json.Unmarshal(location, &loc); err != nil {
			return nil, fmt.Errorf("invalid target folder settings: %w", err)
		}
	}
	root, err := cleanRel(loc.Folder)
	if err != nil {
		return nil, fmt.Errorf("target folder %q: %w", loc.Folder, err)
	}
	if err := f.Test(ctx); err != nil {
		return nil, err
	}
	return filetree.NewSession(filetree.Dir{Path: f.path}, root, f.newFormat()), nil
}

// cleanRel normalises a folder-relative path and rejects escapes.
func cleanRel(p string) (string, error) {
	p = strings.Trim(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"), "/")
	if p == "" || p == "." {
		return "", nil
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", errors.New("must stay inside the folder")
	}
	return c, nil
}

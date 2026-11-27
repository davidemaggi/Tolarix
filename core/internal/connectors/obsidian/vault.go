// Package obsidian reads and writes Obsidian vaults (a folder of Markdown files).
//
// Hierarchy convention (shared with Azure DevOps wikis): a page with children
// is "Parent.md" next to a folder "Parent/" holding the children. A folder
// without a note is a placeholder page that only groups its children.
package obsidian

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the stored connection configuration.
type Config struct {
	// Path is the vault folder on disk (in Docker: the mounted volume path).
	Path string `json:"path"`
}

// Location is where a flow target writes inside the vault.
type Location struct {
	// Folder is vault-relative ("" = vault root), using forward slashes.
	Folder string `json:"folder"`
}

// ArchiveFolder holds archived mirror pages, inside the target folder.
const ArchiveFolder = "Archived"

// defaultAttachmentFolder is used when the vault has no attachment setting.
const defaultAttachmentFolder = "attachments"

func parseConfig(raw json.RawMessage) (Config, error) {
	var c Config
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, fmt.Errorf("invalid Obsidian connection settings: %w", err)
		}
	}
	if strings.TrimSpace(c.Path) == "" {
		return c, errors.New("the vault folder is missing: enter the folder that contains your notes (the one with the .obsidian folder)")
	}
	return c, nil
}

func parseLocation(raw json.RawMessage) (Location, error) {
	var l Location
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &l); err != nil {
			return l, fmt.Errorf("invalid target folder settings: %w", err)
		}
	}
	f, err := cleanRel(l.Folder)
	if err != nil {
		return l, fmt.Errorf("target folder %q: %w", l.Folder, err)
	}
	l.Folder = f
	return l, nil
}

// cleanRel normalises a vault-relative path and rejects escapes.
func cleanRel(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return "", nil
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", errors.New("must stay inside the vault")
	}
	return c, nil
}

// appConfig is the subset of .obsidian/app.json Tolarix reads.
type appConfig struct {
	AttachmentFolderPath string `json:"attachmentFolderPath"`
}

func readAppConfig(vault string) appConfig {
	var c appConfig
	b, err := os.ReadFile(filepath.Join(vault, ".obsidian", "app.json")) //nolint:gosec // inside the configured vault
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// vaultFile is one file found while walking the vault.
type vaultFile struct {
	Rel  string // vault-relative, forward slashes, with extension
	Size int64
}

// walkVault lists notes (.md) and other files, skipping hidden folders and files.
func walkVault(root string) (notes, files []vaultFile, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		vf := vaultFile{Rel: filepath.ToSlash(rel), Size: info.Size()}
		if strings.EqualFold(path.Ext(name), ".md") {
			notes = append(notes, vf)
		} else {
			files = append(files, vf)
		}
		return nil
	})
	sort.Slice(notes, func(i, j int) bool { return notes[i].Rel < notes[j].Rel })
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return notes, files, err
}

// abs joins a vault-relative path to the vault root.
func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func trimMD(p string) string {
	if strings.EqualFold(path.Ext(p), ".md") {
		return p[:len(p)-3]
	}
	return p
}

var mediaTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".svg": "image/svg+xml", ".webp": "image/webp", ".bmp": "image/bmp", ".avif": "image/avif",
	".pdf": "application/pdf", ".drawio": "application/vnd.jgraph.mxfile",
	".mp4": "video/mp4", ".webm": "video/webm", ".mp3": "audio/mpeg", ".wav": "audio/wav",
	".zip": "application/zip", ".txt": "text/plain", ".csv": "text/csv", ".json": "application/json",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// mediaType is deterministic across OSes (mime.TypeByExtension reads the OS registry).
func mediaType(name string) string {
	if t, ok := mediaTypes[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return "application/octet-stream"
}

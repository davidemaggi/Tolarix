package azuredevops

import (
	"context"
	"crypto/sha1" //nolint:gosec // git object ids are SHA-1 by definition
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/convert/frontmatter"
	"github.com/davidemaggi/tolarix/core/internal/convert/mdscan"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// ArchiveFolder holds archived mirror pages, inside the target folder.
const ArchiveFolder = "Archived"

// attachmentFolder holds a mirror's attachments, inside the target folder.
const attachmentFolder = ".attachments"

// Open starts a write session in a folder of the wiki.
func (r *Repo) Open(ctx context.Context, location json.RawMessage) (connectors.Session, error) {
	var loc Location
	if len(location) > 0 && string(location) != "null" {
		if err := json.Unmarshal(location, &loc); err != nil {
			return nil, fmt.Errorf("invalid target folder settings: %w", err)
		}
	}
	root := cleanFolder(loc.Folder)
	if root == ".." || strings.HasPrefix(root, "../") {
		return nil, fmt.Errorf("the target folder %q must stay inside the wiki", loc.Folder)
	}
	if err := r.resolve(ctx); err != nil {
		return nil, err
	}
	return &session{r: r, root: root}, nil
}

// entry is a file in the session's view of the target folder.
type entry struct {
	id      string // git object id
	content []byte // new content held in memory (pages, .order)
	spool   string // new content spooled to disk (attachments)
	size    int64
	origin  string // unchanged content: the path it has in the base commit
}

// session buffers every write in a virtual copy of the target folder and
// pushes the difference in Finish. Keys and paths are relative to the wiki
// root; pages are "A/B.md", folder-only pages "A/".
type session struct {
	r    *Repo
	root string // target folder, relative to the wiki root

	loaded bool
	commit string            // base commit ("" = branch does not exist yet)
	base   map[string]string // path -> object id in the base commit
	cur    map[string]*entry // path -> file after this session's writes
	tmp    string
	order  map[string][]string // folder -> child names in Master order (from Layout)
}

var (
	_ connectors.Session   = (*session)(nil)
	_ connectors.Buffering = (*session)(nil)
)

func (s *session) BuffersWrites() bool { return true }

// load reads the target folder at the branch head once per session.
func (s *session) load(ctx context.Context) error {
	if s.loaded {
		return nil
	}
	commit, err := s.r.head(ctx)
	if err != nil {
		return err
	}
	s.commit, s.base, s.cur = commit, map[string]string{}, map[string]*entry{}
	if commit != "" {
		items, err := s.r.items(ctx, commit, joinRel(s.r.addr.Folder, s.root), true)
		if err != nil && !errors.Is(err, errNoFolder) {
			return err
		}
		prefix := repoPath(s.r.addr.Folder)
		for _, it := range items {
			if it.IsFolder || it.GitObjectType == "tree" {
				continue
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(it.Path, prefix), "/")
			s.base[rel] = it.ObjectID
			s.cur[rel] = &entry{id: it.ObjectID, origin: rel}
		}
	}
	s.loaded = true
	return nil
}

// ---- layout and encoding ----

func (s *session) Layout(tree *model.Tree, w connectors.Warner) *connectors.Layout {
	l := &connectors.Layout{Pages: map[string]connectors.Placement{}, Attachments: map[string]string{}}
	s.order = map[string][]string{}
	var place func(parentID, dir, parentKey string, reserved []string)
	place = func(parentID, dir, parentKey string, reserved []string) {
		used := map[string]bool{}
		for _, r := range reserved {
			used[strings.ToLower(r)] = true
		}
		for _, c := range tree.Children(parentID) {
			want := firstNonEmpty(c.Name, c.Title, "Untitled")
			name := uniqueName(EncodeName(want), used)
			if DecodeName(name) != strings.TrimSpace(want) {
				w.Warn(connectors.Warning{PageID: c.ID, Code: "renamed",
					Message: fmt.Sprintf("the page %q is saved as %q because the original name is not allowed or already taken", c.Title, name)})
			}
			rel := joinRel(dir, name)
			key := rel + ".md"
			if c.Placeholder {
				key = rel + "/"
			}
			s.order[dir] = append(s.order[dir], name)
			l.Pages[c.ID] = connectors.Placement{Key: key, ParentKey: parentKey, Title: c.Title}
			place(c.ID, rel, key, nil)
		}
	}
	place("", s.root, "", []string{ArchiveFolder, attachmentFolder})

	used := map[string]string{} // lower(name) -> hash
	tree.Walk(func(p *model.Page, _ int) {
		for _, h := range p.Attachments {
			a := tree.Attachments[h]
			if _, done := l.Attachments[h]; done || a == nil {
				continue
			}
			name := attachmentName(a.Name)
			if other, taken := used[strings.ToLower(name)]; taken && other != h {
				ext := path.Ext(name)
				name = strings.TrimSuffix(name, ext) + "-" + h[:8] + ext
			}
			used[strings.ToLower(name)] = h
			l.Attachments[h] = joinRel(s.root, attachmentFolder+"/"+name)
		}
	})
	return l
}

func (s *session) Encode(_ *model.Tree, p *model.Page, l *connectors.Layout, w connectors.Warner) ([]byte, error) {
	if p.Placeholder {
		return nil, nil
	}
	fm, err := frontmatter.Block(p.Meta)
	if err != nil {
		return nil, fmt.Errorf("page %q: front matter: %w", p.ID, err)
	}
	body := mdscan.ReplaceFunc(p.Body, mdscan.MarkdownLink, func(m []string) (string, bool) {
		return encodeLink(p, l, w, m[1] == "!", m[2], m[3], m[4])
	})
	return append(fm, encodeBlocks(body)...), nil
}

func encodeLink(p *model.Page, l *connectors.Layout, w connectors.Warner, image bool, text, rawURL, title string) (string, bool) {
	u := strings.TrimSuffix(strings.TrimPrefix(rawURL, "<"), ">")
	if id, heading, ok := model.ParsePageLink(u); ok {
		pl, found := l.Pages[id]
		if !found || strings.HasSuffix(pl.Key, "/") {
			code, why := "link-out-of-scope", "is not part of this mirror"
			if found {
				code, why = "link-to-folder", "is a folder without content in this mirror"
			}
			w.Warn(connectors.Warning{PageID: p.ID, Code: code,
				Message: fmt.Sprintf("the link %q points to a page that %s; it is shown as plain text", text, why)})
			return text, true
		}
		dest := "/" + linkPath(trimMD(pl.Key))
		if heading != "" {
			if id == p.ID {
				dest = ""
			}
			dest += "#" + slug(heading)
		}
		return "[" + text + "](" + dest + titleSuffix(title) + ")", true
	}
	if hash, ok := model.ParseAttachmentLink(u); ok {
		key, found := l.Attachments[hash]
		if !found {
			w.Warn(connectors.Warning{PageID: p.ID, Code: "attachment-not-mirrored",
				Message: fmt.Sprintf("the attachment %q is not copied to this mirror; only its name is shown", text)})
			return text, true
		}
		dest := "/" + linkPath(key)
		if image {
			if width, ok := strings.CutPrefix(title, "width="); ok && width != "" {
				if !strings.Contains(width, "x") {
					width += "x"
				}
				return "![" + text + "](" + dest + " =" + width + ")", true
			}
			return "![" + text + "](" + dest + titleSuffix(title) + ")", true
		}
		return "[" + text + "](" + dest + titleSuffix(title) + ")", true
	}
	return "", false
}

func titleSuffix(t string) string {
	if t == "" {
		return ""
	}
	return ` "` + t + `"`
}

func (s *session) ContentHash(content []byte) string { return blobSHA(content) }

// ---- reads ----

// Inventory lists pages ("x.md") and folders ("x/") under the target folder.
func (s *session) Inventory(ctx context.Context) (map[string]connectors.MirrorItem, error) {
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	out := map[string]connectors.MirrorItem{}
	for p, id := range s.base {
		rel := strings.TrimPrefix(strings.TrimPrefix(p, s.root), "/")
		if hidden(rel) {
			continue
		}
		if isMD(p) {
			out[p] = connectors.MirrorItem{ID: p, Key: p, Hash: id}
		}
		for d := parentDir(p); d != "" && d != s.root; d = parentDir(d) {
			out[d+"/"] = connectors.MirrorItem{ID: d + "/", Key: d + "/"}
		}
	}
	return out, nil
}

func (s *session) HasAttachment(ctx context.Context, key string) (bool, error) {
	if err := s.load(ctx); err != nil {
		return false, err
	}
	_, ok := s.cur[key]
	return ok, nil
}

// ---- buffered writes ----

func (s *session) inRoot(key string) error {
	if s.root != "" && !strings.HasPrefix(key, s.root+"/") {
		return fmt.Errorf("refusing to write outside the target folder: %q", key)
	}
	if c := path.Clean(key); c != strings.TrimSuffix(key, "/") || strings.HasPrefix(c, "../") {
		return fmt.Errorf("invalid path %q", key)
	}
	return nil
}

func (s *session) WritePage(ctx context.Context, w connectors.PageWrite) (connectors.MirrorItem, error) {
	if err := s.load(ctx); err != nil {
		return connectors.MirrorItem{}, err
	}
	key := w.Placement.Key
	if err := s.inRoot(key); err != nil {
		return connectors.MirrorItem{}, err
	}
	// Moved, or a page that became folder-only: drop the old file.
	if w.Existing != nil && w.Existing.Key != key && !strings.HasSuffix(w.Existing.Key, "/") {
		delete(s.cur, w.Existing.Key)
	}
	if w.Placeholder {
		return connectors.MirrorItem{ID: key, Key: key}, nil
	}
	content := w.Content
	if content == nil {
		content = []byte{} // an empty page is still new content
	}
	id := blobSHA(content)
	s.cur[key] = &entry{id: id, content: content, size: int64(len(content))}
	return connectors.MirrorItem{ID: key, Key: key, Hash: id}, nil
}

func (s *session) DeletePage(ctx context.Context, item connectors.MirrorItem) error {
	if err := s.load(ctx); err != nil {
		return err
	}
	if !strings.HasSuffix(item.Key, "/") {
		delete(s.cur, item.Key) // a folder disappears with its last file
	}
	return nil
}

// ArchivePage moves a page under <target>/Archived/, keeping its relative path.
func (s *session) ArchivePage(ctx context.Context, item connectors.MirrorItem) (connectors.MirrorItem, error) {
	if err := s.load(ctx); err != nil {
		return item, err
	}
	if strings.HasSuffix(item.Key, "/") {
		return item, nil
	}
	archive := joinRel(s.root, ArchiveFolder)
	if strings.HasPrefix(item.Key, archive+"/") {
		return item, nil
	}
	e, ok := s.cur[item.Key]
	if !ok {
		return item, fmt.Errorf("archive %s: the page is no longer in the wiki", item.Key)
	}
	key := joinRel(archive, strings.TrimPrefix(strings.TrimPrefix(item.Key, s.root), "/"))
	delete(s.cur, item.Key)
	s.cur[key] = e
	return connectors.MirrorItem{ID: key, Key: key, Hash: e.id}, nil
}

func (s *session) PutAttachment(ctx context.Context, key string, r io.Reader) error {
	if err := s.load(ctx); err != nil {
		return err
	}
	if err := s.inRoot(key); err != nil {
		return err
	}
	if s.tmp == "" {
		dir, err := os.MkdirTemp("", "tolarix-ado-*")
		if err != nil {
			return err
		}
		s.tmp = dir
	}
	f, err := os.CreateTemp(s.tmp, "att-*")
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
	id, err := fileBlobSHA(f.Name(), size)
	if err != nil {
		return err
	}
	s.cur[key] = &entry{id: id, spool: f.Name(), size: size}
	return nil
}

func fileBlobSHA(name string, size int64) (string, error) {
	f, err := os.Open(name) //nolint:gosec // our own temp file
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New() //nolint:gosec // see import
	fmt.Fprintf(h, "blob %d\x00", size)
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *session) DeleteAttachment(ctx context.Context, key string) error {
	if err := s.load(ctx); err != nil {
		return err
	}
	delete(s.cur, key)
	return nil
}

// Finish rewrites the .order files and pushes every change as one commit.
func (s *session) Finish(ctx context.Context, commit bool) error {
	defer func() {
		if s.tmp != "" {
			_ = os.RemoveAll(s.tmp)
			s.tmp = ""
		}
	}()
	if !commit || !s.loaded {
		return nil
	}
	s.writeOrders()
	changes, summary := s.diff(ctx)
	if len(changes) == 0 {
		return nil
	}
	head, err := s.r.push(ctx, s.commit, "Tolarix: update the mirror ("+summary+")", changes)
	if err != nil {
		return err
	}
	s.commit = head
	return nil
}

// writeOrders gives every folder of the target a .order file listing its
// pages in Master order; pages Tolarix does not manage follow alphabetically.
func (s *session) writeOrders() {
	children := map[string]map[string]bool{}
	addChild := func(dir, name string) {
		if children[dir] == nil {
			children[dir] = map[string]bool{}
		}
		children[dir][name] = true
	}
	inTarget := func(d string) bool { return d == s.root || s.root == "" || strings.HasPrefix(d, s.root+"/") }
	for p := range s.cur {
		rel := strings.TrimPrefix(strings.TrimPrefix(p, s.root), "/")
		if hidden(rel) {
			continue
		}
		if isMD(p) {
			addChild(parentDir(p), path.Base(trimMD(p)))
		}
		for d := parentDir(p); d != "" && d != s.root && inTarget(d); d = parentDir(d) {
			addChild(parentDir(d), path.Base(d))
		}
	}
	for dir, set := range children {
		if !inTarget(dir) {
			continue
		}
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sortByOrder(names, s.order[dir])
		content := []byte(strings.Join(names, "\n"))
		key := joinRel(dir, ".order")
		if e, ok := s.cur[key]; !ok || e.id != blobSHA(content) {
			s.cur[key] = &entry{id: blobSHA(content), content: content, size: int64(len(content))}
		}
	}
	for p := range s.cur {
		if path.Base(p) == ".order" && children[parentDir(p)] == nil && inTarget(parentDir(p)) {
			delete(s.cur, p)
		}
	}
}

// diff compares the session's view with the base commit.
func (s *session) diff(ctx context.Context) ([]change, string) {
	full := func(rel string) string { return repoPath(s.r.addr.Folder, rel) }
	paths := make([]string, 0, len(s.cur))
	for p := range s.cur {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	renamed := map[string]bool{}
	var dels, renames, writes []change
	var added, edited int
	for _, p := range paths {
		e := s.cur[p]
		baseID, inBase := s.base[p]
		if inBase && baseID == e.id {
			continue
		}
		if e.content == nil && e.spool == "" && e.origin != "" && !inBase {
			if _, still := s.cur[e.origin]; !still && !renamed[e.origin] {
				renamed[e.origin] = true
				renames = append(renames, change{Type: "rename", From: full(e.origin), Path: full(p)})
				continue
			}
		}
		c := change{Type: "add", Path: full(p), Size: e.size, Content: s.contentOf(ctx, e)}
		if inBase {
			c.Type = "edit"
			edited++
		} else {
			added++
		}
		writes = append(writes, c)
	}
	var removed []string
	for p := range s.base {
		if _, ok := s.cur[p]; !ok && !renamed[p] {
			removed = append(removed, p)
		}
	}
	sort.Strings(removed)
	for _, p := range removed {
		dels = append(dels, change{Type: "delete", Path: full(p)})
	}
	all := append(append(dels, renames...), writes...)
	summary := fmt.Sprintf("%d added, %d changed, %d moved, %d removed", added, edited, len(renames), len(dels))
	return all, summary
}

func (s *session) contentOf(ctx context.Context, e *entry) func() ([]byte, error) {
	switch {
	case e.content != nil:
		return func() ([]byte, error) { return e.content, nil }
	case e.spool != "":
		return func() ([]byte, error) { return os.ReadFile(e.spool) }
	default:
		return func() ([]byte, error) {
			b, err := s.r.readBlob(ctx, e.id)
			if err == nil && blobSHA(b) != e.id {
				err = fmt.Errorf("the file %s changed while reading it", e.origin)
			}
			return b, err
		}
	}
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

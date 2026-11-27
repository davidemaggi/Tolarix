package azuredevops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/connectors/filetree"
	"github.com/davidemaggi/tolarix/core/internal/convert/frontmatter"
	"github.com/davidemaggi/tolarix/core/internal/convert/mdscan"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// ArchiveFolder holds archived mirror pages, inside the target folder.
const ArchiveFolder = filetree.ArchiveFolder

// attachmentFolder holds a mirror's attachments, inside the target folder.
const attachmentFolder = ".attachments"

// Open starts a write session in a folder of the wiki. Every write is
// buffered and pushed as one commit in Finish.
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
	var branch string
	if strings.TrimSpace(loc.Branch) != "" {
		if r.project {
			return nil, errors.New("a project wiki is always on the branch wikiMaster; leave the branch empty")
		}
		b, err := CleanBranch(loc.Branch)
		if err != nil {
			return nil, err
		}
		branch = b
	}
	if err := r.resolve(ctx); err != nil {
		return nil, err
	}
	if branch == "" {
		branch = r.addr.Branch
	}
	return filetree.NewSession(&gitStore{r: r, branch: branch}, root, NewFormat()), nil
}

// gitStore is the wiki folder at the head of a branch; Apply pushes one
// commit. Paths are relative to the wiki root. A branch that does not exist
// starts from the connection's branch, and is created only when there is
// something to push (never in a dry-run).
type gitStore struct {
	r      *Repo
	branch string
	commit string // base commit ("" = nothing to start from)
	create bool   // branch is missing: create it at commit before pushing
}

func (g *gitStore) Load(ctx context.Context, root string) (map[string]string, error) {
	commit, err := g.r.headOf(ctx, g.branch)
	if err != nil {
		return nil, err
	}
	g.create = false
	if commit == "" && g.branch != g.r.addr.Branch {
		if commit, err = g.r.head(ctx); err != nil {
			return nil, err
		}
		g.create = commit != ""
	}
	g.commit = commit
	out := map[string]string{}
	if commit == "" {
		return out, nil
	}
	items, err := g.r.items(ctx, commit, joinRel(g.r.addr.Folder, root), true)
	if err != nil && !errors.Is(err, errNoFolder) {
		return nil, err
	}
	prefix := repoPath(g.r.addr.Folder)
	for _, it := range items {
		if it.IsFolder || it.GitObjectType == "tree" {
			continue
		}
		out[strings.TrimPrefix(strings.TrimPrefix(it.Path, prefix), "/")] = it.ObjectID
	}
	return out, nil
}

func (g *gitStore) Read(ctx context.Context, _, id string) ([]byte, error) {
	return g.r.readBlob(ctx, id)
}

func (g *gitStore) Apply(ctx context.Context, _ string, changes []filetree.Change, summary string) error {
	full := func(rel string) string {
		if rel == "" {
			return ""
		}
		return repoPath(g.r.addr.Folder, rel)
	}
	cs := make([]change, len(changes))
	for i, c := range changes {
		cs[i] = change{Type: c.Type, Path: full(c.Path), From: full(c.From), Size: c.Size, Content: c.Content}
	}
	if g.create {
		if err := g.r.createBranch(ctx, g.branch, g.commit); err != nil {
			return err
		}
		g.create = false
	}
	head, err := g.r.push(ctx, g.branch, g.commit, "Tolarix: update the mirror ("+summary+")", cs)
	if err != nil {
		return err
	}
	g.commit = head
	return nil
}

// Format is the Azure DevOps wiki layout: page files named after their title
// ("Getting-Started.md"), a folder of the same name for children, .order
// files for the page order, attachments in .attachments and absolute links
// ("/Guide/Getting-Started"). Pages and folders live in a git repository or,
// for an export, in a local folder.
type Format struct {
	root  string
	order map[string][]string // folder -> child names in Master order
}

var _ filetree.Format = (*Format)(nil)

// NewFormat returns the layout state for one session.
func NewFormat() *Format { return &Format{} }

func (f *Format) Hidden(rel string) bool { return hidden(rel) }

func (f *Format) Layout(tree *model.Tree, root string, w connectors.Warner) *connectors.Layout {
	f.root = root
	l := &connectors.Layout{Pages: map[string]connectors.Placement{}, Attachments: map[string]string{}}
	f.order = map[string][]string{}
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
			f.order[dir] = append(f.order[dir], name)
			l.Pages[c.ID] = connectors.Placement{Key: key, ParentKey: parentKey, Title: c.Title}
			place(c.ID, rel, key, nil)
		}
	}
	place("", root, "", []string{ArchiveFolder, attachmentFolder})

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
			l.Attachments[h] = joinRel(root, attachmentFolder+"/"+name)
		}
	})
	return l
}

func (f *Format) Encode(p *model.Page, l *connectors.Layout, w connectors.Warner) ([]byte, error) {
	fm, err := frontmatter.Block(p.Meta)
	if err != nil {
		return nil, fmt.Errorf("page %q: front matter: %w", p.ID, err)
	}
	body := mdscan.ReplaceFunc(p.Body, mdscan.MarkdownLink, func(m []string) (string, bool) {
		return encodeLink(p, l, w, m[1] == "!", m[2], m[3], m[4])
	})
	body = mdscan.ReplaceHTMLLinks(body, func(_, u string) (string, bool) {
		return htmlLinkDest(p, l, w, u)
	})
	return append(fm, encodeBlocks(body)...), nil
}

// htmlLinkDest is the destination of a link or image inside an HTML table.
func htmlLinkDest(p *model.Page, l *connectors.Layout, w connectors.Warner, u string) (string, bool) {
	if id, heading, ok := model.ParsePageLink(u); ok {
		pl, found := l.Pages[id]
		if !found || strings.HasSuffix(pl.Key, "/") {
			w.Warn(connectors.Warning{PageID: p.ID, Code: "link-out-of-scope",
				Message: "a link inside a table points to a page that is not part of this mirror; it is shown as plain text"})
			return "", false
		}
		dest := "/" + linkPath(trimMD(pl.Key))
		if heading != "" {
			if id == p.ID {
				dest = ""
			}
			dest += "#" + slug(heading)
		}
		return dest, true
	}
	if hash, ok := model.ParseAttachmentLink(u); ok {
		if key, found := l.Attachments[hash]; found {
			return "/" + linkPath(key), true
		}
		w.Warn(connectors.Warning{PageID: p.ID, Code: "attachment-not-mirrored",
			Message: "an attachment used inside a table is not copied to this mirror; only its description is shown"})
	}
	return "", false
}

func encodeLink(p *model.Page, l *connectors.Layout, w connectors.Warner, image bool, text, rawURL, title string) (string, bool) {
	u := strings.TrimSuffix(strings.TrimPrefix(rawURL, "<"), ">")
	if id, heading, ok := model.ParsePageLink(u); ok {
		if image { // a page embed: the wiki can only link to the page
			w.Warn(connectors.Warning{PageID: p.ID, Code: "page-embed",
				Message: fmt.Sprintf("the content of the page %q is embedded in the Master; this mirror shows a link to it instead", text)})
			title = ""
		}
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

// Finalize gives every folder of the target a .order file listing its pages
// in Master order; pages Tolarix does not manage follow alphabetically.
func (f *Format) Finalize(_ context.Context, t *filetree.Tree) error {
	root := f.root
	children := map[string]map[string]bool{}
	addChild := func(dir, name string) {
		if children[dir] == nil {
			children[dir] = map[string]bool{}
		}
		children[dir][name] = true
	}
	inTarget := func(d string) bool { return d == root || root == "" || strings.HasPrefix(d, root+"/") }
	paths := t.Paths()
	for _, p := range paths {
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		if hidden(rel) {
			continue
		}
		if isMD(p) {
			addChild(parentDir(p), path.Base(trimMD(p)))
		}
		for d := parentDir(p); d != "" && d != root && inTarget(d); d = parentDir(d) {
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
		sortByOrder(names, f.order[dir])
		t.Put(joinRel(dir, ".order"), []byte(strings.Join(names, "\n")))
	}
	for _, p := range paths {
		if path.Base(p) == ".order" && children[parentDir(p)] == nil && inTarget(parentDir(p)) {
			t.Delete(p)
		}
	}
	return nil
}

func firstNonEmpty(vs ...string) string { return filetree.FirstNonEmpty(vs...) }

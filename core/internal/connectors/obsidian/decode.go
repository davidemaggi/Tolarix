package obsidian

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/convert/frontmatter"
	"github.com/davidemaggi/tolarix/core/internal/convert/mdscan"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// index resolves link targets the way Obsidian does: exact path, path
// relative to the note, then by file name (shortest path wins).
type index struct {
	notes      map[string]string   // lower(id) -> id   (id = path without .md)
	notesByEnd map[string][]string // lower(base name) -> ids
	files      map[string]string   // lower(rel) -> rel
	filesByEnd map[string][]string // lower(base name) -> rels
}

func newIndex(notes, files []vaultFile) *index {
	ix := &index{
		notes: map[string]string{}, notesByEnd: map[string][]string{},
		files: map[string]string{}, filesByEnd: map[string][]string{},
	}
	for _, n := range notes {
		id := trimMD(n.Rel)
		ix.notes[strings.ToLower(id)] = id
		b := strings.ToLower(path.Base(id))
		ix.notesByEnd[b] = append(ix.notesByEnd[b], id)
	}
	for _, f := range files {
		ix.files[strings.ToLower(f.Rel)] = f.Rel
		b := strings.ToLower(path.Base(f.Rel))
		ix.filesByEnd[b] = append(ix.filesByEnd[b], f.Rel)
	}
	return ix
}

func resolveIn(target, fromDir string, exact map[string]string, byEnd map[string][]string) (string, bool) {
	t := strings.Trim(target, "/")
	if t == "" {
		return "", false
	}
	lt := strings.ToLower(path.Clean(t))
	if v, ok := exact[lt]; ok {
		return v, true
	}
	if fromDir != "" {
		if v, ok := exact[strings.ToLower(path.Clean(fromDir+"/"+t))]; ok {
			return v, true
		}
	}
	var cands []string
	for _, c := range byEnd[strings.ToLower(path.Base(t))] {
		lc := strings.ToLower(c)
		if lc == lt || strings.HasSuffix(lc, "/"+lt) {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return "", false
	}
	sort.Slice(cands, func(i, j int) bool {
		if len(cands[i]) != len(cands[j]) {
			return len(cands[i]) < len(cands[j])
		}
		return cands[i] < cands[j]
	})
	return cands[0], true
}

func (ix *index) note(target, fromDir string) (string, bool) {
	return resolveIn(trimMD(target), fromDir, ix.notes, ix.notesByEnd)
}

func (ix *index) file(target, fromDir string) (string, bool) {
	return resolveIn(target, fromDir, ix.files, ix.filesByEnd)
}

// snapshot reads the vault at root into a canonical tree.
func snapshot(ctx context.Context, root string, w connectors.Warner) (*model.Tree, error) {
	notes, files, err := walkVault(root)
	if err != nil {
		return nil, fmt.Errorf("read vault %s: %w", root, err)
	}
	ix := newIndex(notes, files)
	tree := model.NewTree()
	d := &decoder{root: root, ix: ix, tree: tree, w: w, fileSize: map[string]int64{}}
	for _, f := range files {
		d.fileSize[f.Rel] = f.Size
	}

	// Pages for notes, then placeholders for folders that hold notes but have no note of their own.
	for _, n := range notes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := trimMD(n.Rel)
		src, err := os.ReadFile(abs(root, n.Rel)) //nolint:gosec // inside the configured vault
		if err != nil {
			return nil, fmt.Errorf("read note %s: %w", n.Rel, err)
		}
		p := &model.Page{ID: id, ParentID: parentID(id), Name: path.Base(id), Title: path.Base(id)}
		d.decodeNote(p, string(src))
		tree.Add(p)
	}
	for _, n := range notes {
		for dir := parentID(trimMD(n.Rel)); dir != ""; dir = parentID(dir) {
			if _, ok := tree.Pages[dir]; ok {
				break
			}
			tree.Add(&model.Page{ID: dir, ParentID: parentID(dir), Name: path.Base(dir), Title: path.Base(dir), Placeholder: true})
		}
	}
	assignOrder(tree)
	return tree, nil
}

func parentID(id string) string {
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	return ""
}

// assignOrder numbers siblings alphabetically (Obsidian's default file order).
func assignOrder(t *model.Tree) {
	byParent := map[string][]*model.Page{}
	for _, p := range t.Pages {
		byParent[p.ParentID] = append(byParent[p.ParentID], p)
	}
	for _, sib := range byParent {
		sort.Slice(sib, func(i, j int) bool {
			a, b := strings.ToLower(sib[i].Name), strings.ToLower(sib[j].Name)
			if a != b {
				return a < b
			}
			return sib[i].Name < sib[j].Name
		})
		for i, p := range sib {
			p.Order = i
		}
	}
}

type decoder struct {
	root     string
	ix       *index
	tree     *model.Tree
	w        connectors.Warner
	fileSize map[string]int64
}

func (d *decoder) warn(p *model.Page, code, format string, a ...any) {
	d.w.Warn(connectors.Warning{PageID: p.ID, Code: code, Message: fmt.Sprintf(format, a...)})
}

func (d *decoder) decodeNote(p *model.Page, src string) {
	body := src
	if fm, rest, ok := mdscan.SplitFrontMatter(src); ok {
		meta, err := frontmatter.Parse(fm)
		if err != nil {
			d.warn(p, "frontmatter-invalid", "the front matter (properties) is not valid YAML and was kept as text: %v", err)
		} else {
			p.Meta = meta
			body = rest
		}
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	dir := parentID(p.ID)
	body = mdscan.ReplaceFunc(body, mdscan.WikiLink, func(m []string) (string, bool) {
		return d.wikiLink(p, dir, m[1] == "!", m[2]), true
	})
	body = mdscan.ReplaceFunc(body, mdscan.MarkdownLink, func(m []string) (string, bool) {
		return d.markdownLink(p, dir, m[1] == "!", m[2], m[3], m[4])
	})
	p.Body = body
}

var widthRe = regexp.MustCompile(`^\d+(x\d+)?$`)

// wikiLink converts [[target#heading|alias]] / ![[...]] to canonical Markdown.
func (d *decoder) wikiLink(p *model.Page, dir string, embed bool, inner string) string {
	// Inside tables the alias separator is written as \|.
	inner = strings.ReplaceAll(inner, `\|`, "|")
	target, alias, hasAlias := strings.Cut(inner, "|")
	target = strings.TrimSpace(target)
	file, heading, _ := strings.Cut(target, "#")
	if i := strings.LastIndexByte(heading, '#'); i >= 0 {
		heading = heading[i+1:] // [[Note#H1#H2]] links to H2
	}
	text := alias
	if !hasAlias {
		text = path.Base(trimMD(file))
		if heading != "" {
			if file == "" {
				text = heading
			} else {
				text += " > " + heading
			}
		}
	}
	// Same-page heading link.
	if file == "" {
		return "[" + escapeLinkText(text) + "](" + model.PageLink(p.ID, heading) + ")"
	}
	if id, ok := d.ix.note(file, dir); ok && !looksLikeFile(file) {
		if embed {
			d.warn(p, "note-embed", "the embedded note %q is shown as a link in mirrors", file)
		}
		return "[" + escapeLinkText(text) + "](" + model.PageLink(id, heading) + ")"
	}
	if rel, ok := d.ix.file(file, dir); ok {
		hash, err := d.attachment(p, rel)
		if err != nil {
			d.warn(p, "attachment-unreadable", "cannot read attachment %q: %v", rel, err)
			return text
		}
		if embed {
			alt, title := path.Base(rel), ""
			if hasAlias {
				if widthRe.MatchString(alias) {
					title = ` "width=` + alias + `"`
				} else {
					alt = alias
				}
			}
			return "![" + escapeLinkText(alt) + "](" + model.AttachmentLink(hash) + title + ")"
		}
		if !hasAlias {
			text = path.Base(rel)
		}
		return "[" + escapeLinkText(text) + "](" + model.AttachmentLink(hash) + ")"
	}
	d.warn(p, "broken-link", "the link [[%s]] points to a note or file that does not exist; it is kept as plain text", inner)
	return text
}

// looksLikeFile reports whether a wikilink target names a non-note file (has
// an extension other than .md).
func looksLikeFile(target string) bool {
	ext := path.Ext(target)
	return ext != "" && !strings.EqualFold(ext, ".md") && len(ext) <= 6 && !strings.Contains(ext, " ")
}

// markdownLink converts relative [text](url) / ![alt](url) links. External
// URLs and canonical links are left untouched.
func (d *decoder) markdownLink(p *model.Page, dir string, image bool, text, rawURL, title string) (string, bool) {
	u := strings.TrimSuffix(strings.TrimPrefix(rawURL, "<"), ">")
	if u == "" || hasScheme(u) {
		return "", false
	}
	target, frag, _ := strings.Cut(u, "#")
	if t, err := url.PathUnescape(target); err == nil {
		target = t
	}
	if f, err := url.PathUnescape(frag); err == nil {
		frag = f
	}
	titlePart := ""
	if title != "" {
		titlePart = ` "` + title + `"`
	}
	if target == "" {
		return "[" + text + "](" + model.PageLink(p.ID, frag) + ")", true
	}
	if !looksLikeFile(target) {
		if id, ok := d.ix.note(target, dir); ok {
			return "[" + text + "](" + model.PageLink(id, frag) + titlePart + ")", true
		}
	}
	if rel, ok := d.ix.file(target, dir); ok {
		hash, err := d.attachment(p, rel)
		if err != nil {
			d.warn(p, "attachment-unreadable", "cannot read attachment %q: %v", rel, err)
			return "", false
		}
		bang := ""
		if image {
			bang = "!"
		}
		return bang + "[" + text + "](" + model.AttachmentLink(hash) + titlePart + ")", true
	}
	d.warn(p, "broken-link", "the link to %q points to a note or file that does not exist; it is kept unchanged", u)
	return "", false
}

var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func hasScheme(u string) bool { return schemeRe.MatchString(u) }

// attachment registers a vault file as an attachment of p and returns its hash.
func (d *decoder) attachment(p *model.Page, rel string) (string, error) {
	f, err := os.Open(abs(d.root, rel)) //nolint:gosec // inside the configured vault
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash, size, err := model.HashReader(f)
	if err != nil {
		return "", err
	}
	if _, ok := d.tree.Attachments[hash]; !ok {
		d.tree.Attachments[hash] = &model.Attachment{
			Hash: hash, Name: path.Base(rel), Size: size, MediaType: mediaType(rel), SourceRef: rel,
		}
	}
	for _, h := range p.Attachments {
		if h == hash {
			return hash, nil
		}
	}
	p.Attachments = append(p.Attachments, hash)
	return hash, nil
}

// escapeLinkText escapes brackets so generated link text cannot break the link.
func escapeLinkText(s string) string {
	return strings.NewReplacer(`[`, `\[`, `]`, `\]`).Replace(s)
}

package folder

import (
	"context"
	"fmt"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/connectors/filetree"
	"github.com/davidemaggi/tolarix/core/internal/convert/frontmatter"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Markdown layout, for static site generators (MkDocs, Docusaurus…), GitLab
// wikis or plain browsing in a repository: a page is "Name.md", a page with
// children is "Name/index.md" next to them, links are relative paths with
// the .md extension, attachments go into an assets folder and every page has
// a title in its front matter.
const (
	markdownIndex  = "index"
	markdownAssets = "assets"
)

// Markdown is the plain Markdown Format.
type Markdown struct{}

var _ filetree.Format = (*Markdown)(nil)

// markdownNameReplacer swaps characters file systems or links do not allow.
var markdownNameReplacer = strings.NewReplacer(
	`\`, "-", "/", "-", ":", "-", "*", "-", "?", "-", `"`, "'", "<", "(", ">", ")", "|", "-",
	"#", "-", "%", "-", "[", "(", "]", ")",
)

// MarkdownName is the file (or folder) name for a page title.
func MarkdownName(title string) string {
	s := strings.TrimSpace(markdownNameReplacer.Replace(cleanTitle(title)))
	s = strings.TrimLeft(strings.TrimRight(s, ". "), ". ") // no hidden files, no trailing dots (Windows)
	if s == "" {
		s = "Untitled"
	}
	if filetree.WindowsReserved(s) {
		s += "_"
	}
	return s
}

func (m *Markdown) Hidden(rel string) bool {
	return filetree.DotHidden(rel) || rel == markdownAssets || strings.HasPrefix(rel, markdownAssets+"/")
}

func (m *Markdown) Layout(tree *model.Tree, root string, w connectors.Warner) *connectors.Layout {
	l := &connectors.Layout{Pages: map[string]connectors.Placement{}, Attachments: map[string]string{}}
	var place func(parentID, dir, parentKey string, reserved []string)
	place = func(parentID, dir, parentKey string, reserved []string) {
		used := map[string]bool{}
		for _, r := range reserved {
			used[strings.ToLower(r)] = true
		}
		for _, c := range tree.Children(parentID) {
			want := cleanTitle(filetree.FirstNonEmpty(c.Title, c.Name, "Untitled"))
			name := filetree.UniqueName(MarkdownName(want), " ", used)
			if name != want {
				w.Warn(connectors.Warning{PageID: c.ID, Code: "renamed",
					Message: fmt.Sprintf("the page %q is saved as %q because the original name is not allowed in file names or is already taken", c.Title, name)})
			}
			rel := filetree.JoinRel(dir, name)
			hasChildren := len(tree.Children(c.ID)) > 0
			var key string
			var childReserved []string
			switch {
			case c.Placeholder:
				key = rel + "/"
			case hasChildren:
				key = rel + "/" + markdownIndex + ".md"
				childReserved = []string{markdownIndex}
			default:
				key = rel + ".md"
			}
			l.Pages[c.ID] = connectors.Placement{Key: key, ParentKey: parentKey, Title: c.Title}
			place(c.ID, rel, key, childReserved)
		}
	}
	place("", root, "", []string{filetree.ArchiveFolder, markdownAssets})
	layoutAttachments(tree, l, filetree.JoinRel(root, markdownAssets))
	return l
}

func (m *Markdown) Encode(p *model.Page, l *connectors.Layout, w connectors.Warner) ([]byte, error) {
	meta := p.Meta
	if !hasKey(meta, "title") && strings.TrimSpace(p.Title) != "" {
		meta = append([]model.MetaItem{{Key: "title", Value: p.Title}}, meta...)
	}
	fm, err := frontmatter.Block(meta)
	if err != nil {
		return nil, fmt.Errorf("page %q: front matter: %w", p.ID, err)
	}
	dir := filetree.ParentDir(l.Pages[p.ID].Key)
	rel := func(key string) string { return escapePath(relPath(dir, key)) }
	return append(fm, encodeBody(p, l, w, rel, rel)...), nil
}

func (m *Markdown) Finalize(context.Context, *filetree.Tree) error { return nil }

func hasKey(meta []model.MetaItem, key string) bool {
	for _, it := range meta {
		if strings.EqualFold(it.Key, key) {
			return true
		}
	}
	return false
}

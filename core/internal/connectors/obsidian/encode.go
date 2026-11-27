package obsidian

import (
	"bytes"
	"fmt"
	"path"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/connectors/filetree"
	"github.com/davidemaggi/tolarix/core/internal/convert/frontmatter"
	"github.com/davidemaggi/tolarix/core/internal/convert/mdscan"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// layout places pages under root as "Name.md" + "Name/" folders and decides
// where each attachment goes according to the vault's attachment setting.
func layout(tree *model.Tree, root string, attSetting string, w connectors.Warner) *connectors.Layout {
	l := &connectors.Layout{Pages: map[string]connectors.Placement{}, Attachments: map[string]string{}}
	attRoot := attachmentRootFolder(root, attSetting)

	var place func(parentID, dir, parentKey string, reserved []string)
	place = func(parentID, dir, parentKey string, reserved []string) {
		used := map[string]bool{}
		for _, r := range reserved {
			used[strings.ToLower(r)] = true
		}
		for _, c := range tree.Children(parentID) {
			want := filetree.CleanTitle(firstNonEmpty(c.Name, c.Title, "Untitled"))
			name := uniqueName(sanitizeName(want), used)
			if name != want {
				w.Warn(connectors.Warning{PageID: c.ID, Code: "renamed",
					Message: fmt.Sprintf("the page %q is saved as %q because the original name is not allowed or already taken", c.Title, name)})
			}
			rel := joinRel(dir, name)
			key := rel + ".md"
			if c.Placeholder {
				key = rel + "/"
			}
			l.Pages[c.ID] = connectors.Placement{Key: key, ParentKey: parentKey, Title: c.Title}
			place(c.ID, rel, key, nil)
		}
	}
	var reserved []string
	reserved = append(reserved, ArchiveFolder)
	if attRoot != "" && parentDir(attRoot) == root {
		reserved = append(reserved, path.Base(attRoot))
	}
	place("", root, "", reserved)

	// Attachments, in page order so placement is deterministic.
	usedIn := map[string]map[string]string{} // folder -> lower(name) -> hash
	tree.Walk(func(p *model.Page, _ int) {
		pageDir := parentDir(strings.TrimSuffix(l.Pages[p.ID].Key, "/"))
		for _, h := range p.Attachments {
			if _, done := l.Attachments[h]; done {
				continue
			}
			a := tree.Attachments[h]
			if a == nil {
				continue
			}
			folder := attachmentFolderFor(attRoot, attSetting, pageDir)
			if usedIn[folder] == nil {
				usedIn[folder] = map[string]string{}
			}
			name := sanitizeFileName(a.Name)
			if other, taken := usedIn[folder][strings.ToLower(name)]; taken && other != h {
				ext := path.Ext(name)
				name = strings.TrimSuffix(name, ext) + " (" + h[:8] + ")" + ext
			}
			usedIn[folder][strings.ToLower(name)] = h
			l.Attachments[h] = joinRel(folder, name)
		}
	})
	return l
}

// attachmentRootFolder returns the fixed attachment folder, or "" when
// attachments go next to notes ("./" settings).
func attachmentRootFolder(root, setting string) string {
	switch {
	case setting == "":
		return joinRel(root, defaultAttachmentFolder)
	case setting == "/":
		return ""
	case strings.HasPrefix(setting, "./"):
		return ""
	default:
		f, err := cleanRel(setting)
		if err != nil {
			return joinRel(root, defaultAttachmentFolder)
		}
		return f
	}
}

func attachmentFolderFor(attRoot, setting, pageDir string) string {
	if strings.HasPrefix(setting, "./") {
		sub, _ := cleanRel(strings.TrimPrefix(setting, "./"))
		return joinRel(pageDir, sub)
	}
	return attRoot
}

// encode renders a canonical page as an Obsidian note.
func encode(p *model.Page, l *connectors.Layout, w connectors.Warner) ([]byte, error) {
	if p.Placeholder {
		return nil, nil
	}
	var b bytes.Buffer
	fm, err := frontmatter.Block(p.Meta)
	if err != nil {
		return nil, fmt.Errorf("page %q: front matter: %w", p.ID, err)
	}
	b.Write(fm)
	body := mdscan.ReplaceFuncAt(p.Body, mdscan.MarkdownLink, func(pos int, m []string) (string, bool) {
		return encodeLink(p, l, w, m[1] == "!", m[2], m[3], m[4], mdscan.InTableRow(p.Body, pos))
	})
	// Links and images inside HTML tables: vault paths, which Obsidian
	// resolves for links; images there may not show (see the user guide).
	body = mdscan.ReplaceHTMLLinks(body, func(_, u string) (string, bool) {
		if id, heading, ok := model.ParsePageLink(u); ok {
			pl, found := l.Pages[id]
			if !found || strings.HasSuffix(pl.Key, "/") {
				w.Warn(connectors.Warning{PageID: p.ID, Code: "link-out-of-scope",
					Message: "a link inside a table points to a page that is not part of this mirror; it is shown as plain text"})
				return "", false
			}
			dest := pl.Key
			if heading != "" {
				dest += "#" + heading
			}
			return dest, true
		}
		if hash, ok := model.ParseAttachmentLink(u); ok {
			if key, found := l.Attachments[hash]; found {
				return key, true
			}
			w.Warn(connectors.Warning{PageID: p.ID, Code: "attachment-not-mirrored",
				Message: "an attachment used inside a table is not copied to this mirror; only its description is shown"})
		}
		return "", false
	})
	b.WriteString(body)
	return b.Bytes(), nil
}

func encodeLink(p *model.Page, l *connectors.Layout, w connectors.Warner, image bool, text, rawURL, title string, inTable bool) (string, bool) {
	u := strings.TrimSuffix(strings.TrimPrefix(rawURL, "<"), ">")
	sep := "|"
	if inTable {
		sep = `\|`
	}
	plain := unescapeLinkText(text)

	if id, heading, ok := model.ParsePageLink(u); ok {
		pl, found := l.Pages[id]
		if !found || strings.HasSuffix(pl.Key, "/") {
			code, why := "link-out-of-scope", "is not part of this mirror"
			if found {
				code, why = "link-to-folder", "is a folder without a note in this mirror"
			}
			w.Warn(connectors.Warning{PageID: p.ID, Code: code,
				Message: fmt.Sprintf("the link %q points to a page that %s; it is shown as plain text", plain, why)})
			return plain, true
		}
		base := path.Base(trimMD(pl.Key))
		target := trimMD(pl.Key)
		if id == p.ID && heading != "" {
			target = "" // same-page heading link: [[#Heading]]
		}
		if heading != "" {
			target += "#" + heading
		}
		// Omit the alias when it equals what Obsidian would display anyway.
		redundant := plain == "" ||
			(heading == "" && (plain == base || plain == pl.Title)) ||
			(heading != "" && (plain == base+" > "+heading || (id == p.ID && plain == heading)))
		if image { // a page embed
			return "![[" + target + "]]", true
		}
		if redundant {
			return "[[" + target + "]]", true
		}
		return "[[" + target + sep + plain + "]]", true
	}

	if hash, ok := model.ParseAttachmentLink(u); ok {
		key, found := l.Attachments[hash]
		if !found {
			w.Warn(connectors.Warning{PageID: p.ID, Code: "attachment-not-mirrored",
				Message: fmt.Sprintf("the attachment %q is not copied to this mirror; only its name is shown", plain)})
			return plain, true
		}
		if image {
			if width, ok := strings.CutPrefix(title, "width="); ok && width != "" {
				return "![[" + key + sep + width + "]]", true
			}
			return "![[" + key + "]]", true
		}
		if plain == "" || plain == path.Base(key) {
			return "[[" + key + "]]", true
		}
		return "[[" + key + sep + plain + "]]", true
	}
	return "", false
}

func unescapeLinkText(s string) string {
	return strings.NewReplacer(`\[`, `[`, `\]`, `]`).Replace(s)
}

// Characters Obsidian (or common file systems) do not allow in note names.
var nameReplacer = strings.NewReplacer(
	`\`, "-", `/`, "-", `:`, "-", `*`, "-", `?`, "-", `"`, "'", `<`, "(", `>`, ")", `|`, "-",
	`#`, "-", `^`, "-", `[`, "(", `]`, ")",
)

func sanitizeName(s string) string {
	s = filetree.CleanTitle(nameReplacer.Replace(s))
	s = strings.TrimRight(s, ". ") // Windows rejects trailing dots and spaces
	if s == "" {
		s = "Untitled"
	}
	if filetree.WindowsReserved(s) {
		// Windows also reserves "CON.anything": mark the part before the dot.
		if i := strings.IndexByte(s, '.'); i >= 0 {
			s = s[:i] + "_" + s[i:]
		} else {
			s += "_"
		}
	}
	return s
}

func sanitizeFileName(s string) string {
	ext := path.Ext(s)
	return sanitizeName(strings.TrimSuffix(s, ext)) + strings.ToLower(ext)
}

func uniqueName(base string, used map[string]bool) string {
	name := base
	for i := 2; used[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s (%d)", base, i)
	}
	used[strings.ToLower(name)] = true
	return name
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	if name == "" {
		return dir
	}
	return dir + "/" + name
}

func parentDir(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

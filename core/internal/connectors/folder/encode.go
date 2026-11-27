package folder

import (
	"fmt"
	"html"
	"path"
	"strings"
	"unicode"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/connectors/filetree"
	"github.com/davidemaggi/tolarix/core/internal/convert/mdscan"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// encodeBody rewrites the canonical links of a page body. pageDest and
// attDest turn a target key into a link destination.
func encodeBody(p *model.Page, l *connectors.Layout, w connectors.Warner, pageDest, attDest func(key string) string) string {
	body := mdscan.ReplaceFunc(p.Body, mdscan.MarkdownLink, func(m []string) (string, bool) {
		image, text, title := m[1] == "!", m[2], m[4]
		u := strings.TrimSuffix(strings.TrimPrefix(m[3], "<"), ">")
		if id, heading, ok := model.ParsePageLink(u); ok {
			if image { // a page embed: Markdown can only link to the page
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
			dest := pageDest(pl.Key)
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
			dest := attDest(key)
			if image {
				if size, ok := strings.CutPrefix(title, "width="); ok && size != "" {
					return imgTag(dest, text, size), true
				}
				return "![" + text + "](" + dest + titleSuffix(title) + ")", true
			}
			return "[" + text + "](" + dest + titleSuffix(title) + ")", true
		}
		return "", false
	})
	// Links and images inside HTML tables.
	return mdscan.ReplaceHTMLLinks(body, func(_, u string) (string, bool) {
		if id, heading, ok := model.ParsePageLink(u); ok {
			pl, found := l.Pages[id]
			if !found || strings.HasSuffix(pl.Key, "/") {
				w.Warn(connectors.Warning{PageID: p.ID, Code: "link-out-of-scope",
					Message: "a link inside a table points to a page that is not part of this mirror; it is shown as plain text"})
				return "", false
			}
			dest := pageDest(pl.Key)
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
				return attDest(key), true
			}
			w.Warn(connectors.Warning{PageID: p.ID, Code: "attachment-not-mirrored",
				Message: "an attachment used inside a table is not copied to this mirror; only its description is shown"})
		}
		return "", false
	})
}

// imgTag is an image with a size ("300" or "300x200"), which Markdown
// cannot express; GitHub and most Markdown renderers accept the HTML.
func imgTag(src, alt, size string) string {
	width, height, _ := strings.Cut(size, "x")
	out := `<img src="` + html.EscapeString(src) + `" alt="` + html.EscapeString(alt) + `"`
	if width != "" {
		out += ` width="` + html.EscapeString(width) + `"`
	}
	if height != "" {
		out += ` height="` + html.EscapeString(height) + `"`
	}
	return out + ">"
}

func titleSuffix(t string) string {
	if t == "" {
		return ""
	}
	return ` "` + t + `"`
}

// slug is the anchor GitHub (and most Markdown tools) give a heading: lower
// case, spaces as "-", punctuation dropped.
func slug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapePath escapes what would break a Markdown link destination.
var escapePath = strings.NewReplacer(
	"%", "%25", " ", "%20", "(", "%28", ")", "%29", "<", "%3C", ">", "%3E", "#", "%23", "?", "%3F",
).Replace

// relPath is the relative path from folder dir to the file target.
func relPath(dir, target string) string {
	from := strings.Split(dir, "/")
	if dir == "" {
		from = nil
	}
	to := strings.Split(target, "/")
	i := 0
	for i < len(from) && i < len(to)-1 && from[i] == to[i] {
		i++
	}
	parts := make([]string, 0, len(from)-i+len(to)-i)
	for range from[i:] {
		parts = append(parts, "..")
	}
	return strings.Join(append(parts, to[i:]...), "/")
}

// layoutAttachments places every attachment in folder dir under a safe,
// unique file name.
func layoutAttachments(tree *model.Tree, l *connectors.Layout, dir string) {
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
			l.Attachments[h] = filetree.JoinRel(dir, name)
		}
	})
}

var attachmentNameReplacer = strings.NewReplacer(
	" ", "-", "/", "-", `\`, "-", "#", "-", "?", "-", "%", "-", ":", "-", "*", "-",
	"<", "-", ">", "-", "|", "-", `"`, "-", "(", "-", ")", "-", "[", "-", "]", "-",
)

// attachmentName makes a file name safe for links and every file system.
func attachmentName(name string) string {
	ext := strings.ToLower(path.Ext(name))
	base := strings.Trim(attachmentNameReplacer.Replace(strings.TrimSuffix(name, path.Ext(name))), ".-_")
	if base == "" {
		base = "file"
	}
	if filetree.WindowsReserved(base) {
		base += "_"
	}
	return base + ext
}

// cleanTitle turns tabs and line breaks into spaces, drops other control
// characters and trims surrounding spaces.
func cleanTitle(s string) string { return filetree.CleanTitle(s) }

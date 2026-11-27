package storage

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Writer resolves canonical references while Markdown is converted to
// storage format.
type Writer interface {
	// SelfID is the canonical id of the page being written.
	SelfID() string
	// PageTitle is the title the page with canonical id has in the target.
	PageTitle(id string) (string, bool)
	// Attachment names the stored attachment with this content hash and the
	// title of the page holding it ("" = the page being written).
	Attachment(hash string) (filename, ownerTitle string, ok bool)
	// Warn reports a degradation.
	Warn(code, message string)
}

var gm = goldmark.New(goldmark.WithExtensions(extension.GFM))

// FromMarkdown converts canonical Markdown to a storage-format body.
func FromMarkdown(md string, w Writer) string {
	c := &fromMD{w: w}
	return c.convert([]byte(md))
}

type fromMD struct {
	w      Writer
	src    []byte
	b      strings.Builder
	taskID int
}

func (c *fromMD) convert(src []byte) string {
	sub := &fromMD{w: c.w, src: src, taskID: c.taskID}
	doc := gm.Parser().Parse(text.NewReader(src))
	sub.children(doc)
	c.taskID = sub.taskID
	return sub.b.String()
}

func (c *fromMD) children(n ast.Node) {
	excerpt := false
	for k := n.FirstChild(); k != nil; k = k.NextSibling() {
		if h, ok := k.(*ast.HTMLBlock); ok {
			line := strings.TrimSpace(c.lines(h))
			if xml, ok := model.ParseMacroMarker(line); ok {
				// A live macro: write it back and skip what it showed.
				if _, err := parse(xml); err == nil {
					c.b.WriteString(xml)
					if end := c.macroEnd(k); end != nil {
						k = end
					}
				}
				continue
			}
			switch line {
			case model.ExcerptStart:
				if !excerpt {
					c.b.WriteString(`<ac:structured-macro ac:name="excerpt"><ac:rich-text-body>`)
					excerpt = true
				}
				continue
			case model.ExcerptEnd:
				if excerpt {
					c.b.WriteString(`</ac:rich-text-body></ac:structured-macro>`)
					excerpt = false
				}
				continue
			}
		}
		c.block(k)
	}
	if excerpt {
		c.b.WriteString(`</ac:rich-text-body></ac:structured-macro>`)
	}
}

// macroEnd finds the MacroEnd line after the MacroStart line start.
func (c *fromMD) macroEnd(start ast.Node) ast.Node {
	for k := start.NextSibling(); k != nil; k = k.NextSibling() {
		if h, ok := k.(*ast.HTMLBlock); ok {
			line := strings.TrimSpace(c.lines(h))
			if line == model.MacroEnd {
				return k
			}
			if _, ok := model.ParseMacroMarker(line); ok {
				return nil
			}
		}
	}
	return nil
}

func (c *fromMD) block(n ast.Node) {
	switch n := n.(type) {
	case *ast.Paragraph:
		if img, ok := n.FirstChild().(*ast.Image); ok && n.FirstChild() == n.LastChild() {
			if _, _, isPage := model.ParsePageLink(plainText(img.Destination)); isPage {
				// A page embed: the macro is a block.
				if out := c.image(img); strings.HasPrefix(out, "<ac:") || out == "" {
					c.b.WriteString(out)
				} else {
					c.b.WriteString("<p>" + out + "</p>")
				}
				return
			}
		}
		if inner := c.inlines(n); strings.TrimSpace(inner) != "" {
			c.b.WriteString("<p>" + inner + "</p>")
		}
	case *ast.TextBlock:
		c.b.WriteString(c.inlines(n))
	case *ast.Heading:
		fmt.Fprintf(&c.b, "<h%d>%s</h%d>", n.Level, c.inlines(n), n.Level)
	case *ast.ThematicBreak:
		c.b.WriteString("<hr/>")
	case *ast.FencedCodeBlock:
		lang := ""
		if n.Info != nil {
			lang = string(n.Language(c.src))
		}
		c.code(lang, c.lines(n))
	case *ast.CodeBlock:
		c.code("", c.lines(n))
	case *ast.Blockquote:
		c.blockquote(n)
	case *ast.List:
		c.list(n)
	case *ast.HTMLBlock:
		c.htmlBlock(n)
	case *east.Table:
		c.table(n)
	default:
		c.children(n)
	}
}

func (c *fromMD) lines(n ast.Node) string {
	var b bytes.Buffer
	ls := n.Lines()
	for i := range ls.Len() {
		seg := ls.At(i)
		b.Write(seg.Value(c.src))
	}
	return b.String()
}

func (c *fromMD) code(lang, body string) {
	body = strings.TrimSuffix(body, "\n")
	switch strings.ToLower(lang) {
	case "confluence-macro":
		if root, err := parse(body); err == nil && len(root.kids) > 0 && hasOnlyElements(root) {
			c.b.WriteString(strings.TrimSpace(body))
			return
		}
		c.w.Warn("macro-invalid", "a confluence-macro block does not hold valid storage XML; it is written as a code block")
		lang = "xml"
	case "mermaid", "plantuml":
		// Confluence has no built-in Mermaid or PlantUML: keep the source in a
		// code block titled with the language so it comes back as that fence.
		c.b.WriteString(`<ac:structured-macro ac:name="code"><ac:parameter ac:name="title">` + strings.ToLower(lang) + `</ac:parameter>` +
			`<ac:plain-text-body>` + cdata(body) + `</ac:plain-text-body></ac:structured-macro>`)
		return
	}
	c.b.WriteString(`<ac:structured-macro ac:name="code">`)
	if l := langToConfluence(lang); l != "" {
		c.b.WriteString(`<ac:parameter ac:name="language">` + l + `</ac:parameter>`)
	}
	c.b.WriteString(`<ac:plain-text-body>` + cdata(body) + `</ac:plain-text-body></ac:structured-macro>`)
}

func hasOnlyElements(root *node) bool {
	for _, k := range root.kids {
		if k.name == "" && strings.TrimSpace(k.text) != "" {
			return false
		}
	}
	return true
}

var calloutRe = regexp.MustCompile(`^\[!([A-Za-z][\w-]*)\]([+-]?)[ \t]*(.*)$`)

func (c *fromMD) blockquote(n *ast.Blockquote) {
	first, ok := n.FirstChild().(*ast.Paragraph)
	if ok && first.Lines().Len() > 0 {
		line0 := first.Lines().At(0)
		if m := calloutRe.FindSubmatch(bytes.TrimRight(line0.Value(c.src), "\r\n")); m != nil {
			c.callout(string(m[1]), string(m[2]), plainText(m[3]), first, n)
			return
		}
	}
	c.b.WriteString("<blockquote>")
	c.children(n)
	c.b.WriteString("</blockquote>")
}

// callout writes a panel macro (or expand, for foldable callouts). The rest
// of the first paragraph after the [!kind] line is parsed again as Markdown.
func (c *fromMD) callout(kind, fold, title string, first *ast.Paragraph, n *ast.Blockquote) {
	var body strings.Builder
	var rest bytes.Buffer
	for i := 1; i < first.Lines().Len(); i++ {
		seg := first.Lines().At(i)
		rest.Write(seg.Value(c.src))
	}
	if rest.Len() > 0 {
		body.WriteString(c.convert(rest.Bytes()))
	}
	sub := &fromMD{w: c.w, src: c.src, taskID: c.taskID}
	for k := first.NextSibling(); k != nil; k = k.NextSibling() {
		sub.block(k)
	}
	c.taskID = sub.taskID
	body.WriteString(sub.b.String())

	name := macroFromCallout(kind)
	if fold != "" {
		name = "expand"
		if title == "" {
			title = "Expand"
		}
	}
	c.b.WriteString(`<ac:structured-macro ac:name="` + name + `">`)
	if title != "" {
		c.b.WriteString(`<ac:parameter ac:name="title">` + escapeText(title) + `</ac:parameter>`)
	}
	c.b.WriteString(`<ac:rich-text-body>` + body.String() + `</ac:rich-text-body></ac:structured-macro>`)
}

func (c *fromMD) list(n *ast.List) {
	if isTaskList(n) {
		c.b.WriteString("<ac:task-list>")
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			c.taskID++
			status := "incomplete"
			if box := taskBox(item); box != nil {
				if box.IsChecked {
					status = "complete"
				}
				box.Parent().RemoveChild(box.Parent(), box)
			}
			c.b.WriteString("<ac:task><ac:task-id>" + strconv.Itoa(c.taskID) + "</ac:task-id><ac:task-status>" + status + "</ac:task-status><ac:task-body>")
			c.children(item)
			c.b.WriteString("</ac:task-body></ac:task>")
		}
		c.b.WriteString("</ac:task-list>")
		return
	}
	tag := "ul"
	open := "<ul>"
	if n.IsOrdered() {
		tag = "ol"
		open = "<ol>"
		if n.Start > 1 {
			open = `<ol start="` + strconv.Itoa(n.Start) + `">`
		}
	}
	c.b.WriteString(open)
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		c.b.WriteString("<li>")
		c.children(item)
		c.b.WriteString("</li>")
	}
	c.b.WriteString("</" + tag + ">")
}

func isTaskList(n *ast.List) bool {
	if n.FirstChild() == nil {
		return false
	}
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		if taskBox(item) == nil {
			return false
		}
	}
	return true
}

func taskBox(item ast.Node) *east.TaskCheckBox {
	if first := item.FirstChild(); first != nil {
		if box, ok := first.FirstChild().(*east.TaskCheckBox); ok {
			return box
		}
	}
	return nil
}

var htmlCommentRe = regexp.MustCompile(`(?s)^\s*<!--.*-->\s*$`)

func (c *fromMD) htmlBlock(n *ast.HTMLBlock) {
	raw := c.lines(n)
	if n.HasClosure() {
		seg := n.ClosureLine
		raw += string(seg.Value(c.src))
	}
	trimmed := strings.TrimSpace(raw)
	switch {
	case trimmed == model.TOCMarker:
		c.b.WriteString(`<ac:structured-macro ac:name="toc"/>`)
	case isHTMLTable(trimmed):
		c.htmlTable(trimmed)
	case htmlCommentRe.MatchString(trimmed):
	case trimmed == "":
	default:
		c.w.Warn("html-block", "a block of raw HTML cannot be stored in Confluence; it is shown as a code block")
		c.code("xml", trimmed)
	}
}

func (c *fromMD) table(n *east.Table) {
	c.b.WriteString("<table><tbody>")
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		header := false
		if _, ok := row.(*east.TableHeader); ok {
			header = true
			if c.emptyRow(row) {
				continue // headerless table (see ToMarkdown)
			}
		}
		c.b.WriteString("<tr>")
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			tag := "td"
			if header {
				tag = "th"
			}
			c.b.WriteString("<" + tag + ">" + c.inlines(cell) + "</" + tag + ">")
		}
		c.b.WriteString("</tr>")
	}
	c.b.WriteString("</tbody></table>")
}

func (c *fromMD) emptyRow(row ast.Node) bool {
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		if strings.TrimSpace(c.inlines(cell)) != "" {
			return false
		}
	}
	return true
}

// ---- inline ----

func (c *fromMD) inlines(n ast.Node) string {
	var b strings.Builder
	for k := n.FirstChild(); k != nil; k = k.NextSibling() {
		c.inline(&b, k)
	}
	return b.String()
}

func (c *fromMD) inline(b *strings.Builder, n ast.Node) {
	switch n := n.(type) {
	case *ast.Text:
		seg := n.Segment
		v := seg.Value(c.src)
		if n.IsRaw() {
			b.WriteString(escapeText(string(v)))
		} else {
			b.WriteString(escapeText(plainText(v)))
		}
		switch {
		case n.HardLineBreak():
			b.WriteString("<br/>")
		case n.SoftLineBreak():
			b.WriteString(" ")
		}
	case *ast.String:
		if n.IsCode() || n.IsRaw() {
			b.WriteString(escapeText(string(n.Value)))
		} else {
			b.WriteString(escapeText(plainText(n.Value)))
		}
	case *ast.CodeSpan:
		var raw strings.Builder
		for k := n.FirstChild(); k != nil; k = k.NextSibling() {
			if t, ok := k.(*ast.Text); ok {
				seg := t.Segment
				raw.Write(seg.Value(c.src))
			} else if s, ok := k.(*ast.String); ok {
				raw.Write(s.Value)
			}
		}
		b.WriteString("<code>" + escapeText(raw.String()) + "</code>")
	case *ast.Emphasis:
		tag := "em"
		if n.Level >= 2 {
			tag = "strong"
		}
		b.WriteString("<" + tag + ">" + c.inlines(n) + "</" + tag + ">")
	case *east.Strikethrough:
		b.WriteString(`<span style="text-decoration: line-through;">` + c.inlines(n) + "</span>")
	case *ast.Link:
		c.link(b, n)
	case *ast.Image:
		b.WriteString(c.image(n))
	case *ast.AutoLink:
		u := string(n.URL(c.src))
		label := string(n.Label(c.src))
		if n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(u, "mailto:") {
			u = "mailto:" + u
		}
		b.WriteString(`<a href="` + escapeAttr(u) + `">` + escapeText(label) + `</a>`)
	case *ast.RawHTML:
		var raw strings.Builder
		for i := range n.Segments.Len() {
			seg := n.Segments.At(i)
			raw.Write(seg.Value(c.src))
		}
		c.rawHTML(b, raw.String())
	case *east.TaskCheckBox:
		// Only reached in a list mixing tasks and plain items.
		if n.IsChecked {
			b.WriteString("[x] ")
		} else {
			b.WriteString("[ ] ")
		}
	default:
		b.WriteString(c.inlines(n))
	}
}

var (
	brRe     = regexp.MustCompile(`(?i)^<br\s*/?>$`)
	subSupRe = regexp.MustCompile(`(?i)^</?(sub|sup)>$`)
)

func (c *fromMD) rawHTML(b *strings.Builder, raw string) {
	switch t := strings.TrimSpace(raw); {
	case brRe.MatchString(t):
		b.WriteString("<br/>")
	case subSupRe.MatchString(t):
		b.WriteString(strings.ToLower(t))
	case strings.HasPrefix(raw, "<!--"):
	default:
		c.w.Warn("html-dropped", fmt.Sprintf("the inline HTML %q is not supported by Confluence and was dropped (its text is kept)", truncate(raw, 60)))
	}
}

func (c *fromMD) link(b *strings.Builder, n *ast.Link) {
	dest := plainText(n.Destination)
	// [![img](a)](b): Confluence cannot wrap an image in a link; write the
	// image followed by a link.
	if img, ok := n.FirstChild().(*ast.Image); ok && n.FirstChild() == n.LastChild() {
		b.WriteString(c.image(img))
		label := plainText(img.Text(c.src)) //nolint:staticcheck // alt text
		if hash, ok := model.ParseAttachmentLink(dest); ok {
			if name, _, found := c.w.Attachment(hash); found {
				label = name
			}
		}
		if label == "" {
			label = dest
		}
		b.WriteString("<br/>")
		b.WriteString(c.linkTo(dest, label, ""))
		return
	}
	body := c.inlines(n)
	plain := ""
	if allText(n) {
		plain = plainText(n.Text(c.src)) //nolint:staticcheck // link label
	}
	b.WriteString(c.linkToBody(dest, plain, body))
}

func allText(n ast.Node) bool {
	for k := n.FirstChild(); k != nil; k = k.NextSibling() {
		if _, ok := k.(*ast.Text); !ok {
			return false
		}
	}
	return true
}

func (c *fromMD) linkTo(dest, plain, body string) string {
	if body == "" {
		body = escapeText(plain)
	}
	return c.linkToBody(dest, plain, body)
}

// linkToBody writes a link; plain is the label as plain text when it has no
// formatting, body the label as storage XML.
func (c *fromMD) linkToBody(dest, plain, body string) string {
	linkBody := func() string {
		if plain != "" {
			return `<ac:plain-text-link-body>` + cdata(plain) + `</ac:plain-text-link-body>`
		}
		if strings.TrimSpace(body) == "" {
			return ""
		}
		return `<ac:link-body>` + body + `</ac:link-body>`
	}
	if id, heading, ok := model.ParsePageLink(dest); ok {
		anchor := ""
		if heading != "" {
			anchor = ` ac:anchor="` + escapeAttr(anchorName(heading)) + `"`
		}
		if id == c.w.SelfID() {
			if anchor == "" {
				return body
			}
			return `<ac:link` + anchor + `>` + linkBody() + `</ac:link>`
		}
		title, found := c.w.PageTitle(id)
		if !found {
			c.w.Warn("link-unresolved", fmt.Sprintf("the link to page %q points to a page that is not in this mirror; it is shown as plain text", id))
			return body
		}
		return `<ac:link` + anchor + `><ri:page ri:content-title="` + escapeAttr(title) + `"/>` + linkBody() + `</ac:link>`
	}
	if hash, ok := model.ParseAttachmentLink(dest); ok {
		name, owner, found := c.w.Attachment(hash)
		if !found {
			c.w.Warn("attachment-missing", "a link points to an attachment that is not in this mirror; it is shown as plain text")
			return body
		}
		return `<ac:link>` + riAttachment(name, owner) + linkBody() + `</ac:link>`
	}
	if strings.HasPrefix(dest, "#") {
		return `<ac:link ac:anchor="` + escapeAttr(anchorName(strings.TrimPrefix(dest, "#"))) + `">` + linkBody() + `</ac:link>`
	}
	if body == "" {
		body = escapeText(dest)
	}
	return `<a href="` + escapeAttr(dest) + `">` + body + `</a>`
}

func riAttachment(name, owner string) string {
	if owner == "" {
		return `<ri:attachment ri:filename="` + escapeAttr(name) + `"/>`
	}
	return `<ri:attachment ri:filename="` + escapeAttr(name) + `"><ri:page ri:content-title="` + escapeAttr(owner) + `"/></ri:attachment>`
}

var widthRe = regexp.MustCompile(`^width=(\d+)$`)

func (c *fromMD) image(n *ast.Image) string {
	dest := plainText(n.Destination)
	alt := plainText(n.Text(c.src)) //nolint:staticcheck // alt text
	attrs := ""
	if alt != "" {
		attrs += ` ac:alt="` + escapeAttr(alt) + `"`
	}
	if m := widthRe.FindStringSubmatch(strings.TrimSpace(plainText(n.Title))); m != nil {
		attrs += ` ac:width="` + m[1] + `"`
	}
	if hash, ok := model.ParseAttachmentLink(dest); ok {
		name, owner, found := c.w.Attachment(hash)
		if !found {
			c.w.Warn("attachment-missing", "an image points to an attachment that is not in this mirror; only its description is shown")
			return escapeText(alt)
		}
		return `<ac:image` + attrs + `>` + riAttachment(name, owner) + `</ac:image>`
	}
	if id, _, ok := model.ParsePageLink(dest); ok {
		return c.include(id, alt, strings.TrimSpace(plainText(n.Title)) == model.ExcerptTitle)
	}
	return `<ac:image` + attrs + `><ri:url ri:value="` + escapeAttr(dest) + `"/></ac:image>`
}

// include writes a page embed as an include (or excerpt-include) macro.
func (c *fromMD) include(id, alt string, excerpt bool) string {
	if id == c.w.SelfID() {
		c.w.Warn("page-embed", "a page embeds itself; the embed is left out")
		return ""
	}
	title, found := c.w.PageTitle(id)
	if !found {
		c.w.Warn("link-unresolved", fmt.Sprintf("the page %q embedded here is not in this mirror; only its title is shown", firstNonEmpty(alt, id)))
		return escapeText(alt)
	}
	name := "include"
	if excerpt {
		name = "excerpt-include"
	}
	return `<ac:structured-macro ac:name="` + name + `"><ac:parameter ac:name=""><ac:link><ri:page ri:content-title="` +
		escapeAttr(title) + `"/></ac:link></ac:parameter></ac:structured-macro>`
}

// anchorName is the anchor Confluence gives a heading: its text without spaces.
func anchorName(heading string) string {
	return strings.Join(strings.Fields(heading), "")
}

// plainText resolves Markdown backslash escapes and character references.
func plainText(b []byte) string {
	b = util.UnescapePunctuations(b)
	b = util.ResolveNumericReferences(b)
	b = util.ResolveEntityNames(b)
	return string(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

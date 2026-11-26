package storage

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Env resolves references while a page is converted to Markdown.
type Env interface {
	// SelfID is the canonical id of the page being converted.
	SelfID() string
	// PageID resolves a link to the page titled title in space ("" = the
	// page's own space) to a canonical page id.
	PageID(space, title string) (string, bool)
	// Attachment returns the content hash of the attachment filename on the
	// page titled ownerTitle ("" = the page being converted), registering it.
	Attachment(ownerTitle, filename string) (string, bool)
	// Diagram returns the hash of the draw.io diagram called name, stored on
	// the page with id ownerID ("" = the page being converted).
	Diagram(ownerID, name string) (string, bool)
	// Heading maps a Confluence anchor (heading text without spaces) on the
	// page with canonical id pageID back to the heading's text; it returns
	// anchor unchanged when no heading matches.
	Heading(pageID, anchor string) string
	// Warn reports a degradation.
	Warn(code, message string)
}

// PageURLResolver is an optional Env extension. Confluence Cloud writes many
// page links as plain web addresses (smart links); PageByURL maps the address
// of a page in the snapshot to its canonical id, title and anchor.
type PageURLResolver interface {
	PageByURL(href string) (id, title, anchor string, ok bool)
}

// ToMarkdown converts a storage-format body to canonical Markdown.
func ToMarkdown(src string, env Env) (string, error) {
	root, err := parse(src)
	if err != nil {
		return "", fmt.Errorf("read storage format: %w", err)
	}
	c := &toMD{env: env}
	out := strings.Join(c.blocks(root.kids), "\n\n")
	if out != "" {
		out += "\n"
	}
	return out, nil
}

type toMD struct {
	env     Env
	inTable int
	held    []string // blocks found inside paragraphs (see paragraph)
}

// Elements rendered as blocks.
var blockElems = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "pre": true, "blockquote": true, "table": true, "hr": true,
	"div": true, "section": true, "center": true, "ac:task-list": true,
	"ac:layout": true, "ac:layout-section": true, "ac:layout-cell": true,
	"ac:adf-extension": true,
}

// Macros that sit inside a line of text.
var inlineMacros = map[string]bool{"status": true, "anchor": true, "jira": true}

func isMacro(n *node) bool { return n.name == "ac:structured-macro" || n.name == "ac:macro" }

func macroName(n *node) string { return strings.ToLower(n.attr("ac:name")) }

func (c *toMD) isBlock(n *node) bool {
	if isMacro(n) {
		return !inlineMacros[macroName(n)]
	}
	return blockElems[n.name]
}

// blocks renders a list of nodes as Markdown blocks; runs of inline nodes
// become paragraphs.
func (c *toMD) blocks(kids []*node) []string {
	var out []string
	var run []*node
	flush := func() {
		if len(run) > 0 {
			out = append(out, c.paragraph(run)...)
			run = nil
		}
	}
	for _, k := range kids {
		if k.name != "" && c.isBlock(k) {
			flush()
			out = append(out, c.block(k)...)
			continue
		}
		run = append(run, k)
	}
	flush()
	return out
}

func (c *toMD) block(n *node) []string {
	switch n.name {
	case "p":
		return c.paragraph(n.kids)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		s := oneLine(c.inline(n.kids))
		if s == "" {
			return nil
		}
		return []string{strings.Repeat("#", int(n.name[1]-'0')) + " " + s}
	case "ul", "ol":
		if s := c.list(n, n.name == "ol"); s != "" {
			return []string{s}
		}
		return nil
	case "ac:task-list":
		if s := c.taskList(n); s != "" {
			return []string{s}
		}
		return nil
	case "pre":
		return []string{fence("", n.textContent())}
	case "blockquote":
		bs := c.blocks(n.kids)
		if len(bs) == 0 {
			return nil
		}
		return []string{quote(bs)}
	case "hr":
		return []string{"---"}
	case "table":
		if s := c.table(n); s != "" {
			return []string{s}
		}
		return nil
	case "ac:structured-macro", "ac:macro":
		return c.macro(n)
	case "ac:adf-extension":
		return c.adfExtension(n)
	default:
		return c.blocks(n.kids)
	}
}

// panelFromADF maps the panel types of the Confluence Cloud editor.
var panelFromADF = map[string]string{
	"info": "info", "note": "note", "success": "tip", "tip": "tip",
	"warning": "warning", "error": "danger", "custom": "note",
}

// adfExtension renders content of the Confluence Cloud editor that has no
// storage-format equivalent (custom panels, decision lists, …): panels
// become callouts, anything else its HTML fallback.
func (c *toMD) adfExtension(n *node) []string {
	adf := n.child("ac:adf-node")
	if adf != nil && adf.attr("type") == "panel" {
		kind := "info"
		for _, a := range adf.kids {
			if a.name == "ac:adf-attribute" && a.attr("key") == "panel-type" {
				kind = strings.ToLower(strings.TrimSpace(a.textContent()))
			}
		}
		callout := panelFromADF[kind]
		if callout == "" {
			callout = "note"
		}
		return c.callout(callout, "", "", adf.child("ac:adf-content"))
	}
	if fb := n.child("ac:adf-fallback"); fb != nil {
		return c.blocks(fb.kids)
	}
	if adf != nil {
		if body := adf.child("ac:adf-content"); body != nil {
			return c.blocks(body.kids)
		}
	}
	return nil
}

const heldMark = "\x00"

var heldRe = regexp.MustCompile("\x00(\\d+)\x00")

// paragraph renders inline nodes. Block macros met inside the text (an
// unknown macro inside <p>) split the paragraph around them.
func (c *toMD) paragraph(nodes []*node) []string {
	s := c.inline(nodes)
	var out []string
	add := func(text string) {
		text = trimParagraph(text)
		if text != "" {
			out = append(out, escapeLineStarts(text))
		}
	}
	last := 0
	for _, m := range heldRe.FindAllStringSubmatchIndex(s, -1) {
		add(s[last:m[0]])
		i, _ := strconv.Atoi(s[m[2]:m[3]])
		out = append(out, c.held[i])
		last = m[1]
	}
	add(s[last:])
	return out
}

// trimParagraph removes surrounding spaces and dangling hard breaks.
func trimParagraph(s string) string {
	for {
		t := strings.TrimSpace(s)
		t = strings.TrimSuffix(t, "\\")
		t = strings.TrimPrefix(t, "\\\n")
		if t == s {
			return s
		}
		s = t
	}
}

// inline renders nodes as one run of Markdown inline text.
func (c *toMD) inline(nodes []*node) string {
	w := &inlineWriter{}
	for _, n := range nodes {
		c.inlineNode(w, n)
	}
	return w.b.String()
}

type inlineWriter struct {
	b    strings.Builder
	last rune // last rune written; 0 at start
}

func (w *inlineWriter) raw(s string) {
	if s == "" {
		return
	}
	w.b.WriteString(s)
	w.last, _ = utf8.DecodeLastRuneInString(s)
}

// text writes source text: whitespace collapses as in HTML, Markdown
// syntax characters are escaped.
func (w *inlineWriter) text(s string) {
	var b strings.Builder
	space := w.last == 0 || w.last == ' ' || w.last == '\n'
	for _, r := range s {
		if unicode.IsSpace(r) || r == ' ' {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		space = false
		b.WriteRune(r)
	}
	w.raw(escapeMD(b.String()))
}

func (c *toMD) inlineNode(w *inlineWriter, n *node) {
	if n.name == "" {
		w.text(n.text)
		return
	}
	switch n.name {
	case "strong", "b":
		c.wrap(w, "**", n.kids)
	case "em", "i", "cite", "dfn":
		c.wrap(w, "*", n.kids)
	case "s", "del", "strike":
		c.wrap(w, "~~", n.kids)
	case "span":
		if strings.Contains(strings.ReplaceAll(n.attr("style"), " ", ""), "text-decoration:line-through") {
			c.wrap(w, "~~", n.kids)
		} else {
			c.inlineChildren(w, n)
		}
	case "code", "tt", "kbd", "samp":
		w.raw(c.codeSpan(n.textContent()))
	case "br":
		if c.inTable > 0 {
			w.raw("<br>")
		} else {
			w.raw("\\\n")
		}
	case "a":
		c.anchorLink(w, n)
	case "img":
		alt := n.attr("alt")
		w.raw("![" + escapeMD(alt) + "](" + mdURL(n.attr("src")) + ")")
	case "ac:image":
		w.raw(c.image(n))
	case "ac:link":
		w.raw(c.link(n))
	case "ac:emoticon":
		w.raw(emoticon(n))
	case "time":
		w.text(n.attr("datetime"))
	case "ac:placeholder", "ac:parameter", "ac:adf-attribute":
	case "ac:adf-extension":
		if fb := n.child("ac:adf-fallback"); fb != nil {
			c.inlineChildren(w, fb)
		} else if adf := n.child("ac:adf-node"); adf != nil {
			c.inlineChildren(w, adf)
		}
	case "ac:structured-macro", "ac:macro":
		switch name := macroName(n); name {
		case "status":
			if t := n.param("title"); t != "" {
				w.text("[" + t + "]")
			}
		case "jira":
			w.text(n.param("key"))
		case "anchor":
		default:
			// A block macro inside a line: hold it and split the paragraph.
			for _, b := range c.macro(n) {
				c.held = append(c.held, b)
				w.raw(heldMark + strconv.Itoa(len(c.held)-1) + heldMark)
			}
		}
	default:
		c.inlineChildren(w, n)
	}
}

func (c *toMD) inlineChildren(w *inlineWriter, n *node) {
	for _, k := range n.kids {
		c.inlineNode(w, k)
	}
}

// wrap writes marker + content + marker, moving surrounding spaces outside
// the markers (CommonMark ignores "** bold **").
func (c *toMD) wrap(w *inlineWriter, marker string, kids []*node) {
	sub := &toMD{env: c.env, inTable: c.inTable}
	inner := sub.inline(kids)
	c.held = append(c.held, sub.held...) // rare: macros inside bold text
	trimmed := strings.TrimSpace(inner)
	if trimmed == "" {
		if inner != "" {
			w.text(" ")
		}
		return
	}
	if inner[0] == ' ' {
		w.text(" ")
	}
	w.raw(marker + trimmed + marker)
	if inner[len(inner)-1] == ' ' {
		w.text(" ")
	}
}

func (c *toMD) codeSpan(s string) string {
	s = collapseSpaces(s)
	if s == "" {
		return ""
	}
	if c.inTable > 0 {
		s = strings.ReplaceAll(s, "|", `\|`)
	}
	n := longestRun(s, '`') + 1
	ticks := strings.Repeat("`", n)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return ticks + " " + s + " " + ticks
	}
	return ticks + s + ticks
}

func (c *toMD) anchorLink(w *inlineWriter, n *node) {
	href := n.attr("href")
	text := strings.TrimSpace(c.inline(n.kids))
	if href == "" {
		w.raw(text)
		return
	}
	if text == "" {
		text = escapeMD(href)
	}
	if frag, ok := strings.CutPrefix(href, "#"); ok {
		self := c.env.SelfID()
		w.raw("[" + text + "](" + model.PageLink(self, c.env.Heading(self, frag)) + ")")
		return
	}
	if r, ok := c.env.(PageURLResolver); ok {
		if id, title, anchor, ok := r.PageByURL(href); ok {
			if text == escapeMD(href) && title != "" { // a smart link shows the address
				text = escapeMD(title)
			}
			w.raw("[" + text + "](" + model.PageLink(id, c.env.Heading(id, anchor)) + ")")
			return
		}
	}
	w.raw("[" + text + "](" + mdURL(href) + ")")
}

func (c *toMD) image(n *node) string {
	alt := n.attr("ac:alt")
	url := ""
	switch {
	case n.child("ri:attachment") != nil:
		ri := n.child("ri:attachment")
		filename := ri.attr("ri:filename")
		hash, ok := c.env.Attachment(ownerTitle(ri), filename)
		if !ok {
			c.env.Warn("attachment-missing", fmt.Sprintf("the image %q was not found on the page; only its name is shown", filename))
			return escapeMD(firstNonEmpty(alt, filename))
		}
		url = model.AttachmentLink(hash)
		if alt == "" {
			alt = filename
		}
	case n.child("ri:url") != nil:
		url = n.child("ri:url").attr("ri:value")
	default:
		return escapeMD(alt)
	}
	title := ""
	if wd := n.attr("ac:width"); wd != "" {
		title = ` "width=` + wd + `"`
	}
	return "![" + escapeMD(alt) + "](" + mdURL(url) + title + ")"
}

func ownerTitle(ri *node) string {
	if p := ri.child("ri:page"); p != nil {
		return p.attr("ri:content-title")
	}
	return ""
}

func (c *toMD) link(n *node) string {
	anchor := n.attr("ac:anchor")
	text := ""
	if b := n.child("ac:plain-text-link-body"); b != nil {
		text = escapeMD(collapseSpaces(b.textContent()))
	} else if b := n.child("ac:link-body"); b != nil {
		text = strings.TrimSpace(c.inline(b.kids))
	}
	switch {
	case n.child("ri:page") != nil:
		ri := n.child("ri:page")
		title := ri.attr("ri:content-title")
		if title == "" {
			return c.selfAnchor(text, anchor)
		}
		id, ok := c.env.PageID(ri.attr("ri:space-key"), title)
		if text == "" {
			text = escapeMD(title)
		}
		if !ok {
			c.env.Warn("link-outside-master", fmt.Sprintf("the link to %q points outside the Master (another space, or outside the root page); it is shown as plain text", title))
			return text
		}
		if anchor != "" {
			anchor = c.env.Heading(id, anchor)
		}
		return "[" + text + "](" + model.PageLink(id, anchor) + ")"
	case n.child("ri:attachment") != nil:
		ri := n.child("ri:attachment")
		filename := ri.attr("ri:filename")
		if text == "" {
			text = escapeMD(filename)
		}
		hash, ok := c.env.Attachment(ownerTitle(ri), filename)
		if !ok {
			c.env.Warn("attachment-missing", fmt.Sprintf("the attachment %q was not found; only its name is shown", filename))
			return text
		}
		return "[" + text + "](" + model.AttachmentLink(hash) + ")"
	case n.child("ri:user") != nil:
		if text != "" {
			return text
		}
		ri := n.child("ri:user")
		return "@" + escapeMD(firstNonEmpty(ri.attr("ri:username"), ri.attr("ri:userkey"), ri.attr("ri:account-id"), "user"))
	case n.child("ri:url") != nil:
		u := n.child("ri:url").attr("ri:value")
		return "[" + firstNonEmpty(text, escapeMD(u)) + "](" + mdURL(u) + ")"
	case anchor != "":
		return c.selfAnchor(text, anchor)
	default:
		for _, k := range n.kids {
			if strings.HasPrefix(k.name, "ri:") {
				what := firstNonEmpty(k.attr("ri:content-title"), k.attr("ri:space-key"), strings.TrimPrefix(k.name, "ri:"))
				c.env.Warn("link-unsupported", fmt.Sprintf("the link to %q (a Confluence %s) has no equivalent in mirrors; it is shown as plain text", what, strings.TrimPrefix(k.name, "ri:")))
				return firstNonEmpty(text, escapeMD(what))
			}
		}
		return text
	}
}

func (c *toMD) selfAnchor(text, anchor string) string {
	if anchor == "" {
		return text
	}
	self := c.env.SelfID()
	heading := c.env.Heading(self, anchor)
	return "[" + firstNonEmpty(text, escapeMD(heading)) + "](" + model.PageLink(self, heading) + ")"
}

func emoticon(n *node) string {
	if f := n.attr("ac:emoji-fallback"); f != "" && !strings.HasPrefix(f, ":") {
		return f
	}
	name := n.attr("ac:name")
	if e, ok := emoticons[name]; ok {
		return e
	}
	return ":" + name + ":"
}

func (c *toMD) list(n *node, ordered bool) string {
	start := 1
	if v, err := strconv.Atoi(n.attr("start")); err == nil && v > 0 {
		start = v
	}
	var items []string
	i := start
	for _, li := range n.kids {
		switch li.name {
		case "li":
		case "ul", "ol":
			// A list directly inside a list (invalid but common): nest it under the previous item.
			if len(items) > 0 {
				items[len(items)-1] += "\n" + indentLines(c.list(li, li.name == "ol"), "  ")
			}
			continue
		default:
			continue
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(i) + ". "
			i++
		}
		items = append(items, prefixLines(marker, c.itemBody(li.kids)))
	}
	return strings.Join(items, "\n")
}

func (c *toMD) taskList(n *node) string {
	var items []string
	for _, t := range n.kids {
		if t.name != "ac:task" {
			continue
		}
		box := "[ ] "
		if st := t.child("ac:task-status"); st != nil && strings.TrimSpace(st.textContent()) == "complete" {
			box = "[x] "
		}
		body := ""
		if b := t.child("ac:task-body"); b != nil {
			body = c.itemBody(b.kids)
		}
		items = append(items, prefixLines("- ", box+body))
	}
	return strings.Join(items, "\n")
}

// itemBody renders the content of a list item; a nested list follows its
// text on the next line so the list stays tight.
func (c *toMD) itemBody(kids []*node) string {
	var b strings.Builder
	for i, s := range c.blocks(kids) {
		if i > 0 {
			if isListBlock(s) {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(s)
	}
	return b.String()
}

var listStartRe = regexp.MustCompile(`^(- |\d+\. )`)

func isListBlock(s string) bool { return listStartRe.MatchString(s) }

func (c *toMD) table(n *node) string {
	var rows [][]*node
	var collect func(n *node)
	collect = func(n *node) {
		for _, k := range n.kids {
			switch k.name {
			case "thead", "tbody", "tfoot":
				collect(k)
			case "tr":
				var cells []*node
				for _, cell := range k.kids {
					if cell.name == "th" || cell.name == "td" {
						cells = append(cells, cell)
					}
				}
				rows = append(rows, cells)
			}
		}
	}
	collect(n)
	if len(rows) == 0 {
		return ""
	}
	c.inTable++
	defer func() { c.inTable-- }()

	simplified := false
	ncol := 0
	grid := make([][]string, len(rows))
	for r, cells := range rows {
		for _, cell := range cells {
			if span(cell.attr("colspan")) > 1 || span(cell.attr("rowspan")) > 1 {
				simplified = true
			}
			s, complex := c.cell(cell)
			simplified = simplified || complex
			grid[r] = append(grid[r], s)
		}
		ncol = max(ncol, len(grid[r]))
	}
	if ncol == 0 {
		return ""
	}
	if simplified {
		c.env.Warn("table-simplified", "a table with merged cells or block content (lists, code, panels) inside cells was simplified; check how it looks in the mirror")
	}
	header := make([]string, ncol)
	body := grid
	if allHeaderCells(rows[0]) {
		copy(header, grid[0])
		body = grid[1:]
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		b.WriteString("|")
		for i := range ncol {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			b.WriteString(" " + cell + " |")
		}
		b.WriteString("\n")
	}
	writeRow(header)
	b.WriteString("|" + strings.Repeat(" --- |", ncol) + "\n")
	for _, r := range body {
		writeRow(r)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func span(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 1
	}
	return n
}

func allHeaderCells(cells []*node) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if c.name != "th" {
			return false
		}
	}
	return true
}

// cell renders a table cell on one line; complex reports block content that
// does not fit a Markdown table cell.
func (c *toMD) cell(n *node) (string, bool) {
	bs := c.blocks(n.kids)
	complex := false
	for _, b := range bs {
		if strings.Contains(b, "\n") || strings.HasPrefix(b, "#") || strings.HasPrefix(b, ">") || isListBlock(b) {
			complex = true
		}
	}
	return strings.ReplaceAll(strings.Join(bs, "<br>"), "\n", "<br>"), complex
}

func (c *toMD) macro(n *node) []string {
	name := macroName(n)
	body := n.child("ac:rich-text-body")
	switch {
	case name == "code" || name == "noformat":
		lang := ""
		if name == "code" {
			lang = langFromConfluence(n.param("language"))
			if lang == "" && strings.EqualFold(n.param("title"), "mermaid") {
				lang = "mermaid" // written so by FromMarkdown
			}
		}
		return []string{fence(lang, plainBody(n))}
	case calloutFromMacro[name] != "":
		fold := ""
		return c.callout(calloutFromMacro[name], n.param("title"), fold, body)
	case name == "expand":
		return c.callout("note", firstNonEmpty(n.param("title"), "Expand"), "-", body)
	case name == "toc":
		return []string{model.TOCMarker}
	case name == "children" || name == "pagetree" || name == "anchor":
		return nil // the mirror's own navigation shows child pages
	case name == "details" || name == "section" || name == "column" || name == "excerpt" || name == "toc-zone" || name == "div":
		if body == nil {
			return nil
		}
		return c.blocks(body.kids)
	case strings.Contains(name, "drawio"):
		return []string{c.diagram(n)}
	case strings.Contains(name, "mermaid") && n.child("ac:plain-text-body") != nil:
		return []string{fence("mermaid", plainBody(n))}
	case inlineMacros[name]:
		return c.paragraph([]*node{n})
	default:
		c.env.Warn("macro-unsupported", fmt.Sprintf("the Confluence macro %q has no equivalent; mirrors that are not Confluence show it as a code block", name))
		return []string{fence("confluence-macro", serialize(n))}
	}
}

func plainBody(n *node) string {
	if b := n.child("ac:plain-text-body"); b != nil {
		return b.textContent()
	}
	return ""
}

func (c *toMD) callout(kind, title, fold string, body *node) []string {
	head := "[!" + kind + "]" + fold
	if title = collapseSpaces(title); title != "" {
		head += " " + escapeMD(title)
	}
	blocks := []string{head}
	if body != nil {
		if bs := c.blocks(body.kids); len(bs) > 0 {
			blocks[0] += "\n" + strings.Join(bs, "\n\n")
		}
	}
	return []string{quote(blocks)}
}

func (c *toMD) diagram(n *node) string {
	name := n.param("diagramName")
	hash, ok := c.env.Diagram(n.param("pageId"), name)
	if !ok {
		c.env.Warn("diagram-missing", fmt.Sprintf("the draw.io diagram %q was not found among the page attachments; only its name is shown", name))
		return escapeMD("[diagram: " + name + "]")
	}
	return "![" + escapeMD(name) + "](" + model.AttachmentLink(hash) + ")"
}

// Headings lists the text of the headings in a storage-format body, for
// Env.Heading.
func Headings(src string) []string {
	root, err := parse(src)
	if err != nil {
		return nil
	}
	var out []string
	var walk func(n *node)
	walk = func(n *node) {
		for _, k := range n.kids {
			if len(k.name) == 2 && k.name[0] == 'h' && k.name[1] >= '1' && k.name[1] <= '6' {
				out = append(out, collapseSpaces(k.textContent()))
				continue
			}
			walk(k)
		}
	}
	walk(root)
	return out
}

// HeadingForAnchor finds the heading whose Confluence anchor is anchor.
func HeadingForAnchor(headings []string, anchor string) (string, bool) {
	if dec, err := url.PathUnescape(anchor); err == nil {
		anchor = dec
	}
	for _, h := range headings {
		// Data Center drops the spaces; Cloud turns them into dashes.
		if anchorName(h) == anchor || h == anchor || strings.Join(strings.Fields(h), "-") == anchor {
			return h, true
		}
	}
	return "", false
}

// ---- text helpers ----

func fence(lang, body string) string {
	body = strings.TrimRight(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	body = strings.TrimLeft(body, "\n")
	n := max(3, longestRun(body, '`')+1)
	f := strings.Repeat("`", n)
	return f + lang + "\n" + body + "\n" + f
}

func quote(blocks []string) string {
	lines := strings.Split(strings.Join(blocks, "\n\n"), "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

// prefixLines puts marker before the first line and indents the others to
// line up with the content.
func prefixLines(marker, body string) string {
	if body == "" {
		return strings.TrimRight(marker, " ")
	}
	pad := strings.Repeat(" ", len(marker))
	lines := strings.Split(body, "\n")
	for i := range lines {
		switch {
		case i == 0:
			lines[i] = marker + lines[i]
		case lines[i] != "":
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func indentLines(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		if lines[i] != "" {
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\\\n", " ")
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
}

func collapseSpaces(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == ' ' }), " ")
}

func longestRun(s string, c byte) int {
	best, cur := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			cur++
			best = max(best, cur)
		} else {
			cur = 0
		}
	}
	return best
}

var entityRe = regexp.MustCompile(`^&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);`)

// escapeMD escapes characters that would start Markdown syntax.
func escapeMD(s string) string {
	var b strings.Builder
	prev := rune(0)
	for i, r := range s {
		switch r {
		case '\\', '`', '*', '[', ']', '<', '|', '~':
			b.WriteByte('\\')
		case '_':
			next, _ := utf8.DecodeRuneInString(s[i+1:])
			if !isWordRune(prev) || !isWordRune(next) {
				b.WriteByte('\\')
			}
		case '&':
			if entityRe.MatchString(s[i:]) {
				b.WriteByte('\\')
			}
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

var lineStartRe = regexp.MustCompile(`^(#{1,6}(\s|$)|>|[-+*](\s|$)|\d{1,9}[.)](\s|$)|=+\s*$|-+\s*$)`)

// escapeLineStarts escapes text at the start of each paragraph line that
// Markdown would read as a heading, quote, list or rule.
func escapeLineStarts(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if lineStartRe.MatchString(l) {
			if loc := regexp.MustCompile(`^\d+`).FindStringIndex(l); loc != nil {
				lines[i] = l[:loc[1]] + `\` + l[loc[1]:]
			} else {
				lines[i] = `\` + l
			}
		}
	}
	return strings.Join(lines, "\n")
}

// mdURL wraps URLs that contain spaces or parentheses in angle brackets.
func mdURL(u string) string {
	if strings.ContainsAny(u, " ()<>") {
		return "<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(u) + ">"
	}
	return u
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

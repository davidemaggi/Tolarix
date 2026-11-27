package storage

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Complex tables travel as HTML blocks in canonical Markdown (see
// model.Page). ToMarkdown renders each cell's Markdown to HTML;
// FromMarkdown reads the HTML back into a storage table.

// gmHTML renders cell content. Raw HTML (<sub>, <br>) is kept: it comes from
// the converter itself.
var gmHTML = goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(html.WithUnsafe()))

var (
	preRe      = regexp.MustCompile(`(?s)<pre[ >].*?</pre>`)
	imgWidthRe = regexp.MustCompile(`(<img [^>]*?) title="width=(\d+)"`)
)

// mdToHTML renders Markdown as HTML on one line: newlines inside <pre> become
// &#10;, others (between tags) are dropped. A lone paragraph is unwrapped.
func mdToHTML(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := gmHTML.Convert([]byte(md), &buf); err != nil {
		return escapeText(md)
	}
	// Image sizes: HTML has an attribute for what Markdown keeps in the title.
	out := imgWidthRe.ReplaceAllString(strings.TrimSpace(buf.String()), `$1 width="$2"`)
	if strings.HasPrefix(out, "<p>") && strings.HasSuffix(out, "</p>") && strings.Count(out, "<p>") == 1 {
		out = strings.TrimSuffix(strings.TrimPrefix(out, "<p>"), "</p>")
	}
	var b strings.Builder
	last := 0
	for _, m := range preRe.FindAllStringIndex(out, -1) {
		b.WriteString(strings.ReplaceAll(out[last:m[0]], "\n", ""))
		pre := strings.Replace(out[m[0]:m[1]], "\n</code></pre>", "</code></pre>", 1)
		b.WriteString(strings.ReplaceAll(pre, "\n", "&#10;"))
		last = m[1]
	}
	b.WriteString(strings.ReplaceAll(out[last:], "\n", ""))
	return b.String()
}

var htmlTableRe = regexp.MustCompile(`(?is)^<table[\s>].*</table>$`)

// isHTMLTable reports an HTML block holding one table.
func isHTMLTable(s string) bool { return htmlTableRe.MatchString(s) }

// HTML elements kept as they are in storage format.
var keptElems = map[string]bool{
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "th": true, "td": true,
	"p": true, "ul": true, "ol": true, "li": true, "strong": true, "em": true, "b": true, "i": true,
	"u": true, "code": true, "sub": true, "sup": true, "blockquote": true, "hr": true, "br": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// htmlTable writes an HTML table block as a storage table.
func (c *fromMD) htmlTable(raw string) {
	root, err := parse(raw)
	if err != nil {
		c.w.Warn("html-block", "an HTML table could not be read; it is shown as a code block")
		c.code("xml", raw)
		return
	}
	for _, k := range root.kids {
		c.b.WriteString(c.htmlNode(k))
	}
}

func (c *fromMD) htmlNode(n *node) string {
	if n.name == "" {
		if strings.TrimSpace(n.text) == "" && !strings.Contains(n.text, " ") {
			return "" // newlines between tags
		}
		return escapeText(n.text)
	}
	inner := func() string {
		var b strings.Builder
		for _, k := range n.kids {
			b.WriteString(c.htmlNode(k))
		}
		return b.String()
	}
	switch n.name {
	case "a":
		href := n.attr("href")
		body := inner()
		if href == "" {
			return body
		}
		plain := ""
		if allTextNodes(n) {
			plain = n.textContent()
		}
		return c.linkToBody(href, plain, body)
	case "img":
		return c.htmlImage(n)
	case "pre":
		lang := ""
		text := n.textContent()
		if code := n.child("code"); code != nil {
			for _, cls := range strings.Fields(code.attr("class")) {
				if l, ok := strings.CutPrefix(cls, "language-"); ok {
					lang = l
				}
			}
		}
		sub := &fromMD{w: c.w, src: c.src}
		sub.code(lang, strings.TrimSuffix(text, "\n"))
		return sub.b.String()
	case "del", "s", "strike":
		return `<span style="text-decoration: line-through;">` + inner() + `</span>`
	case "input":
		if n.attr("checked") != "" {
			return "[x] "
		}
		return "[ ] "
	}
	if !keptElems[n.name] {
		return inner()
	}
	attrs := ""
	switch n.name {
	case "td", "th":
		for _, a := range []string{"colspan", "rowspan"} {
			if v := span(n.attr(a)); v > 1 {
				attrs += " " + a + `="` + strconv.Itoa(v) + `"`
			}
		}
	case "ol":
		if v := span(n.attr("start")); v > 1 {
			attrs = ` start="` + strconv.Itoa(v) + `"`
		}
	}
	if voidElems[n.name] {
		return "<" + n.name + attrs + "/>"
	}
	return "<" + n.name + attrs + ">" + inner() + "</" + n.name + ">"
}

func allTextNodes(n *node) bool {
	for _, k := range n.kids {
		if k.name != "" {
			return false
		}
	}
	return true
}

func (c *fromMD) htmlImage(n *node) string {
	src, alt := n.attr("src"), n.attr("alt")
	attrs := ""
	if alt != "" {
		attrs += ` ac:alt="` + escapeAttr(alt) + `"`
	}
	width := n.attr("width")
	if m := widthRe.FindStringSubmatch(n.attr("title")); m != nil {
		width = m[1]
	}
	if _, err := strconv.Atoi(width); err == nil {
		attrs += ` ac:width="` + width + `"`
	}
	if hash, ok := model.ParseAttachmentLink(src); ok {
		name, owner, found := c.w.Attachment(hash)
		if !found {
			c.w.Warn("attachment-missing", "an image points to an attachment that is not in this mirror; only its description is shown")
			return escapeText(alt)
		}
		return `<ac:image` + attrs + `>` + riAttachment(name, owner) + `</ac:image>`
	}
	if src == "" {
		return escapeText(alt)
	}
	return `<ac:image` + attrs + `><ri:url ri:value="` + escapeAttr(src) + `"/></ac:image>`
}

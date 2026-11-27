// Package storage converts Confluence storage format (XHTML with ac:/ri:
// elements) to canonical Markdown and back. It is shared by the Confluence
// Data Center/Server and Cloud connectors.
//
// What maps to what is documented in docs/dev/connectors.md. Anything without
// an equivalent degrades visibly and is reported through a warning; unknown
// macros survive as ```confluence-macro blocks holding their raw XML, so a
// Confluence-to-Confluence mirror keeps them.
package storage

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// node is a minimal DOM: an element (name != "") or a text node.
type node struct {
	name  string // "p", "ac:structured-macro", ... ("" for text)
	attrs []attr
	text  string
	kids  []*node
}

type attr struct{ name, value string }

func (n *node) attr(name string) string {
	for _, a := range n.attrs {
		if a.name == name {
			return a.value
		}
	}
	return ""
}

// child returns the first child element called name.
func (n *node) child(name string) *node {
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

// textContent is the concatenated text of n and its descendants.
func (n *node) textContent() string {
	if n.name == "" {
		return n.text
	}
	var b strings.Builder
	for _, k := range n.kids {
		b.WriteString(k.textContent())
	}
	return b.String()
}

// param returns the value of <ac:parameter ac:name="name"> of a macro.
func (n *node) param(name string) string {
	for _, k := range n.kids {
		if k.name == "ac:parameter" && k.attr("ac:name") == name {
			return strings.TrimSpace(k.textContent())
		}
	}
	return ""
}

// Namespace URIs used while parsing; storage format omits the declarations.
const rootOpen = `<tolarix-root xmlns:ac="ac" xmlns:ri="ri" xmlns:at="at">`

// parse reads a storage-format fragment. It is lenient: HTML entities such as
// &nbsp; are accepted and unclosed void elements are closed.
func parse(src string) (*node, error) {
	d := xml.NewDecoder(io.MultiReader(strings.NewReader(rootOpen), strings.NewReader(src), strings.NewReader("</tolarix-root>")))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	root := &node{name: "document"}
	stack := []*node{root}
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: qname(t.Name)}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
					continue
				}
				n.attrs = append(n.attrs, attr{qname(a.Name), a.Value})
			}
			top.kids = append(top.kids, n)
			if !voidElems[n.name] { // RawToken does not apply AutoClose
				stack = append(stack, n)
			}
		case xml.EndElement:
			name := qname(t.Name)
			// Pop to the matching element (tolerates stray end tags).
			for i := len(stack) - 1; i > 0; i-- {
				if stack[i].name == name {
					stack = stack[:i]
					break
				}
			}
		case xml.CharData:
			top.kids = append(top.kids, &node{text: string(t)})
		}
	}
	if len(root.kids) == 1 && root.kids[0].name == "tolarix-root" {
		return root.kids[0], nil
	}
	return root, nil
}

// HTML elements that never have content.
var voidElems = map[string]bool{
	"br": true, "hr": true, "img": true, "col": true, "input": true, "meta": true,
	"link": true, "area": true, "wbr": true, "source": true, "base": true, "embed": true,
}

// qname keeps the source prefix ("ac:link"), lower-cased local names for
// plain HTML elements.
func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return strings.ToLower(n.Local)
}

// serialize writes n back as storage XML (used for unknown macros).
func serialize(n *node) string {
	var b strings.Builder
	writeNode(&b, n)
	return b.String()
}

func writeNode(b *strings.Builder, n *node) {
	if n.name == "" {
		b.WriteString(escapeText(n.text))
		return
	}
	b.WriteString("<" + n.name)
	for _, a := range n.attrs {
		b.WriteString(" " + a.name + `="` + escapeAttr(a.value) + `"`)
	}
	if len(n.kids) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteString(">")
	plain := n.name == "ac:plain-text-body" || n.name == "ac:plain-text-link-body"
	for _, k := range n.kids {
		if plain && k.name == "" {
			b.WriteString(cdata(k.text))
			continue
		}
		writeNode(b, k)
	}
	b.WriteString("</" + n.name + ">")
}

func cdata(s string) string {
	return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>"
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
)

func escapeText(s string) string { return textEscaper.Replace(s) }
func escapeAttr(s string) string { return attrEscaper.Replace(s) }

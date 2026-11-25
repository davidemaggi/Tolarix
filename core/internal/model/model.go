// Package model is the canonical page tree every connector converts to and
// from. Connectors never talk to each other: Master -> canonical -> Mirror.
//
// Bodies are canonical Markdown (CommonMark + GFM tables and task lists) with
// these conventions:
//
//   - Internal page links:  [text](tolarix://page/<escaped-id>[#<escaped heading text>])
//   - Attachment links:     [text](tolarix://att/<sha256>) and ![alt](tolarix://att/<sha256> "width=300")
//   - Callouts:             > [!note] Optional title   (kind lowercase; fold markers + / - kept)
//   - Mermaid:              ```mermaid fenced code blocks
//   - Table of contents:    a line holding only TOCMarker
//   - Unknown macros:       ```confluence-macro fenced blocks holding the raw storage XML
//
// Fragments carry the raw heading text (percent-escaped), not a slug: each
// target slugs headings its own way.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
)

// TOCMarker is the canonical "table of contents here" line. It is an HTML
// comment so targets without a TOC feature (Obsidian) simply hide it.
const TOCMarker = "<!-- tolarix:toc -->"

// Page is one node of the tree.
type Page struct {
	// ID is stable for the page's lifetime in the Master. For path-identity
	// sources (Obsidian, ADO) it is the path, so a rename changes it; the
	// engine pairs such changes up by body hash to detect moves.
	ID       string
	ParentID string // "" for a root page
	// Name is the page's path segment as the source spells it (file name
	// without extension); Title is the display title. Often equal.
	Name  string
	Title string
	Order int // position among siblings, ascending

	Body string     // canonical Markdown, without front matter
	Meta []MetaItem // front matter in source order

	// Placeholder marks a page that exists only to hold children (e.g. an
	// Obsidian folder without a note). Targets that can, write no content.
	Placeholder bool

	// Attachments are the content hashes referenced by Body.
	Attachments []string
}

// MetaItem is one front-matter entry. Value holds YAML/JSON-compatible data.
type MetaItem struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// Attachment is a binary file referenced by one or more pages.
type Attachment struct {
	Hash      string // lowercase hex SHA-256 of the content: the canonical id
	Name      string // original file name, e.g. "diagram.png"
	Size      int64
	MediaType string
	// SourceRef is how the source connector finds the bytes again.
	SourceRef string
	// Role and DerivedFrom are set by renderers (see internal/render).
	Role        AttachmentRole
	DerivedFrom string
}

// AttachmentRole classifies attachments produced or consumed by renderers.
type AttachmentRole string

const (
	RoleNormal        AttachmentRole = ""
	RoleDiagramSource AttachmentRole = "diagram-source"
	RoleRendered      AttachmentRole = "rendered"
)

// Tree is a snapshot of the Master (after scope filtering).
type Tree struct {
	Pages       map[string]*Page
	Attachments map[string]*Attachment
}

// NewTree returns an empty tree.
func NewTree() *Tree {
	return &Tree{Pages: map[string]*Page{}, Attachments: map[string]*Attachment{}}
}

// Add inserts or replaces a page.
func (t *Tree) Add(p *Page) { t.Pages[p.ID] = p }

// Children returns the direct children of id ("" for roots), ordered by
// Order then Title then ID so the result is deterministic.
func (t *Tree) Children(id string) []*Page {
	var out []*Page
	for _, p := range t.Pages {
		if p.ParentID == id {
			out = append(out, p)
		}
	}
	sortPages(out)
	return out
}

// Walk visits pages depth-first, parents before children, in order.
func (t *Tree) Walk(fn func(p *Page, depth int)) {
	var rec func(id string, depth int)
	rec = func(id string, depth int) {
		for _, c := range t.Children(id) {
			fn(c, depth)
			rec(c.ID, depth+1)
		}
	}
	rec("", 0)
}

// Ancestors returns the chain from the root down to p's parent.
func (t *Tree) Ancestors(p *Page) []*Page {
	var chain []*Page
	seen := map[string]bool{p.ID: true}
	for id := p.ParentID; id != ""; {
		a, ok := t.Pages[id]
		if !ok || seen[id] {
			break
		}
		seen[id] = true
		chain = append(chain, a)
		id = a.ParentID
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// Depth returns the number of ancestors of p.
func (t *Tree) Depth(p *Page) int { return len(t.Ancestors(p)) }

func sortPages(ps []*Page) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.ID < b.ID
	})
}

// BodyHash hashes only the body: used to recognise a moved page whose ID changed.
func (p *Page) BodyHash() string { return HashString(p.Body) }

// ContentHash hashes everything a mirror shows for this page except its
// position: title, body, metadata, placeholder flag and attachments.
func (p *Page) ContentHash() string {
	b, _ := json.Marshal(struct {
		Title       string
		Body        string
		Meta        []MetaItem
		Placeholder bool
		Attachments []string
	}{p.Title, p.Body, p.Meta, p.Placeholder, p.Attachments})
	return HashBytes(b)
}

// HashBytes returns the lowercase hex SHA-256 of b.
func HashBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// HashString returns the lowercase hex SHA-256 of s.
func HashString(s string) string { return HashBytes([]byte(s)) }

// HashReader returns the hex SHA-256 and size of everything read from r.
func HashReader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

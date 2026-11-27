package storage

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/model"
)

// PageRef is a page of the Master as the label macros list it.
type PageRef struct {
	ID      string
	Title   string
	Created string // as the API gives it; ISO 8601 sorts as text
	Updated string
}

// LabelIndex is an optional Env extension for the macros that list pages by
// label (Page Properties Report, Content by Label). Only pages of the Master
// are known.
type LabelIndex interface {
	// PagesWithLabels lists the pages carrying the labels: all of them when
	// all is true, any of them otherwise. The order does not matter.
	PagesWithLabels(labels []string, all bool) []PageRef
	// PagePropertiesOf returns the Page Properties of a page of the Master.
	PagePropertiesOf(pageID string) []Property
}

// Macros that only make sense live in Confluence (search boxes, recent
// changes, history): other targets show nothing, Confluence mirrors get them
// back.
var liveOnlyMacros = map[string]bool{
	"livesearch": true, "popular-labels": true, "recently-updated": true,
	"recently-updated-dashboard": true, "change-history": true,
	"pagetreesearch": true, "create-from-template": true, "listlabels": true,
}

// live wraps what a live macro showed when read (nothing, a table, a list)
// in the markers that let a Confluence mirror put the macro back.
func live(n *node, content ...string) []string {
	out := append([]string{model.MacroMarker(serialize(n))}, content...)
	return append(out, model.MacroEnd)
}

var (
	reCQLLabelEq = regexp.MustCompile(`(?i)\blabel\s*(?:=|~)\s*("[^"]*"|'[^']*'|[^\s()]+)`)
	reCQLLabelIn = regexp.MustCompile(`(?i)\blabel\s+in\s*\(([^)]*)\)`)
	reCQLOr      = regexp.MustCompile(`(?i)\s+or\s+`)
)

// macroLabels reads the labels a macro filters on, from its label/labels
// parameter or the label clauses of its CQL. all is true when every label is
// required.
func macroLabels(n *node, operator string) (labels []string, all bool) {
	split := func(s string) {
		for _, l := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
			if l = strings.Trim(l, `"'`); l != "" {
				labels = append(labels, strings.ToLower(l))
			}
		}
	}
	for _, name := range []string{"label", "labels"} {
		if p := paramNode(n, name); p != nil {
			for _, k := range p.kids {
				if k.name == "ri:label" {
					labels = append(labels, strings.ToLower(k.attr("ri:name")))
				}
			}
			if len(labels) == 0 {
				split(p.textContent())
			}
		}
	}
	if len(labels) > 0 {
		return labels, strings.EqualFold(operator, "and")
	}
	cql := n.param("cql")
	for _, m := range reCQLLabelIn.FindAllStringSubmatch(cql, -1) {
		split(m[1])
	}
	in := len(labels) > 0
	for _, m := range reCQLLabelEq.FindAllStringSubmatch(cql, -1) {
		split(m[1])
	}
	return labels, !in && !reCQLOr.MatchString(cql) && len(labels) > 1
}

// propertiesReport renders a Page Properties Report as the table Confluence
// shows: one row per page of the Master with the label and Page Properties.
func (c *toMD) propertiesReport(n *node, idx LabelIndex) []string {
	labels, all := macroLabels(n, "and")
	if len(labels) == 0 {
		return live(n)
	}
	type row struct {
		page  PageRef
		props map[string]string
	}
	var rows []row
	var keys []string
	seen := map[string]bool{}
	for _, p := range idx.PagesWithLabels(labels, all) {
		props := idx.PagePropertiesOf(p.ID)
		if len(props) == 0 {
			continue
		}
		r := row{page: p, props: map[string]string{}}
		for _, pr := range props {
			k := strings.ToLower(pr.Key)
			r.props[k] = propertyText(pr.Value)
			if !seen[k] {
				seen[k] = true
				keys = append(keys, pr.Key)
			}
		}
		rows = append(rows, r)
	}
	if h := n.param("headings"); h != "" {
		keys = nil
		for _, k := range strings.Split(h, ",") {
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
	}
	if len(rows) == 0 {
		return live(n)
	}
	sortBy := strings.ToLower(n.param("sortBy"))
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].page.Title, rows[j].page.Title
		if sortBy != "" && sortBy != "title" {
			a, b = rows[i].props[sortBy], rows[j].props[sortBy]
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
	if n.param("reverseSort") == "true" {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	var b strings.Builder
	b.WriteString("| " + cellEscape(firstNonEmpty(n.param("firstcolumn"), "Title")))
	for _, k := range keys {
		b.WriteString(" | " + cellEscape(k))
	}
	b.WriteString(" |\n|" + strings.Repeat(" --- |", len(keys)+1))
	for _, r := range rows {
		b.WriteString("\n| [" + cellEscape(r.page.Title) + "](" + model.PageLink(r.page.ID, "") + ")")
		for _, k := range keys {
			b.WriteString(" | " + cellEscape(r.props[strings.ToLower(k)]))
		}
		b.WriteString(" |")
	}
	return live(n, b.String())
}

// contentByLabel renders a Content by Label macro as a list of links to the
// pages of the Master with the labels, sorted and capped like Confluence.
func (c *toMD) contentByLabel(n *node, idx LabelIndex) []string {
	labels, all := macroLabels(n, n.param("operator"))
	if len(labels) == 0 {
		return live(n)
	}
	pages := idx.PagesWithLabels(labels, all)
	if n.param("excludeCurrent") == "true" {
		self := c.env.SelfID()
		pages = slices.DeleteFunc(pages, func(p PageRef) bool { return p.ID == self })
	}
	sortBy := strings.ToLower(n.param("sort"))
	reverse := n.param("reverse") == "true"
	sort.SliceStable(pages, func(i, j int) bool {
		switch sortBy {
		case "title":
			return strings.ToLower(pages[i].Title) < strings.ToLower(pages[j].Title)
		case "creation":
			return pages[i].Created > pages[j].Created // newest first
		default: // "modified", Confluence's default
			return pages[i].Updated > pages[j].Updated
		}
	})
	if reverse {
		for i, j := 0, len(pages)-1; i < j; i, j = i+1, j-1 {
			pages[i], pages[j] = pages[j], pages[i]
		}
	}
	limit := 5 // Confluence's default
	if v, err := strconv.Atoi(n.param("max")); err == nil && v > 0 {
		limit = v
	}
	if len(pages) > limit {
		pages = pages[:limit]
	}
	if len(pages) == 0 {
		return live(n)
	}
	items := make([]string, len(pages))
	for i, p := range pages {
		items[i] = "- [" + escapeMD(p.Title) + "](" + model.PageLink(p.ID, "") + ")"
	}
	return live(n, strings.Join(items, "\n"))
}

func propertyText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []string:
		return strings.Join(v, ", ")
	}
	return ""
}

// cellEscape makes plain text safe inside a pipe-table cell.
func cellEscape(s string) string {
	return escapeMD(collapseSpaces(s))
}

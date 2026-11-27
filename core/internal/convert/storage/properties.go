package storage

import "strings"

// Property is one entry of a Page Properties macro.
type Property struct {
	Key   string
	Value any // string, or []string for a cell holding a list
}

// PageProperties reads the Page Properties (details) macros of a storage
// body, hidden ones included. A table either has a key and a value on each
// row, or a header row of keys over one row of values. Values are plain
// text; users (when users is not nil) show as display names.
func PageProperties(src string, users UserResolver) []Property {
	root, err := parse(src)
	if err != nil {
		return nil
	}
	var out []Property
	seen := map[string]bool{}
	add := func(key *node, value *node) {
		k := collapseSpaces(plainCell(key, users))
		if k == "" || seen[strings.ToLower(k)] {
			return
		}
		seen[strings.ToLower(k)] = true
		out = append(out, Property{Key: k, Value: cellValue(value, users)})
	}
	var walk func(n *node)
	walk = func(n *node) {
		for _, k := range n.kids {
			if isMacro(k) && macroName(k) == "details" {
				if body := k.child("ac:rich-text-body"); body != nil {
					if t := findDescendant(body, "table"); t != nil {
						rows := tableRows(t)
						switch {
						case len(rows) == 2 && len(rows[0]) > 1 && allHeaderCells(rows[0]):
							for i, key := range rows[0] {
								if i < len(rows[1]) {
									add(key, rows[1][i])
								}
							}
						default:
							for _, r := range rows {
								if len(r) >= 2 {
									add(r[0], r[1])
								}
							}
						}
					}
				}
				continue
			}
			walk(k)
		}
	}
	walk(root)
	return out
}

func tableRows(t *node) [][]*node {
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
	collect(t)
	return rows
}

func cellValue(n *node, users UserResolver) any {
	var items []string
	var walk func(n *node)
	walk = func(n *node) {
		for _, k := range n.kids {
			if k.name == "li" || k.name == "ac:task" {
				if s := collapseSpaces(plainCell(k, users)); s != "" {
					items = append(items, s)
				}
				continue
			}
			walk(k)
		}
	}
	walk(n)
	if len(items) > 0 {
		return items
	}
	return collapseSpaces(plainCell(n, users))
}

// plainCell is the text of a cell as a reader sees it.
func plainCell(n *node, users UserResolver) string {
	if n.name == "" {
		return n.text
	}
	switch {
	case n.name == "ri:user":
		acc, key, name := n.attr("ri:account-id"), n.attr("ri:userkey"), n.attr("ri:username")
		if users != nil {
			if dn, ok := users.UserName(acc, key, name); ok {
				return dn
			}
		}
		return firstNonEmpty(name, key, acc)
	case n.name == "ac:link" && n.child("ac:plain-text-link-body") == nil && n.child("ac:link-body") == nil:
		if p := n.child("ri:page"); p != nil {
			return p.attr("ri:content-title")
		}
		if a := n.child("ri:attachment"); a != nil {
			return a.attr("ri:filename")
		}
	case n.name == "time":
		return n.attr("datetime")
	case isMacro(n) && macroName(n) == "status":
		return n.param("title")
	case n.name == "ac:parameter" || n.name == "ac:task-id" || n.name == "ac:task-status":
		return ""
	case n.name == "br" || n.name == "p" || n.name == "li":
		var b strings.Builder
		for _, k := range n.kids {
			b.WriteString(plainCell(k, users))
		}
		return " " + b.String() + " "
	}
	var b strings.Builder
	for _, k := range n.kids {
		b.WriteString(plainCell(k, users))
	}
	return b.String()
}

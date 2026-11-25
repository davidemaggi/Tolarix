package engine

import (
	"fmt"
	"strings"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// ApplyScope returns the part of tree the flow mirrors.
//
// In "selected" mode each included subtree becomes a top-level page of the
// mirror (its Master ancestors are not copied). Exclusions win over
// inclusions. Attachments no longer referenced are dropped.
func ApplyScope(tree *model.Tree, s Scope, w connectors.Warner) *model.Tree {
	excluded := map[string]bool{}
	for _, id := range s.Exclude {
		if _, ok := tree.Pages[id]; !ok {
			w.Warn(connectors.Warning{PageID: id, Code: "scope-missing",
				Message: fmt.Sprintf("the excluded page %q no longer exists in the Master", id)})
		}
		excluded[id] = true
	}
	isExcluded := func(p *model.Page) bool {
		if excluded[p.ID] {
			return true
		}
		for _, a := range tree.Ancestors(p) {
			if excluded[a.ID] {
				return true
			}
		}
		return false
	}

	out := model.NewTree()
	keep := func(p *model.Page, parent string) {
		cp := *p
		cp.ParentID = parent
		cp.Attachments = append([]string(nil), p.Attachments...)
		cp.Meta = append([]model.MetaItem(nil), p.Meta...)
		out.Add(&cp)
	}

	if !strings.EqualFold(s.Mode, "selected") {
		for _, p := range tree.Pages {
			if !isExcluded(p) {
				keep(p, p.ParentID)
			}
		}
	} else {
		roots := map[string]bool{}
		for _, id := range s.Include {
			if _, ok := tree.Pages[id]; !ok {
				w.Warn(connectors.Warning{PageID: id, Code: "scope-missing",
					Message: fmt.Sprintf("the selected page %q no longer exists in the Master; edit the flow's scope", id)})
				continue
			}
			roots[id] = true
		}
		for _, p := range tree.Pages {
			if isExcluded(p) {
				continue
			}
			if roots[p.ID] {
				// A selected page nested inside another selected page stays under it.
				parent := ""
				for _, a := range tree.Ancestors(p) {
					if roots[a.ID] {
						parent = p.ParentID
						break
					}
				}
				keep(p, parent)
				continue
			}
			for _, a := range tree.Ancestors(p) {
				if roots[a.ID] {
					keep(p, p.ParentID)
					break
				}
			}
		}
	}
	for _, p := range out.Pages {
		for _, h := range p.Attachments {
			if a, ok := tree.Attachments[h]; ok {
				cp := *a
				out.Attachments[h] = &cp
			}
		}
	}
	return out
}

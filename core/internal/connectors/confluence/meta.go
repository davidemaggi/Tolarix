package confluence

import (
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/convert/storage"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Front matter keys of the "page details" option.
var detailKeys = []string{"confluence_id", "confluence_url", "author", "created", "updated", "updated_by", "version"}

// infos reads the history and labels of every page, when an option needs
// them. A page whose details cannot be read gets a warning and no details.
func (s *snapshot) infos(pages []fetched) map[string]*pageInfo {
	if !s.c.opts.Properties && !s.c.opts.PageDetails && !needsLabels(pages) {
		return nil
	}
	out := make([]*pageInfo, len(pages))
	g, gctx := errgroup.WithContext(s.ctx)
	g.SetLimit(fetchers)
	for i, f := range pages {
		if f.page.Folder {
			continue
		}
		g.Go(func() error {
			info, err := s.c.api.info(gctx, f.page.ID)
			if err != nil {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				s.w.Warn(connectors.Warning{PageID: f.page.ID, Code: "page-info-unreadable",
					Message: fmt.Sprintf("the labels and details of %q could not be read (%v); its front matter leaves them out", f.page.Title, err)})
				return nil
			}
			out[i] = info
			return nil
		})
	}
	_ = g.Wait() // a cancelled run fails at the next request
	m := make(map[string]*pageInfo, len(pages))
	for i, f := range pages {
		if out[i] != nil {
			m[f.page.ID] = out[i]
		}
	}
	return m
}

// needsLabels tells whether a page lists other pages by label (Page
// Properties Report, Content by Label), which needs the labels of every page.
func needsLabels(pages []fetched) bool {
	for _, f := range pages {
		if strings.Contains(f.page.Body, `ac:name="detailssummary"`) || strings.Contains(f.page.Body, `ac:name="contentbylabel"`) {
			return true
		}
	}
	return false
}

// meta is the front matter of a page: labels as tags, page details, then
// the Page Properties, each key once.
func (s *snapshot) meta(p page, info *pageInfo, env *pageEnv) []model.MetaItem {
	var out []model.MetaItem
	seen := map[string]bool{}
	add := func(k string, v any) {
		if seen[strings.ToLower(k)] {
			return
		}
		seen[strings.ToLower(k)] = true
		out = append(out, model.MetaItem{Key: k, Value: v})
	}
	if s.c.opts.Properties && info != nil && len(info.Labels) > 0 {
		add("tags", info.Labels)
	}
	if s.c.opts.PageDetails && info != nil {
		add("confluence_id", p.ID)
		if info.WebUI != "" {
			add("confluence_url", strings.TrimRight(s.c.addr.BaseURL, "/")+info.WebUI)
		}
		if n := s.userName(info.CreatedBy); n != "" {
			add("author", n)
		}
		if info.CreatedAt != "" {
			add("created", info.CreatedAt)
		}
		if info.UpdatedAt != "" {
			add("updated", info.UpdatedAt)
		}
		if n := s.userName(info.UpdatedBy); n != "" {
			add("updated_by", n)
		}
		if info.Version > 0 {
			add("version", info.Version)
		}
	}
	if s.c.opts.Properties {
		if s.c.opts.PageDetails {
			for _, k := range detailKeys {
				seen[k] = true
			}
		}
		seen["tags"] = true
		for _, pr := range storage.PageProperties(p.Body, env) {
			add(pr.Key, pr.Value)
		}
	}
	return out
}

func (s *snapshot) userName(ref userRef) string {
	if n, ok := s.users.name(ref); ok {
		return n
	}
	return firstNonEmpty(ref.Username, ref.AccountID, ref.UserKey)
}

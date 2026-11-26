package confluence

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/davidemaggi/tolarix/core/internal/httpc"
)

// v1 is the REST API v1 of Confluence Data Center / Server.
type v1 struct{ c *Confluence }

var _ api = (*v1)(nil)

const (
	pageExpand = "body.storage,version"
	pageLimit  = 100
	fetchers   = 8
)

func (a *v1) page(ctx context.Context, id string, body bool) (*page, error) {
	expand := "space,version,children.page"
	if body {
		expand += ",body.storage"
	}
	var p content
	if err := a.c.client.Get(ctx, "/rest/api/content/"+url.PathEscape(id)+"?expand="+expand, &p); err != nil {
		return nil, err
	}
	return p.toPage(), nil
}

func (a *v1) pageByTitle(ctx context.Context, title string) (*page, error) {
	q := url.Values{"spaceKey": {a.c.addr.SpaceKey}, "title": {title}, "type": {"page"}, "expand": {"version"}}
	var res results[content]
	if err := a.c.client.Get(ctx, "/rest/api/content?"+q.Encode(), &res); err != nil {
		return nil, err
	}
	if len(res.Results) == 0 {
		return nil, nil
	}
	return res.Results[0].toPage(), nil
}

func (a *v1) space(ctx context.Context) error {
	var sp struct {
		Key string `json:"key"`
	}
	return a.c.client.Get(ctx, "/rest/api/space/"+url.PathEscape(a.c.addr.SpaceKey), &sp)
}

// tree lists the pages breadth-first, children in Confluence's order.
func (a *v1) tree(ctx context.Context, rootID string) ([]fetched, error) {
	c := a.c
	var level []fetched
	if rootID != "" {
		var root content
		if err := c.client.Get(ctx, "/rest/api/content/"+url.PathEscape(rootID)+"?expand="+pageExpand, &root); err != nil {
			return nil, c.friendly(err, "the page "+rootID)
		}
		level = []fetched{{page: *root.toPage()}}
	} else {
		roots, err := list[content](ctx, c.client, "/rest/api/space/"+url.PathEscape(c.addr.SpaceKey)+"/content/page?depth=root&expand="+pageExpand, pageLimit)
		if err != nil {
			return nil, c.friendly(err, "the space "+c.addr.SpaceKey)
		}
		for i, r := range roots {
			level = append(level, fetched{page: *r.toPage(), order: i})
		}
	}
	var all []fetched
	seen := map[string]bool{}
	for len(level) > 0 {
		all = append(all, level...)
		kids := make([][]content, len(level))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(fetchers)
		for i, f := range level {
			seen[f.page.ID] = true
			g.Go(func() error {
				ch, err := list[content](gctx, c.client, "/rest/api/content/"+url.PathEscape(f.page.ID)+"/child/page?expand="+pageExpand, pageLimit)
				if err != nil {
					return fmt.Errorf("list the child pages of %q: %w", f.page.Title, err)
				}
				kids[i] = ch
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		var next []fetched
		for i, f := range level {
			for j, k := range kids[i] {
				if !seen[k.ID] {
					next = append(next, fetched{page: *k.toPage(), parent: f.page.ID, order: j})
				}
			}
		}
		level = next
	}
	return all, nil
}

func (a *v1) children(ctx context.Context, parentID string) ([]page, error) {
	c := a.c
	var pages []content
	var err error
	if parentID == "" {
		pages, err = list[content](ctx, c.client, "/rest/api/space/"+url.PathEscape(c.addr.SpaceKey)+"/content/page?depth=root&expand=children.page", pageLimit)
	} else {
		pages, err = list[content](ctx, c.client, "/rest/api/content/"+url.PathEscape(parentID)+"/child/page?expand=children.page", pageLimit)
	}
	if err != nil {
		return nil, err
	}
	out := make([]page, 0, len(pages))
	for _, p := range pages {
		out = append(out, *p.toPage())
	}
	return out, nil
}

func (a *v1) allPages(ctx context.Context) ([]page, error) {
	q := url.Values{"spaceKey": {a.c.addr.SpaceKey}, "type": {"page"}, "expand": {"version"}}
	pages, err := list[content](ctx, a.c.client, "/rest/api/content?"+q.Encode(), 200)
	if err != nil {
		return nil, err
	}
	out := make([]page, 0, len(pages))
	for _, p := range pages {
		out = append(out, *p.toPage())
	}
	return out, nil
}

func (a *v1) attachments(ctx context.Context, pageID string) ([]attachment, error) {
	list, err := list[content](ctx, a.c.client, "/rest/api/content/"+url.PathEscape(pageID)+"/child/attachment?expand=version", 200)
	if err != nil {
		return nil, err
	}
	out := make([]attachment, 0, len(list))
	for _, x := range list {
		at := attachment{ID: x.ID, Title: x.Title, Version: x.version(), Download: x.Links.Download}
		if x.Extensions != nil {
			at.MediaType = x.Extensions.MediaType
		}
		out = append(out, at)
	}
	return out, nil
}

// pageBody is a v1 page write.
type pageBody struct {
	ID        string              `json:"id,omitempty"`
	Type      string              `json:"type"`
	Title     string              `json:"title"`
	Space     map[string]string   `json:"space"`
	Ancestors []map[string]string `json:"ancestors,omitempty"`
	Version   *v1Version          `json:"version,omitempty"`
	Body      struct {
		Storage struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"storage"`
	} `json:"body"`
}

type v1Version struct {
	Number    int  `json:"number"`
	MinorEdit bool `json:"minorEdit"`
}

func (a *v1) newBody(title, parentID, value string) *pageBody {
	b := &pageBody{Type: "page", Title: title, Space: map[string]string{"key": a.c.addr.SpaceKey}}
	if parentID != "" {
		b.Ancestors = []map[string]string{{"id": parentID}}
	}
	b.Body.Storage.Value = value
	b.Body.Storage.Representation = "storage"
	return b
}

func (a *v1) create(ctx context.Context, title, parentID, body string) (*page, error) {
	var res content
	if err := a.c.client.JSON(ctx, http.MethodPost, "/rest/api/content", a.newBody(title, parentID, body), &res); err != nil {
		return nil, err
	}
	return res.toPage(), nil
}

func (a *v1) update(ctx context.Context, id, title, parentID, body string, version int) (*page, error) {
	b := a.newBody(title, parentID, body)
	b.ID = id
	b.Version = &v1Version{Number: version, MinorEdit: true}
	var res content
	if err := a.c.client.JSON(ctx, http.MethodPut, "/rest/api/content/"+url.PathEscape(id), b, &res); err != nil {
		return nil, err
	}
	return res.toPage(), nil
}

func (a *v1) deletePage(ctx context.Context, id string) error {
	return a.c.client.JSON(ctx, http.MethodDelete, "/rest/api/content/"+url.PathEscape(id), nil, nil)
}

func (a *v1) deleteAttachment(ctx context.Context, id string) error {
	return a.c.client.JSON(ctx, http.MethodDelete, "/rest/api/content/"+url.PathEscape(id), nil, nil)
}

func (a *v1) defaultParent(context.Context) (string, string, error) { return "", "", nil }

// ---- v1 types ----

type results[T any] struct {
	Results []T `json:"results"`
	Size    int `json:"size"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type content struct {
	ID     string `json:"id"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
	Title  string `json:"title"`
	Space  *struct {
		Key string `json:"key"`
	} `json:"space,omitempty"`
	Version *struct {
		Number int `json:"number"`
	} `json:"version,omitempty"`
	Body *struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body,omitempty"`
	Children *struct {
		Page *struct {
			Size int `json:"size"`
		} `json:"page"`
	} `json:"children,omitempty"`
	Extensions *struct {
		MediaType string `json:"mediaType"`
		FileSize  int64  `json:"fileSize"`
	} `json:"extensions,omitempty"`
	Links struct {
		Download string `json:"download"`
	} `json:"_links"`
}

func (p *content) version() int {
	if p.Version == nil {
		return 0
	}
	return p.Version.Number
}

func (p *content) toPage() *page {
	out := &page{ID: p.ID, Title: p.Title, Version: p.version()}
	if p.Body != nil {
		out.Body = p.Body.Storage.Value
	}
	if p.Space != nil {
		out.SpaceKey = p.Space.Key
	}
	out.HasChildren = p.Children != nil && p.Children.Page != nil && p.Children.Page.Size > 0
	return out
}

// list fetches every page of a paged v1 collection.
func list[T any](ctx context.Context, c *httpc.Client, path string, limit int) ([]T, error) {
	var out []T
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for start := 0; ; {
		var res results[T]
		if err := c.Get(ctx, fmt.Sprintf("%s%sstart=%d&limit=%d", path, sep, start, limit), &res); err != nil {
			return nil, err
		}
		out = append(out, res.Results...)
		if res.Links.Next == "" || len(res.Results) == 0 {
			return out, nil
		}
		start += len(res.Results)
	}
}

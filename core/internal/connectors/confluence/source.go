package confluence

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/convert/storage"
	"github.com/davidemaggi/tolarix/core/internal/httpc"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

const (
	pageExpand = "body.storage,version"
	pageLimit  = 100
	fetchers   = 8
	drawioType = "application/vnd.jgraph.mxfile"
)

// hashCache remembers attachment hashes across runs in this process, keyed
// by base URL, attachment id and version, so unchanged attachments are not
// downloaded again just to be hashed.
var hashCache sync.Map // string -> hashed

type hashed struct {
	hash string
	size int64
}

type fetched struct {
	page   content
	parent string
	order  int
}

// Snapshot reads the configured page and its subtree, or the whole space.
func (c *Confluence) Snapshot(ctx context.Context, w connectors.Warner) (*model.Tree, error) {
	if err := c.Test(ctx); err != nil {
		return nil, err
	}
	pages, err := c.fetchTree(ctx)
	if err != nil {
		return nil, err
	}
	snap := &snapshot{c: c, ctx: ctx, w: w, tree: model.NewTree(),
		byTitle: map[string]string{}, raw: map[string]string{}, titles: map[string]string{},
		atts: map[string]map[string]content{}}
	for _, f := range pages {
		snap.byTitle[f.page.Title] = f.page.ID
		snap.raw[f.page.ID] = f.page.storage()
		snap.titles[f.page.ID] = f.page.Title
	}
	for _, f := range pages {
		p := &model.Page{ID: f.page.ID, ParentID: f.parent, Name: f.page.Title, Title: f.page.Title, Order: f.order}
		env := &pageEnv{snap: snap, page: p}
		body, err := storage.ToMarkdown(f.page.storage(), env)
		if err != nil {
			w.Warn(connectors.Warning{PageID: p.ID, Code: "page-unreadable",
				Message: fmt.Sprintf("the content of %q could not be read (%v); the page is mirrored empty", p.Title, err)})
		}
		p.Body = body
		snap.tree.Add(p)
	}
	return snap.tree, nil
}

// fetchTree lists the pages breadth-first, children in Confluence's order.
func (c *Confluence) fetchTree(ctx context.Context) ([]fetched, error) {
	var level []fetched
	if c.addr.PageID != "" {
		var root content
		if err := c.client.Get(ctx, "/rest/api/content/"+url.PathEscape(c.addr.PageID)+"?expand="+pageExpand, &root); err != nil {
			return nil, c.friendly(err, "the page "+c.addr.PageID)
		}
		level = []fetched{{page: root}}
	} else {
		roots, err := list[content](ctx, c.client, "/rest/api/space/"+url.PathEscape(c.addr.SpaceKey)+"/content/page?depth=root&expand="+pageExpand, pageLimit)
		if err != nil {
			return nil, c.friendly(err, "the space "+c.addr.SpaceKey)
		}
		for i, r := range roots {
			level = append(level, fetched{page: r, order: i})
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
					next = append(next, fetched{page: k, parent: f.page.ID, order: j})
				}
			}
		}
		level = next
	}
	return all, nil
}

// snapshot is the state shared by the pages of one Snapshot call.
type snapshot struct {
	c       *Confluence
	ctx     context.Context //nolint:containedctx // scoped to one Snapshot call
	w       connectors.Warner
	tree    *model.Tree
	byTitle map[string]string // title -> page id, pages in the snapshot
	titles  map[string]string // page id -> title
	raw     map[string]string // page id -> storage body
	heads   map[string][]string
	atts    map[string]map[string]content // page id -> filename -> attachment
}

func (s *snapshot) attachments(pageID string) map[string]content {
	if m, ok := s.atts[pageID]; ok {
		return m
	}
	m := map[string]content{}
	list, err := list[content](s.ctx, s.c.client, "/rest/api/content/"+url.PathEscape(pageID)+"/child/attachment?expand=version", 200)
	if err != nil {
		s.w.Warn(connectors.Warning{PageID: pageID, Code: "attachments-unreadable",
			Message: fmt.Sprintf("the attachments of %q could not be listed: %v", s.titles[pageID], err)})
	}
	for _, a := range list {
		m[a.Title] = a
	}
	s.atts[pageID] = m
	return m
}

// register hashes an attachment (downloading it unless cached) and adds it
// to the tree.
func (s *snapshot) register(a content, name string, role model.AttachmentRole, mediaType string) (string, error) {
	key := s.c.addr.BaseURL + "|" + a.ID + "|" + strconv.Itoa(a.version())
	h, ok := hashCache.Load(key)
	if !ok {
		rc, err := s.c.OpenAttachment(s.ctx, a.Links.Download)
		if err != nil {
			return "", err
		}
		hash, size, err := model.HashReader(rc)
		_ = rc.Close()
		if err != nil {
			return "", err
		}
		h = hashed{hash, size}
		hashCache.Store(key, h)
	}
	hv := h.(hashed)
	if mediaType == "" && a.Extensions != nil {
		mediaType = a.Extensions.MediaType
	}
	if existing := s.tree.Attachments[hv.hash]; existing == nil {
		s.tree.Attachments[hv.hash] = &model.Attachment{Hash: hv.hash, Name: name, Size: hv.size,
			MediaType: mediaType, SourceRef: a.Links.Download, Role: role}
	} else if role != "" {
		existing.Role = role
	}
	return hv.hash, nil
}

// pageEnv resolves references for one page (storage.Env).
type pageEnv struct {
	snap *snapshot
	page *model.Page
}

func (e *pageEnv) SelfID() string { return e.page.ID }

func (e *pageEnv) PageID(space, title string) (string, bool) {
	if space != "" && !strings.EqualFold(space, e.snap.c.addr.SpaceKey) {
		return "", false
	}
	id, ok := e.snap.byTitle[title]
	return id, ok
}

func (e *pageEnv) Heading(pageID, anchor string) string {
	if e.snap.heads == nil {
		e.snap.heads = map[string][]string{}
	}
	hs, ok := e.snap.heads[pageID]
	if !ok {
		hs = storage.Headings(e.snap.raw[pageID])
		e.snap.heads[pageID] = hs
	}
	// Anchors written by Confluence may carry the page title as a prefix.
	for _, cand := range []string{anchor, strings.TrimPrefix(anchor, strings.ReplaceAll(e.snap.titles[pageID], " ", "")+"-")} {
		if h, ok := storage.HeadingForAnchor(hs, cand); ok {
			return h
		}
	}
	return anchor
}

func (e *pageEnv) owner(ownerTitle string) (string, bool) {
	if ownerTitle == "" || ownerTitle == e.page.Title {
		return e.page.ID, true
	}
	id, ok := e.snap.byTitle[ownerTitle]
	return id, ok
}

func (e *pageEnv) Attachment(ownerTitle, filename string) (string, bool) {
	ownerID, ok := e.owner(ownerTitle)
	if !ok {
		return "", false
	}
	a, ok := e.snap.attachments(ownerID)[filename]
	if !ok {
		return "", false
	}
	return e.add(a, filename, "", "")
}

func (e *pageEnv) add(a content, name string, role model.AttachmentRole, mediaType string) (string, bool) {
	hash, err := e.snap.register(a, name, role, mediaType)
	if err != nil {
		e.Warn("attachment-failed", fmt.Sprintf("the attachment %q could not be downloaded: %v", name, err))
		return "", false
	}
	e.page.Attachments = appendUnique(e.page.Attachments, hash)
	return hash, true
}

// Diagram registers a draw.io diagram (stored as an attachment named after
// the diagram, with or without .drawio) and its PNG preview, which the
// draw.io renderer reuses.
func (e *pageEnv) Diagram(ownerID, name string) (string, bool) {
	if ownerID == "" {
		ownerID = e.page.ID
	}
	if _, inSnapshot := e.snap.titles[ownerID]; !inSnapshot {
		return "", false
	}
	atts := e.snap.attachments(ownerID)
	src, ok := atts[name]
	if !ok {
		if src, ok = atts[name+".drawio"]; !ok {
			return "", false
		}
	}
	hash, ok := e.add(src, strings.TrimSuffix(name, ".drawio")+".drawio", model.RoleDiagramSource, drawioType)
	if !ok {
		return "", false
	}
	if png, ok := atts[strings.TrimSuffix(name, ".drawio")+".png"]; ok {
		e.add(png, png.Title, "", "image/png")
	}
	return hash, true
}

func (e *pageEnv) Warn(code, message string) {
	e.snap.w.Warn(connectors.Warning{PageID: e.page.ID, Code: code, Message: message})
}

func appendUnique(list []string, h string) []string {
	for _, x := range list {
		if x == h {
			return list
		}
	}
	return append(list, h)
}

// OpenAttachment downloads an attachment by its download path.
func (c *Confluence) OpenAttachment(ctx context.Context, ref string) (io.ReadCloser, error) {
	if ref == "" {
		return nil, fmt.Errorf("empty attachment reference")
	}
	resp, err := c.client.Do(ctx, httpc.Request{Method: http.MethodGet, Path: ref, Header: http.Header{"Accept": {"*/*"}}})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Browse lists the children of parentID; "" lists the configured page, or
// the top-level pages of the space.
func (c *Confluence) Browse(ctx context.Context, parentID string) ([]connectors.Node, error) {
	if err := c.resolve(ctx); err != nil {
		return nil, err
	}
	var pages []content
	switch {
	case parentID == "" && c.addr.PageID != "":
		var p content
		if err := c.client.Get(ctx, "/rest/api/content/"+url.PathEscape(c.addr.PageID)+"?expand=children.page", &p); err != nil {
			return nil, c.friendly(err, "the page "+c.addr.PageID)
		}
		pages = []content{p}
	case parentID == "":
		var err error
		if pages, err = list[content](ctx, c.client, "/rest/api/space/"+url.PathEscape(c.addr.SpaceKey)+"/content/page?depth=root&expand=children.page", pageLimit); err != nil {
			return nil, c.friendly(err, "the space "+c.addr.SpaceKey)
		}
	default:
		var err error
		if pages, err = list[content](ctx, c.client, "/rest/api/content/"+url.PathEscape(parentID)+"/child/page?expand=children.page", pageLimit); err != nil {
			return nil, err
		}
	}
	out := make([]connectors.Node, 0, len(pages))
	for _, p := range pages {
		has := p.Children != nil && p.Children.Page != nil && p.Children.Page.Size > 0
		out = append(out, connectors.Node{ID: p.ID, Title: p.Title, HasChildren: has})
	}
	return out, nil
}

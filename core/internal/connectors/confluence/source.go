package confluence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/convert/storage"
	"github.com/davidemaggi/tolarix/core/internal/httpc"
	"github.com/davidemaggi/tolarix/core/internal/model"
)

const drawioType = "application/vnd.jgraph.mxfile"

// hashCache remembers attachment hashes across runs in this process, keyed
// by base URL, attachment id and version, so unchanged attachments are not
// downloaded again just to be hashed.
var hashCache sync.Map // string -> hashed

type hashed struct {
	hash string
	size int64
}

// Snapshot reads the configured page and its subtree, or the whole space.
func (c *Confluence) Snapshot(ctx context.Context, w connectors.Warner) (*model.Tree, error) {
	if err := c.Test(ctx); err != nil {
		return nil, err
	}
	pages, err := c.api.tree(ctx, c.addr.PageID)
	if err != nil {
		return nil, err
	}
	snap := &snapshot{c: c, ctx: ctx, w: w, tree: model.NewTree(),
		byTitle: map[string]string{}, raw: map[string]string{}, titles: map[string]string{},
		atts:  map[string]map[string]attachment{},
		users: &users{c: c, ctx: ctx, cache: map[string]string{}}}
	for _, f := range pages {
		snap.byTitle[f.page.Title] = f.page.ID
		snap.raw[f.page.ID] = f.page.Body
		snap.titles[f.page.ID] = f.page.Title
	}
	snap.info = snap.infos(pages)
	for _, f := range pages {
		p := &model.Page{ID: f.page.ID, ParentID: f.parent, Name: f.page.Title, Title: f.page.Title, Order: f.order,
			Placeholder: f.page.Folder}
		env := &pageEnv{snap: snap, page: p}
		body, err := storage.ToMarkdown(f.page.Body, env)
		if err != nil {
			w.Warn(connectors.Warning{PageID: p.ID, Code: "page-unreadable",
				Message: fmt.Sprintf("the content of %q could not be read (%v); the page is mirrored empty", p.Title, err)})
		}
		p.Body = body
		if !f.page.Folder {
			p.Meta = snap.meta(f.page, snap.info[f.page.ID], env)
		}
		snap.tree.Add(p)
	}
	return snap.tree, nil
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
	atts    map[string]map[string]attachment // page id -> filename -> attachment
	users   *users
	info    map[string]*pageInfo // page id -> history and labels, when needed
	props   map[string][]storage.Property
}

func (s *snapshot) attachments(pageID string) map[string]attachment {
	if m, ok := s.atts[pageID]; ok {
		return m
	}
	m := map[string]attachment{}
	list, err := s.c.api.attachments(s.ctx, pageID)
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
func (s *snapshot) register(a attachment, name string, role model.AttachmentRole, mediaType string) (string, error) {
	key := s.c.addr.BaseURL + "|" + a.ID + "|" + strconv.Itoa(a.Version)
	h, ok := hashCache.Load(key)
	if !ok {
		rc, err := s.c.OpenAttachment(s.ctx, a.Download)
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
	if mediaType == "" {
		mediaType = a.MediaType
	}
	if existing := s.tree.Attachments[hv.hash]; existing == nil {
		s.tree.Attachments[hv.hash] = &model.Attachment{Hash: hv.hash, Name: name, Size: hv.size,
			MediaType: mediaType, SourceRef: a.Download, Role: role}
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

// UserName shows mentions as display names (storage.UserResolver).
func (e *pageEnv) UserName(accountID, userKey, username string) (string, bool) {
	return e.snap.users.name(userRef{AccountID: accountID, UserKey: userKey, Username: username})
}

// PagesWithLabels lists the pages of the snapshot with the labels
// (storage.LabelIndex).
func (e *pageEnv) PagesWithLabels(labels []string, all bool) []storage.PageRef {
	var out []storage.PageRef
	for id, info := range e.snap.info {
		have := map[string]bool{}
		for _, l := range info.Labels {
			have[strings.ToLower(l)] = true
		}
		hits := 0
		for _, l := range labels {
			if have[l] {
				hits++
			}
		}
		if hits == 0 || (all && hits < len(labels)) {
			continue
		}
		out = append(out, storage.PageRef{ID: id, Title: e.snap.titles[id], Created: info.CreatedAt, Updated: info.UpdatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PagePropertiesOf reads the Page Properties of a page of the snapshot
// (storage.LabelIndex).
func (e *pageEnv) PagePropertiesOf(pageID string) []storage.Property {
	if e.snap.props == nil {
		e.snap.props = map[string][]storage.Property{}
	}
	if p, ok := e.snap.props[pageID]; ok {
		return p
	}
	p := storage.PageProperties(e.snap.raw[pageID], e)
	e.snap.props[pageID] = p
	return p
}

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

var (
	reLinkSpacePage = regexp.MustCompile(`/spaces/[^/]+/pages/(\d+)`)
	reLinkDisplay   = regexp.MustCompile(`/display/([^/]+)/([^/?#]+)/?$`)
)

// PageByURL maps the web address of a page of the snapshot (Cloud smart
// links, pasted addresses) to the page (storage.PageURLResolver).
func (e *pageEnv) PageByURL(href string) (string, string, string, bool) {
	u, err := url.Parse(href)
	if err != nil {
		return "", "", "", false
	}
	base, err := url.Parse(e.snap.c.addr.BaseURL)
	if err != nil {
		return "", "", "", false
	}
	if u.IsAbs() && !strings.EqualFold(u.Host, base.Host) {
		return "", "", "", false
	}
	if !u.IsAbs() && u.Host != "" {
		return "", "", "", false
	}
	p := u.EscapedPath()
	if !strings.HasPrefix(p, strings.TrimRight(base.Path, "/")+"/") {
		return "", "", "", false
	}
	var id string
	if m := reLinkSpacePage.FindStringSubmatch(p); m != nil {
		id = m[1]
	} else if v := u.Query().Get("pageId"); v != "" && strings.HasSuffix(p, "/pages/viewpage.action") {
		id = v
	} else if m := reTinyCode.FindStringSubmatch(p); m != nil {
		id, _ = tinyPageID(m[1])
	} else if m := reLinkDisplay.FindStringSubmatch(p); m != nil {
		if !strings.EqualFold(unescapeSegment(m[1]), e.snap.c.addr.SpaceKey) {
			return "", "", "", false
		}
		id = e.snap.byTitle[unescapeSegment(m[2])]
	}
	title, ok := e.snap.titles[id]
	if !ok {
		return "", "", "", false
	}
	return id, title, u.Fragment, true
}

func unescapeSegment(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		s = v
	}
	return strings.ReplaceAll(s, "+", " ")
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

func (e *pageEnv) add(a attachment, name string, role model.AttachmentRole, mediaType string) (string, bool) {
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
	if webPageInsteadOfFile(ref, resp.Header.Get("Content-Type")) {
		_ = resp.Body.Close()
		return nil, errors.New("Confluence answered with a web page instead of the file (a login or single sign-on page?): check that the token can download attachments")
	}
	return resp.Body, nil
}

// webPageInsteadOfFile reports an HTML answer to the download of a file
// that is not HTML itself, which login and SSO pages give with status 200.
func webPageInsteadOfFile(ref, contentType string) bool {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/html") {
		return false
	}
	name := ref
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	ext := strings.ToLower(path.Ext(name))
	return ext != ".html" && ext != ".htm"
}

// Browse lists the children of parentID; "" lists the configured page, or
// the top-level pages of the space.
func (c *Confluence) Browse(ctx context.Context, parentID string) ([]connectors.Node, error) {
	if err := c.resolve(ctx); err != nil {
		return nil, err
	}
	var pages []page
	switch {
	case parentID == "" && c.addr.PageID != "":
		p, err := c.api.page(ctx, c.addr.PageID, false)
		if err != nil {
			return nil, c.friendly(err, "the page "+c.addr.PageID)
		}
		if c.cloud {
			p.HasChildren = true
		}
		pages = []page{*p}
	case parentID == "":
		var err error
		if pages, err = c.api.children(ctx, ""); err != nil {
			return nil, c.friendly(err, "the space "+c.addr.SpaceKey)
		}
	default:
		var err error
		if pages, err = c.api.children(ctx, parentID); err != nil {
			return nil, err
		}
	}
	out := make([]connectors.Node, 0, len(pages))
	for _, p := range pages {
		out = append(out, connectors.Node{ID: p.ID, Title: p.Title, HasChildren: p.HasChildren})
	}
	return out, nil
}

package confluence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/httpc"
)

// v2 is the REST API v2 of Confluence Cloud (/wiki/api/v2).
type v2 struct {
	c *Confluence

	mu       sync.Mutex
	spaceID  string
	homeID   string
	keyCache map[string]string // space id -> key
}

var _ api = (*v2)(nil)

const v2Limit = 250

type v2Page struct {
	ID            string `json:"id"`
	Type          string `json:"type,omitempty"` // direct-children: page, folder, whiteboard, …
	Status        string `json:"status,omitempty"`
	Title         string `json:"title"`
	SpaceID       string `json:"spaceId,omitempty"`
	ParentID      string `json:"parentId,omitempty"`
	ParentType    string `json:"parentType,omitempty"`
	Position      *int   `json:"position,omitempty"`
	ChildPosition *int   `json:"childPosition,omitempty"`
	Version       *struct {
		Number int `json:"number"`
	} `json:"version,omitempty"`
	Body *struct {
		Storage *struct {
			Value string `json:"value"`
		} `json:"storage,omitempty"`
	} `json:"body,omitempty"`
}

func (p *v2Page) toPage() *page {
	out := &page{ID: p.ID, Title: p.Title, Folder: p.Type == "folder"}
	if p.Version != nil {
		out.Version = p.Version.Number
	}
	if p.Body != nil && p.Body.Storage != nil {
		out.Body = p.Body.Storage.Value
	}
	return out
}

func (p *v2Page) position() int {
	switch {
	case p.Position != nil:
		return *p.Position
	case p.ChildPosition != nil:
		return *p.ChildPosition
	}
	return 1 << 30
}

type v2Results[T any] struct {
	Results []T `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type v2Attachment struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	MediaType    string `json:"mediaType"`
	DownloadLink string `json:"downloadLink"`
	Version      *struct {
		Number int `json:"number"`
	} `json:"version,omitempty"`
}

// path builds an /api/v2 path.
func v2Path(p string, q url.Values) string {
	if len(q) == 0 {
		return "/api/v2" + p
	}
	return "/api/v2" + p + "?" + q.Encode()
}

// relative turns a "next" link, which starts with the site's context path
// (/wiki/api/v2/…), into a path for the client, whose base URL has it.
func (a *v2) relative(next string) string {
	if u, err := url.Parse(next); err == nil && u.IsAbs() {
		next = u.RequestURI()
	}
	if base, err := url.Parse(a.c.addr.BaseURL); err == nil && base.Path != "" {
		next = strings.TrimPrefix(next, strings.TrimRight(base.Path, "/"))
	}
	return next
}

// v2List fetches every page of a cursor-paged collection.
func v2List[T any](ctx context.Context, a *v2, path string) ([]T, error) {
	var out []T
	for path != "" {
		var res v2Results[T]
		if err := a.c.client.Get(ctx, path, &res); err != nil {
			return nil, err
		}
		out = append(out, res.Results...)
		path = ""
		if res.Links.Next != "" && len(res.Results) > 0 {
			path = a.relative(res.Links.Next)
		}
	}
	return out, nil
}

// spaceInfo returns the id and homepage of the space, looked up once.
func (a *v2) spaceInfo(ctx context.Context) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.spaceID != "" {
		return a.spaceID, a.homeID, nil
	}
	var res v2Results[struct {
		ID         string `json:"id"`
		Key        string `json:"key"`
		HomepageID string `json:"homepageId"`
	}]
	if err := a.c.client.Get(ctx, v2Path("/spaces", url.Values{"keys": {a.c.addr.SpaceKey}}), &res); err != nil {
		return "", "", err
	}
	if len(res.Results) == 0 {
		return "", "", &httpc.Error{Status: http.StatusNotFound, Message: "space not found"}
	}
	a.spaceID, a.homeID = res.Results[0].ID, res.Results[0].HomepageID
	return a.spaceID, a.homeID, nil
}

func (a *v2) spaceKey(ctx context.Context, spaceID string) (string, error) {
	a.mu.Lock()
	if k, ok := a.keyCache[spaceID]; ok {
		a.mu.Unlock()
		return k, nil
	}
	a.mu.Unlock()
	var sp struct {
		Key string `json:"key"`
	}
	if err := a.c.client.Get(ctx, v2Path("/spaces/"+url.PathEscape(spaceID), nil), &sp); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.keyCache == nil {
		a.keyCache = map[string]string{}
	}
	a.keyCache[spaceID] = sp.Key
	a.mu.Unlock()
	return sp.Key, nil
}

func (a *v2) page(ctx context.Context, id string, body bool) (*page, error) {
	q := url.Values{}
	if body {
		q.Set("body-format", "storage")
	}
	var p v2Page
	if err := a.c.client.Get(ctx, v2Path("/pages/"+url.PathEscape(id), q), &p); err != nil {
		return nil, err
	}
	out := p.toPage()
	if p.SpaceID != "" {
		key, err := a.spaceKey(ctx, p.SpaceID)
		if err != nil {
			return nil, err
		}
		out.SpaceKey = key
	}
	return out, nil
}

func (a *v2) pageByTitle(ctx context.Context, title string) (*page, error) {
	sid, _, err := a.spaceInfo(ctx)
	if err != nil {
		return nil, err
	}
	var res v2Results[v2Page]
	q := url.Values{"space-id": {sid}, "title": {title}, "status": {"current"}}
	if err := a.c.client.Get(ctx, v2Path("/pages", q), &res); err != nil {
		return nil, err
	}
	for _, p := range res.Results {
		if p.Title == title {
			return p.toPage(), nil
		}
	}
	return nil, nil
}

func (a *v2) space(ctx context.Context) error {
	_, _, err := a.spaceInfo(ctx)
	return err
}

// tree reads every current page of the space in one paged listing and
// builds the hierarchy from parent ids and positions. Folders that hold
// pages are fetched one by one.
func (a *v2) tree(ctx context.Context, rootID string) ([]fetched, error) {
	sid, _, err := a.spaceInfo(ctx)
	if err != nil {
		return nil, a.c.friendly(err, "the space "+a.c.addr.SpaceKey)
	}
	q := url.Values{"depth": {"all"}, "status": {"current"}, "body-format": {"storage"}, "limit": {fmt.Sprint(v2Limit)}}
	pages, err := v2List[v2Page](ctx, a, v2Path("/spaces/"+url.PathEscape(sid)+"/pages", q))
	if err != nil {
		return nil, a.c.friendly(err, "the pages of the space "+a.c.addr.SpaceKey)
	}
	byID := make(map[string]*v2Page, len(pages))
	for i := range pages {
		byID[pages[i].ID] = &pages[i]
	}
	// Folders holding pages: fetch them (and the folders they sit in).
	for i := 0; i < len(pages); i++ {
		p := pages[i]
		if p.ParentType != "folder" || p.ParentID == "" || byID[p.ParentID] != nil {
			continue
		}
		var f v2Page
		if err := a.c.client.Get(ctx, v2Path("/folders/"+url.PathEscape(p.ParentID), nil), &f); err != nil {
			return nil, fmt.Errorf("read the folder %s: %w", p.ParentID, err)
		}
		f.Type = "folder"
		pages = append(pages, f)
		byID[f.ID] = &pages[len(pages)-1]
		// pages may have been reallocated: rebuild the index.
		for j := range pages {
			byID[pages[j].ID] = &pages[j]
		}
	}
	kids := map[string][]*v2Page{}
	for i := range pages {
		p := &pages[i]
		if p.ParentID != "" && byID[p.ParentID] != nil {
			kids[p.ParentID] = append(kids[p.ParentID], p)
		} else {
			kids[""] = append(kids[""], p)
		}
	}
	for _, list := range kids {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].position() != list[j].position() {
				return list[i].position() < list[j].position()
			}
			return list[i].ID < list[j].ID
		})
	}
	var level []fetched
	if rootID != "" {
		root := byID[rootID]
		if root == nil {
			return nil, fmt.Errorf("the page %s is not a current page of the space %s; check the address", rootID, a.c.addr.SpaceKey)
		}
		level = []fetched{{page: *root.toPage()}}
	} else {
		for i, p := range kids[""] {
			level = append(level, fetched{page: *p.toPage(), order: i})
		}
	}
	var all []fetched
	seen := map[string]bool{}
	for len(level) > 0 {
		var next []fetched
		for _, f := range level {
			if seen[f.page.ID] {
				continue
			}
			seen[f.page.ID] = true
			all = append(all, f)
			for j, k := range kids[f.page.ID] {
				next = append(next, fetched{page: *k.toPage(), parent: f.page.ID, order: j})
			}
		}
		level = next
	}
	return all, nil
}

func (a *v2) children(ctx context.Context, parentID string) ([]page, error) {
	var items []v2Page
	var err error
	if parentID == "" {
		sid, _, err := a.spaceInfo(ctx)
		if err != nil {
			return nil, err
		}
		q := url.Values{"depth": {"root"}, "status": {"current"}, "limit": {fmt.Sprint(v2Limit)}}
		items, err = v2List[v2Page](ctx, a, v2Path("/spaces/"+url.PathEscape(sid)+"/pages", q))
		if err != nil {
			return nil, err
		}
	} else {
		q := url.Values{"limit": {fmt.Sprint(v2Limit)}}
		items, err = v2List[v2Page](ctx, a, v2Path("/pages/"+url.PathEscape(parentID)+"/direct-children", q))
		if httpc.StatusOf(err) == http.StatusNotFound { // a folder
			items, err = v2List[v2Page](ctx, a, v2Path("/folders/"+url.PathEscape(parentID)+"/direct-children", q))
		}
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].position() < items[j].position() })
	out := make([]page, 0, len(items))
	for _, it := range items {
		if it.Type != "" && it.Type != "page" && it.Type != "folder" {
			continue // whiteboards, databases, embeds: not mirrored
		}
		p := it.toPage()
		p.HasChildren = true
		out = append(out, *p)
	}
	return out, nil
}

func (a *v2) allPages(ctx context.Context) ([]page, error) {
	sid, _, err := a.spaceInfo(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{"depth": {"all"}, "status": {"current"}, "limit": {fmt.Sprint(v2Limit)}}
	items, err := v2List[v2Page](ctx, a, v2Path("/spaces/"+url.PathEscape(sid)+"/pages", q))
	if err != nil {
		return nil, err
	}
	out := make([]page, 0, len(items))
	for _, it := range items {
		out = append(out, *it.toPage())
	}
	return out, nil
}

func (a *v2) attachments(ctx context.Context, pageID string) ([]attachment, error) {
	q := url.Values{"limit": {fmt.Sprint(v2Limit)}}
	items, err := v2List[v2Attachment](ctx, a, v2Path("/pages/"+url.PathEscape(pageID)+"/attachments", q))
	if err != nil {
		return nil, err
	}
	out := make([]attachment, 0, len(items))
	for _, x := range items {
		at := attachment{ID: x.ID, Title: x.Title, MediaType: x.MediaType, Download: x.DownloadLink}
		if x.Version != nil {
			at.Version = x.Version.Number
		}
		out = append(out, at)
	}
	return out, nil
}

type v2Body struct {
	Representation string `json:"representation"`
	Value          string `json:"value"`
}

type v2Write struct {
	ID       string `json:"id,omitempty"`
	SpaceID  string `json:"spaceId,omitempty"`
	Status   string `json:"status"`
	Title    string `json:"title"`
	ParentID string `json:"parentId,omitempty"`
	Body     v2Body `json:"body"`
	Version  *v2Ver `json:"version,omitempty"`
}

type v2Ver struct {
	Number  int    `json:"number"`
	Message string `json:"message,omitempty"`
}

func (a *v2) create(ctx context.Context, title, parentID, body string) (*page, error) {
	sid, _, err := a.spaceInfo(ctx)
	if err != nil {
		return nil, err
	}
	in := v2Write{SpaceID: sid, Status: "current", Title: title, ParentID: parentID, Body: v2Body{"storage", body}}
	var res v2Page
	if err := a.c.client.JSON(ctx, http.MethodPost, v2Path("/pages", nil), in, &res); err != nil {
		return nil, err
	}
	return res.toPage(), nil
}

func (a *v2) update(ctx context.Context, id, title, parentID, body string, version int) (*page, error) {
	in := v2Write{ID: id, Status: "current", Title: title, ParentID: parentID, Body: v2Body{"storage", body},
		Version: &v2Ver{Number: version, Message: "Tolarix"}}
	var res v2Page
	if err := a.c.client.JSON(ctx, http.MethodPut, v2Path("/pages/"+url.PathEscape(id), nil), in, &res); err != nil {
		return nil, err
	}
	return res.toPage(), nil
}

func (a *v2) deletePage(ctx context.Context, id string) error {
	return a.c.client.JSON(ctx, http.MethodDelete, v2Path("/pages/"+url.PathEscape(id), nil), nil, nil)
}

func (a *v2) deleteAttachment(ctx context.Context, id string) error {
	return a.c.client.JSON(ctx, http.MethodDelete, v2Path("/attachments/"+url.PathEscape(id), nil), nil, nil)
}

// defaultParent is the space homepage: Cloud shows a space as the tree
// below it, and creates pages there when no parent is given.
func (a *v2) defaultParent(ctx context.Context) (string, string, error) {
	_, home, err := a.spaceInfo(ctx)
	if err != nil {
		return "", "", err
	}
	if home == "" {
		return "", "", errors.New("the space has no homepage; set the parent page in the mirror location")
	}
	p, err := a.page(ctx, home, false)
	if err != nil {
		return "", "", err
	}
	return p.ID, p.Title, nil
}

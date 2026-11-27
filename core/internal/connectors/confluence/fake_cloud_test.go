package confluence

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// newCloudFake is the fake in Cloud mode: REST v2 under /wiki/api/v2, Basic
// email:token auth, a space homepage, folders and cursor pagination whose
// "next" links carry the /wiki prefix like the real service.
func newCloudFake(t *testing.T) *fakeConfluence {
	f := &fakeConfluence{t: t, token: "tok", email: "me@example.com", space: "DOC", spaceID: "98304", nextID: 1000,
		pages: map[string]*fakePage{}, atts: map[string]*fakeAtt{}, limit: 2, ctx: "/wiki", cloud: true}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.home = f.addPage("Home", "", "<p>Welcome</p>")
	return f
}

func (f *fakeConfluence) addFolder(title, parent string) string {
	id := f.addPage(title, parent, "")
	f.mu.Lock()
	f.pages[id].Folder = true
	f.mu.Unlock()
	return id
}

// v2Kids lists pages and folders directly under parent, by position.
func (f *fakeConfluence) v2Kids(parent string) []*fakePage {
	var out []*fakePage
	for _, p := range f.pages {
		if p.Parent == parent {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

func (f *fakeConfluence) v2JSON(p *fakePage, body bool) map[string]any {
	m := map[string]any{"id": p.ID, "status": "current", "title": p.Title, "position": p.Order}
	if p.Folder {
		m["type"] = "folder"
	} else {
		m["spaceId"] = f.spaceID
		m["version"] = map[string]any{"number": p.Version}
		if body {
			m["body"] = map[string]any{"storage": map[string]any{"value": p.Body, "representation": "storage"}}
		}
	}
	if parent := f.pages[p.Parent]; parent != nil {
		m["parentId"] = parent.ID
		m["parentType"] = "page"
		if parent.Folder {
			m["parentType"] = "folder"
		}
	}
	return m
}

// v2Paged serves a cursor-paged list; the cursor is an offset here.
func (f *fakeConfluence) v2Paged(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	q := r.URL.Query()
	start, _ := strconv.Atoi(q.Get("cursor"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > f.limit {
		limit = f.limit
	}
	start = min(start, len(items))
	end := min(start+limit, len(items))
	links := map[string]any{"base": f.srv.URL + f.ctx}
	if end < len(items) {
		q.Set("cursor", strconv.Itoa(end))
		links["next"] = r.URL.Path + "?" + q.Encode() // starts with /wiki
	}
	if items == nil {
		items = []map[string]any{}
	}
	writeJSON(w, map[string]any{"results": items[start:end], "_links": links})
}

type v2PutBody struct {
	SpaceID  string `json:"spaceId"`
	Status   string `json:"status"`
	Title    string `json:"title"`
	ParentID string `json:"parentId"`
	Body     struct {
		Representation string `json:"representation"`
		Value          string `json:"value"`
	} `json:"body"`
	Version struct {
		Number int `json:"number"`
	} `json:"version"`
}

func (f *fakeConfluence) titleTaken(title string, except *fakePage) bool {
	for _, p := range f.pages {
		if p != except && !p.Folder && strings.EqualFold(p.Title, title) {
			return true
		}
	}
	return false
}

func (f *fakeConfluence) serveV2(w http.ResponseWriter, r *http.Request, path string) {
	q := r.URL.Query()
	seg := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v2"), "/"), "/")
	body := q.Get("body-format") == "storage"
	page := func(id string) *fakePage {
		if p := f.pages[id]; p != nil && !p.Folder {
			return p
		}
		return nil
	}
	folder := func(id string) *fakePage {
		if p := f.pages[id]; p != nil && p.Folder {
			return p
		}
		return nil
	}
	switch {
	case len(seg) == 1 && seg[0] == "spaces":
		var items []map[string]any
		if q.Get("keys") == f.space {
			items = append(items, map[string]any{"id": f.spaceID, "key": f.space, "homepageId": f.home})
		}
		f.v2Paged(w, r, items)

	case len(seg) == 2 && seg[0] == "spaces":
		if seg[1] != f.spaceID {
			fail(w, http.StatusNotFound, "space not found")
			return
		}
		writeJSON(w, map[string]any{"id": f.spaceID, "key": f.space, "homepageId": f.home})

	case len(seg) == 3 && seg[0] == "spaces" && seg[2] == "pages":
		var items []map[string]any
		ids := make([]string, 0, len(f.pages))
		for id, p := range f.pages {
			if p.Folder || (q.Get("depth") == "root" && p.Parent != "") {
				continue
			}
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			items = append(items, f.v2JSON(f.pages[id], body))
		}
		f.v2Paged(w, r, items)

	case len(seg) == 1 && seg[0] == "pages" && r.Method == http.MethodGet:
		var items []map[string]any
		for _, p := range f.pages {
			if !p.Folder && q.Get("space-id") == f.spaceID && p.Title == q.Get("title") {
				items = append(items, f.v2JSON(p, false))
			}
		}
		f.v2Paged(w, r, items)

	case len(seg) == 1 && seg[0] == "pages" && r.Method == http.MethodPost:
		var b v2PutBody
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.SpaceID != f.spaceID || b.Body.Representation != "storage" {
			fail(w, http.StatusBadRequest, "bad page")
			return
		}
		if f.titleTaken(b.Title, nil) {
			fail(w, http.StatusBadRequest, "A page with this title already exists")
			return
		}
		parent := b.ParentID
		if parent == "" {
			parent = f.home // Cloud puts pages under the homepage
		}
		f.nextID++
		id := strconv.Itoa(f.nextID)
		f.pages[id] = &fakePage{ID: id, Title: b.Title, Parent: parent, Body: normalise(b.Body.Value), Version: 1, Order: f.nextID}
		writeJSON(w, f.v2JSON(f.pages[id], false))

	case len(seg) == 2 && seg[0] == "pages":
		p := page(seg[1])
		if p == nil {
			fail(w, http.StatusNotFound, "page not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			m := f.v2JSON(p, body)
			m["authorId"], m["createdAt"] = "557058:ada", "2024-01-02T09:00:00.000Z"
			m["version"] = map[string]any{"number": p.Version, "authorId": "557058:alan", "createdAt": "2024-03-04T11:00:00.000Z"}
			m["_links"] = map[string]any{"webui": "/spaces/" + f.space + "/pages/" + p.ID}
			if r.URL.Query().Get("include-labels") == "true" {
				m["labels"] = map[string]any{"results": fakeLabels(p)}
			}
			writeJSON(w, m)
		case http.MethodPut:
			var b v2PutBody
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b.Version.Number != p.Version+1 {
				fail(w, http.StatusConflict, fmt.Sprintf("Version must be incremented on update. Current version is: %d", p.Version))
				return
			}
			if f.titleTaken(b.Title, p) {
				fail(w, http.StatusBadRequest, "A page with this title already exists")
				return
			}
			p.Title, p.Body, p.Version = b.Title, normalise(b.Body.Value), b.Version.Number
			if b.ParentID != "" {
				p.Parent = b.ParentID
			}
			writeJSON(w, f.v2JSON(p, false))
		case http.MethodDelete:
			for _, c := range f.v2Kids(p.ID) {
				c.Parent = p.Parent
			}
			delete(f.pages, p.ID)
			w.WriteHeader(http.StatusNoContent)
		}

	case len(seg) == 3 && seg[0] == "pages" && seg[2] == "direct-children":
		if page(seg[1]) == nil {
			fail(w, http.StatusNotFound, "page not found")
			return
		}
		f.v2Children(w, r, seg[1])

	case len(seg) == 2 && seg[0] == "folders":
		fo := folder(seg[1])
		if fo == nil {
			fail(w, http.StatusNotFound, "folder not found")
			return
		}
		writeJSON(w, f.v2JSON(fo, false))

	case len(seg) == 3 && seg[0] == "folders" && seg[2] == "direct-children":
		if folder(seg[1]) == nil {
			fail(w, http.StatusNotFound, "folder not found")
			return
		}
		f.v2Children(w, r, seg[1])

	case len(seg) == 3 && seg[0] == "pages" && seg[2] == "attachments":
		var items []map[string]any
		ids := []string{}
		for id, a := range f.atts {
			if a.Page == seg[1] {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			a := f.atts[id]
			items = append(items, map[string]any{"id": a.ID, "title": a.Name, "mediaType": "application/octet-stream",
				"version":      map[string]any{"number": a.Version},
				"downloadLink": "/download/attachments/" + a.Page + "/" + url.PathEscape(a.Name) + "?version=" + strconv.Itoa(a.Version) + "&api=v2"})
		}
		f.v2Paged(w, r, items)

	case len(seg) == 2 && seg[0] == "attachments" && r.Method == http.MethodDelete:
		if f.atts[seg[1]] == nil {
			fail(w, http.StatusNotFound, "attachment not found")
			return
		}
		delete(f.atts, seg[1])
		w.WriteHeader(http.StatusNoContent)

	default:
		f.t.Errorf("fake confluence cloud: unexpected %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

func (f *fakeConfluence) v2Children(w http.ResponseWriter, r *http.Request, parent string) {
	var items []map[string]any
	for _, c := range f.v2Kids(parent) {
		typ := "page"
		if c.Folder {
			typ = "folder"
		}
		items = append(items, map[string]any{"id": c.ID, "status": "current", "title": c.Title, "type": typ,
			"spaceId": f.spaceID, "childPosition": c.Order})
	}
	f.v2Paged(w, r, items)
}

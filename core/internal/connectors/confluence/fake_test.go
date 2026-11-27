package confluence

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const ctxPath = "/confluence"

// fakeConfluence is an in-memory Confluence Data Center REST API (the
// subset Tolarix uses), served under a context path with small pages so
// pagination is exercised.
type fakeConfluence struct {
	t      *testing.T
	mu     sync.Mutex
	srv    *httptest.Server
	token  string
	space  string
	nextID int
	pages  map[string]*fakePage
	atts   map[string]*fakeAtt
	calls  []string // "METHOD path" of writes
	limit  int      // max page size the server honours
	ctx    string   // context path: /confluence (Data Center) or /wiki (Cloud)

	// Cloud mode serves REST v2 (fake_cloud_test.go) with Basic auth.
	cloud   bool
	email   string
	spaceID string
	home    string // homepage id
}

type fakePage struct {
	ID, Title, Parent, Body string
	Version                 int
	Order                   int
	Folder                  bool // Cloud folder
	Labels                  []string
}

// Users of the fakes: Data Center by user key or username, Cloud by account
// id. History names the first as the author and the second as the editor.
var fakeUsers = map[string]string{"ukey-ada": "Ada Lovelace", "ada": "Ada Lovelace", "557058:ada": "Ada Lovelace",
	"ukey-alan": "Alan Turing", "alan": "Alan Turing", "557058:alan": "Alan Turing"}

func fakeLabels(p *fakePage) []map[string]any {
	out := []map[string]any{}
	for _, l := range p.Labels {
		out = append(out, map[string]any{"name": l, "prefix": "global"})
	}
	return append(out, map[string]any{"name": "private", "prefix": "my"})
}

func (f *fakeConfluence) serveUser(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for _, k := range []string{q.Get("accountId"), q.Get("key"), q.Get("username")} {
		if n, ok := fakeUsers[k]; ok && k != "" {
			writeJSON(w, map[string]any{"displayName": n})
			return
		}
	}
	fail(w, http.StatusNotFound, "user not found")
}

type fakeAtt struct {
	ID, Page, Name string
	Data           []byte
	Version        int
}

func newFake(t *testing.T) *fakeConfluence {
	f := &fakeConfluence{t: t, token: "tok", space: "DOC", nextID: 1000,
		pages: map[string]*fakePage{}, atts: map[string]*fakeAtt{}, limit: 2, ctx: ctxPath}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeConfluence) url(p string) string { return f.srv.URL + f.ctx + p }

func (f *fakeConfluence) addPage(title, parent, body string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := strconv.Itoa(f.nextID)
	f.pages[id] = &fakePage{ID: id, Title: title, Parent: parent, Body: body, Version: 1, Order: f.nextID}
	return id
}

func (f *fakeConfluence) addAtt(page, name string, data []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := "att" + strconv.Itoa(f.nextID)
	f.atts[id] = &fakeAtt{ID: id, Page: page, Name: name, Data: data, Version: 1}
	return id
}

func (f *fakeConfluence) byTitle(title string) *fakePage {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.pages {
		if p.Title == title {
			return p
		}
	}
	return nil
}

func (f *fakeConfluence) attsOf(pageID string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for _, a := range f.atts {
		if a.Page == pageID {
			out[a.Name] = string(a.Data)
		}
	}
	return out
}

func (f *fakeConfluence) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

func (f *fakeConfluence) pageJSON(p *fakePage, expand string) map[string]any {
	m := map[string]any{"id": p.ID, "type": "page", "status": "current", "title": p.Title,
		"space": map[string]any{"key": f.space}}
	if strings.Contains(expand, "version") {
		m["version"] = map[string]any{"number": p.Version}
	}
	if strings.Contains(expand, "body.storage") {
		m["body"] = map[string]any{"storage": map[string]any{"value": p.Body, "representation": "storage"}}
	}
	if strings.Contains(expand, "history") {
		// Data Center names the author; the editor is looked up by key.
		m["history"] = map[string]any{"createdDate": "2024-01-02T10:00:00.000+01:00",
			"createdBy": map[string]any{"userKey": "ukey-ada", "username": "ada", "displayName": "Ada Lovelace"}}
		m["version"] = map[string]any{"number": p.Version, "when": "2024-03-04T12:00:00.000+01:00",
			"by": map[string]any{"userKey": "ukey-alan"}}
		m["_links"] = map[string]any{"webui": "/pages/viewpage.action?pageId=" + p.ID}
	}
	if strings.Contains(expand, "metadata.labels") {
		m["metadata"] = map[string]any{"labels": map[string]any{"results": fakeLabels(p)}}
	}
	if strings.Contains(expand, "children.page") {
		m["children"] = map[string]any{"page": map[string]any{"size": len(f.children(p.ID))}}
	}
	return m
}

func (f *fakeConfluence) children(parent string) []*fakePage {
	var out []*fakePage
	for _, p := range f.pages {
		if p.Parent == parent && !p.Folder {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

func (f *fakeConfluence) paged(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > f.limit {
		limit = f.limit
	}
	end := min(start+limit, len(items))
	if start > len(items) {
		start = end
	}
	links := map[string]any{"base": f.srv.URL + f.ctx, "context": f.ctx}
	if end < len(items) {
		q := r.URL.Query()
		q.Set("start", strconv.Itoa(end))
		links["next"] = strings.TrimPrefix(r.URL.Path, f.ctx) + "?" + q.Encode()
	}
	writeJSON(w, map[string]any{"results": items[start:end], "size": end - start, "start": start, "limit": limit, "_links": links})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"statusCode": status, "message": msg})
}

type putBody struct {
	Title     string              `json:"title"`
	Type      string              `json:"type"`
	Space     map[string]string   `json:"space"`
	Ancestors []map[string]string `json:"ancestors"`
	Version   struct {
		Number int `json:"number"`
	} `json:"version"`
	Body struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
}

func (f *fakeConfluence) serve(w http.ResponseWriter, r *http.Request) {
	if f.cloud {
		if u, p, ok := r.BasicAuth(); !ok || u != f.email || p != f.token {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	} else if r.Header.Get("Authorization") != "Bearer "+f.token {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, f.ctx)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	expand := q.Get("expand")
	seg := strings.Split(strings.Trim(path, "/"), "/")
	if r.Method != http.MethodGet {
		f.calls = append(f.calls, r.Method+" "+path)
	}
	if f.cloud {
		if strings.HasPrefix(path, "/api/v2/") {
			f.serveV2(w, r, path)
			return
		}
		// Cloud keeps only attachment upload and download on v1.
		upload := r.Method == http.MethodPost && strings.Contains(path, "/child/attachment")
		if !upload && !strings.HasPrefix(path, "/download/attachments/") && path != "/rest/api/user" {
			f.t.Errorf("fake confluence cloud: v1 call %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
	}

	switch {
	case path == "/rest/api/user":
		f.serveUser(w, r)

	case strings.HasPrefix(path, "/download/attachments/"):
		name, _ := url.PathUnescape(seg[len(seg)-1])
		for _, a := range f.atts {
			if a.Page == seg[2] && a.Name == name {
				_, _ = w.Write(a.Data)
				return
			}
		}
		http.NotFound(w, r)

	case len(seg) == 4 && seg[2] == "space" && r.Method == http.MethodGet:
		if seg[3] != f.space {
			fail(w, http.StatusNotFound, "No space with key : "+seg[3])
			return
		}
		writeJSON(w, map[string]any{"key": f.space, "name": "Docs"})

	case len(seg) == 6 && seg[2] == "space" && seg[4] == "content" && seg[5] == "page":
		var items []map[string]any
		for _, p := range f.children("") {
			items = append(items, f.pageJSON(p, expand))
		}
		f.paged(w, r, items)

	case path == "/rest/api/content" && r.Method == http.MethodGet:
		var items []map[string]any
		ids := make([]string, 0, len(f.pages))
		for id := range f.pages {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			p := f.pages[id]
			if t := q.Get("title"); t != "" && t != p.Title {
				continue
			}
			items = append(items, f.pageJSON(p, expand))
		}
		f.paged(w, r, items)

	case path == "/rest/api/content" && r.Method == http.MethodPost:
		var b putBody
		_ = json.NewDecoder(r.Body).Decode(&b)
		for _, p := range f.pages {
			if strings.EqualFold(p.Title, b.Title) {
				fail(w, http.StatusBadRequest, "A page with this title already exists: A page already exists with the title "+b.Title+" in this space")
				return
			}
		}
		parent := ""
		if len(b.Ancestors) > 0 {
			parent = b.Ancestors[0]["id"]
		}
		f.nextID++
		id := strconv.Itoa(f.nextID)
		f.pages[id] = &fakePage{ID: id, Title: b.Title, Parent: parent, Body: normalise(b.Body.Storage.Value), Version: 1, Order: f.nextID}
		writeJSON(w, f.pageJSON(f.pages[id], "version"))

	case len(seg) == 4 && seg[2] == "content":
		p := f.pages[seg[3]]
		if p == nil {
			fail(w, http.StatusNotFound, "No content found with id: "+seg[3])
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, f.pageJSON(p, expand))
		case http.MethodPut:
			var b putBody
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b.Version.Number != p.Version+1 {
				fail(w, http.StatusConflict, fmt.Sprintf("Version must be incremented on update. Current version is: %d", p.Version))
				return
			}
			for _, o := range f.pages {
				if o != p && strings.EqualFold(o.Title, b.Title) {
					fail(w, http.StatusBadRequest, "A page with this title already exists")
					return
				}
			}
			p.Title, p.Body, p.Version = b.Title, normalise(b.Body.Storage.Value), b.Version.Number
			if len(b.Ancestors) > 0 {
				p.Parent = b.Ancestors[0]["id"]
			}
			writeJSON(w, f.pageJSON(p, "version"))
		case http.MethodDelete:
			for _, c := range f.children(p.ID) {
				c.Parent = p.Parent // Confluence moves children up
			}
			delete(f.pages, p.ID)
			w.WriteHeader(http.StatusNoContent)
		}

	case len(seg) == 6 && seg[2] == "content" && seg[4] == "child" && seg[5] == "page":
		var items []map[string]any
		for _, c := range f.children(seg[3]) {
			items = append(items, f.pageJSON(c, expand))
		}
		f.paged(w, r, items)

	case len(seg) >= 6 && seg[2] == "content" && seg[4] == "child" && seg[5] == "attachment":
		pageID := seg[3]
		if r.Method == http.MethodGet {
			var items []map[string]any
			ids := make([]string, 0)
			for id, a := range f.atts {
				if a.Page == pageID {
					ids = append(ids, id)
				}
			}
			sort.Strings(ids)
			for _, id := range ids {
				a := f.atts[id]
				items = append(items, map[string]any{"id": a.ID, "type": "attachment", "title": a.Name,
					"version":    map[string]any{"number": a.Version},
					"extensions": map[string]any{"mediaType": "application/octet-stream", "fileSize": len(a.Data)},
					"_links":     map[string]any{"download": "/download/attachments/" + pageID + "/" + url.PathEscape(a.Name) + "?version=" + strconv.Itoa(a.Version) + "&api=v2"}})
			}
			f.paged(w, r, items)
			return
		}
		if r.Header.Get("X-Atlassian-Token") != "no-check" {
			fail(w, http.StatusForbidden, "XSRF check failed")
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		data, _ := io.ReadAll(file)
		var a *fakeAtt
		if len(seg) == 8 && seg[7] == "data" {
			a = f.atts[seg[6]]
			if a == nil {
				fail(w, http.StatusNotFound, "no attachment")
				return
			}
			a.Data, a.Version = data, a.Version+1
		} else {
			for _, x := range f.atts {
				if x.Page == pageID && x.Name == hdr.Filename {
					fail(w, http.StatusBadRequest, "Cannot add a new attachment with same file name as an existing attachment")
					return
				}
			}
			f.nextID++
			a = &fakeAtt{ID: "att" + strconv.Itoa(f.nextID), Page: pageID, Name: hdr.Filename, Data: data, Version: 1}
			f.atts[a.ID] = a
		}
		writeJSON(w, map[string]any{"results": []map[string]any{{"id": a.ID, "title": a.Name}}})

	default:
		f.t.Errorf("fake confluence: unexpected %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}
}

// normalise imitates Confluence rewriting what it stores.
func normalise(s string) string { return strings.ReplaceAll(s, "<br/>", "<br />") }

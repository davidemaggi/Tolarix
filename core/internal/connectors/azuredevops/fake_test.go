package azuredevops

import (
	"crypto/sha1" //nolint:gosec // fake commit ids
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
)

const (
	fakeOrg     = "org"
	fakeProject = "Docs Project"
	fakeRepo    = "wiki-repo"
	fakeRepoID  = "11111111-2222-3333-4444-555555555555"
)

// fakeADO is an in-memory Azure DevOps Git REST API (the subset Tolarix
// uses): wikis, repositories, refs, items, blobs and pushes.
type fakeADO struct {
	t             *testing.T
	mu            sync.Mutex
	srv           *httptest.Server
	token         string
	defaultBranch string
	branches      map[string]string            // branch -> commit
	commits       map[string]map[string]string // commit -> path -> blob id
	blobs         map[string][]byte
	wikis         map[string]wikiInfo
	pushes        [][]pushChange
	nextCommit    int
	rejectPush    string // error message returned by the next push
	beforePush    func() // runs (unlocked) before a push is applied
}

func newFake(t *testing.T) *fakeADO {
	f := &fakeADO{t: t, token: "pat", defaultBranch: "main", branches: map[string]string{},
		commits: map[string]map[string]string{}, blobs: map[string][]byte{}, wikis: map[string]wikiInfo{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeADO) url(rest string) string {
	return f.srv.URL + "/" + fakeOrg + "/" + url.PathEscape(fakeProject) + rest
}

// commitFiles creates a commit on branch with files changed (content "" deletes).
func (f *fakeADO) commitFiles(branch string, files map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tree := map[string]string{}
	for p, id := range f.commits[f.branches[branch]] {
		tree[p] = id
	}
	for p, c := range files {
		if c == "" {
			delete(tree, p)
			continue
		}
		id := blobSHA([]byte(c))
		f.blobs[id] = []byte(c)
		tree[p] = id
	}
	return f.newCommitLocked(branch, tree)
}

func (f *fakeADO) newCommitLocked(branch string, tree map[string]string) string {
	f.nextCommit++
	h := sha1.Sum([]byte(fmt.Sprint("commit", f.nextCommit))) //nolint:gosec // fake ids
	id := hex.EncodeToString(h[:])
	f.commits[id] = tree
	f.branches[branch] = id
	return id
}

// file returns a file's content at the branch head ("" when missing).
func (f *fakeADO) file(branch, p string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.blobs[f.commits[f.branches[branch]][p]])
}

// paths lists the files at the branch head under prefix.
func (f *fakeADO) paths(branch, prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for p := range f.commits[f.branches[branch]] {
		if strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (f *fakeADO) takePushes() [][]pushChange {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.pushes
	f.pushes = nil
	return out
}

func adoError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": msg, "typeKey": "GitException"})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeADO) serve(w http.ResponseWriter, r *http.Request) {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+f.token))
	if r.Header.Get("Authorization") != want {
		// Azure DevOps answers a bad PAT with a sign-in page.
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNonAuthoritativeInfo)
		_, _ = w.Write([]byte("<html>Sign in</html>"))
		return
	}
	if r.URL.Query().Get("api-version") != apiVersion {
		adoError(w, http.StatusBadRequest, "missing api-version")
		return
	}
	prefix := "/" + fakeOrg + "/" + fakeProject + "/_apis/"
	rest, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		adoError(w, http.StatusNotFound, "TF200016: The following project does not exist")
		return
	}
	seg := strings.Split(rest, "/")
	q := r.URL.Query()

	if len(seg) == 3 && seg[0] == "wiki" && seg[1] == "wikis" {
		f.mu.Lock()
		wi, ok := f.wikis[seg[2]]
		f.mu.Unlock()
		if !ok {
			adoError(w, http.StatusNotFound, "The wiki "+seg[2]+" does not exist")
			return
		}
		writeJSON(w, wi)
		return
	}
	if len(seg) < 3 || seg[0] != "git" || seg[1] != "repositories" {
		f.t.Errorf("fake ado: unexpected %s %s", r.Method, r.URL)
		adoError(w, http.StatusNotFound, "not found")
		return
	}
	if seg[2] != fakeRepo && seg[2] != fakeRepoID {
		adoError(w, http.StatusNotFound, "TF401019: The Git repository with name or identifier "+seg[2]+" does not exist or you do not have permissions")
		return
	}
	if len(seg) == 3 {
		f.mu.Lock()
		info := repoInfo{ID: fakeRepoID, Name: fakeRepo}
		if _, ok := f.branches[f.defaultBranch]; ok {
			info.DefaultBranch = "refs/heads/" + f.defaultBranch
		}
		f.mu.Unlock()
		writeJSON(w, info)
		return
	}
	if seg[2] != fakeRepoID {
		f.t.Errorf("fake ado: use the repository id, got %s", r.URL.Path)
	}
	switch {
	case seg[3] == "refs" && r.Method == http.MethodGet:
		name := strings.TrimPrefix(q.Get("filter"), "heads/")
		f.mu.Lock()
		var refs []gitRef
		for b, c := range f.branches {
			if strings.HasPrefix(b, name) {
				refs = append(refs, gitRef{Name: "refs/heads/" + b, ObjectID: c})
			}
		}
		f.mu.Unlock()
		writeJSON(w, list[gitRef]{Count: len(refs), Value: refs})
	case seg[3] == "items":
		f.items(w, q)
	case seg[3] == "blobs" && len(seg) == 5:
		f.mu.Lock()
		b, ok := f.blobs[seg[4]]
		f.mu.Unlock()
		if !ok {
			adoError(w, http.StatusNotFound, "blob not found")
			return
		}
		if r.Header.Get("Accept") != "application/octet-stream" || q.Get("$format") != "octetstream" {
			f.t.Errorf("blob request without octet-stream: %v", r.URL)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(b)
	case seg[3] == "pushes" && r.Method == http.MethodPost:
		f.push(w, r)
	default:
		f.t.Errorf("fake ado: unexpected %s %s", r.Method, r.URL)
		adoError(w, http.StatusNotFound, "not found")
	}
}

func (f *fakeADO) items(w http.ResponseWriter, q url.Values) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if q.Get("versionDescriptor.versionType") != "commit" {
		f.t.Errorf("items must be read at a commit: %v", q)
	}
	tree, ok := f.commits[q.Get("versionDescriptor.version")]
	if !ok {
		adoError(w, http.StatusNotFound, "TF401175: The version descriptor could not be resolved")
		return
	}
	scope := strings.TrimSuffix(q.Get("scopePath"), "/")
	full := q.Get("recursionLevel") == "Full"
	seen := map[string]bool{}
	var out []gitItem
	add := func(it gitItem) {
		if !seen[it.Path] {
			seen[it.Path] = true
			out = append(out, it)
		}
	}
	for p, id := range tree {
		if !strings.HasPrefix(p, scope+"/") {
			continue
		}
		rel := strings.TrimPrefix(p, scope+"/")
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts); i++ {
			if !full && i > 1 {
				break
			}
			add(gitItem{Path: scope + "/" + strings.Join(parts[:i], "/"), GitObjectType: "tree", IsFolder: true, ObjectID: "tree"})
		}
		if full || len(parts) == 1 {
			add(gitItem{Path: p, GitObjectType: "blob", ObjectID: id})
		}
	}
	if len(out) == 0 && scope != "" {
		adoError(w, http.StatusNotFound, "TF401174: The item '"+scope+"' could not be found in the repository")
		return
	}
	root := scope
	if root == "" {
		root = "/"
	}
	out = append([]gitItem{{Path: root, GitObjectType: "tree", IsFolder: true}}, out...)
	sort.Slice(out[1:], func(i, j int) bool { return out[i+1].Path < out[j+1].Path })
	writeJSON(w, list[gitItem]{Count: len(out), Value: out})
}

func (f *fakeADO) push(w http.ResponseWriter, r *http.Request) {
	if f.beforePush != nil {
		hook := f.beforePush
		f.beforePush = nil
		hook()
	}
	var body struct {
		RefUpdates []struct {
			Name        string `json:"name"`
			OldObjectID string `json:"oldObjectId"`
		} `json:"refUpdates"`
		Commits []struct {
			Comment string       `json:"comment"`
			Changes []pushChange `json:"changes"`
		} `json:"commits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.RefUpdates) != 1 || len(body.Commits) != 1 {
		adoError(w, http.StatusBadRequest, fmt.Sprintf("bad push body: %v", err))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rejectPush != "" {
		msg := f.rejectPush
		f.rejectPush = ""
		adoError(w, http.StatusForbidden, msg)
		return
	}
	branch := strings.TrimPrefix(body.RefUpdates[0].Name, "refs/heads/")
	head, exists := f.branches[branch]
	if (!exists && body.RefUpdates[0].OldObjectID != zeroID) || (exists && body.RefUpdates[0].OldObjectID != head) {
		adoError(w, http.StatusConflict, "TF401028: The reference 'refs/heads/"+branch+"' has already been updated by another client, so you cannot update it. Please try again.")
		return
	}
	tree := map[string]string{}
	for p, id := range f.commits[head] {
		tree[p] = id
	}
	for _, c := range body.Commits[0].Changes {
		p := c.Item.Path
		_, has := tree[p]
		fail := func(msg string) { adoError(w, http.StatusBadRequest, msg+": "+p) }
		switch c.ChangeType {
		case "add", "edit":
			if (c.ChangeType == "add") == has {
				fail("TF401022: wrong change type for")
				return
			}
			if c.NewContent == nil || c.NewContent.ContentType != "base64encoded" {
				fail("missing content")
				return
			}
			b, err := base64.StdEncoding.DecodeString(c.NewContent.Content)
			if err != nil {
				fail("bad base64")
				return
			}
			id := blobSHA(b)
			f.blobs[id] = b
			tree[p] = id
		case "delete":
			if !has {
				fail("TF401174: cannot delete missing")
				return
			}
			delete(tree, p)
		case "rename":
			src, ok := tree[c.SourceServerItem]
			if !ok || has {
				fail("bad rename")
				return
			}
			delete(tree, c.SourceServerItem)
			tree[p] = src
		default:
			fail("unknown change " + c.ChangeType)
			return
		}
	}
	f.pushes = append(f.pushes, body.Commits[0].Changes)
	id := f.newCommitLocked(branch, tree)
	writeJSON(w, map[string]any{"pushId": len(f.pushes),
		"refUpdates": []map[string]string{{"name": "refs/heads/" + branch, "newObjectId": id}}})
}

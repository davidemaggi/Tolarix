// Package azuredevops holds the Azure DevOps wiki connectors:
//
//   - Code wiki: a wiki published from a folder of a Git repository;
//   - Project wiki: the wiki every project can create, stored in a hidden
//     "<project>.wiki" repository on the branch "wikiMaster".
//
// Both read and write the repository through the Git REST API (no clone),
// and a run's writes become a single push.
//
// See docs/dev/connectors.md for naming, ordering and link rules.
package azuredevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/httpc"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

// Registry keys.
const (
	Kind        connectors.Kind = "azure-devops-code"
	ProjectKind connectors.Kind = "azure-devops-project"
)

const apiVersion = "7.1"

// projectBranch is the branch a project wiki lives on.
const projectBranch = "wikiMaster"

func init() {
	for _, k := range []struct {
		kind connectors.Kind
		name string
	}{{Kind, "Azure DevOps Code wiki"}, {ProjectKind, "Azure DevOps Project wiki"}} {
		kind := k.kind
		connectors.Register(connectors.Spec{
			Kind:        kind,
			DisplayName: k.name,
			CanRead:     true,
			CanWrite:    true,
			Fields: []connectors.Field{
				{Name: "url", Type: "url", Required: true},
				{Name: "token", Type: "secret", Required: true, Secret: true},
			},
			LocationFields: []connectors.Field{{Name: "folder", Type: "string"}},
			New: func(raw json.RawMessage, secret secrets.String) (connectors.Connector, error) {
				var c Config
				if len(raw) > 0 {
					if err := json.Unmarshal(raw, &c); err != nil {
						return nil, fmt.Errorf("invalid Azure DevOps connection settings: %w", err)
					}
				}
				if kind == ProjectKind {
					return NewProject(c, secret)
				}
				return New(c, secret)
			},
		})
	}
}

// Config is the stored connection configuration. The personal access token
// is the connection secret.
type Config struct {
	// URL is the address of the wiki (…/_wiki/wikis/<name>) or, for a code
	// wiki, of the repository folder it is published from
	// (…/_git/<repo>?path=/docs).
	URL string `json:"url"`
}

// Location is a flow target inside the wiki.
type Location struct {
	// Folder is relative to the wiki root ("" = the wiki root).
	Folder string `json:"folder,omitempty"`
}

// Address is what an Azure DevOps URL points at.
type Address struct {
	BaseURL string // organisation (or collection) URL, e.g. https://dev.azure.com/org
	Project string
	Repo    string // repository name or id
	Wiki    string // wiki name, when the URL was a wiki address
	Branch  string // without refs/heads/; "" = the default branch
	Folder  string // the wiki root inside the repository, without slashes ("" = repo root)
}

// Repo is a configured connection; it is a Source and a Target.
type Repo struct {
	addr    Address
	client  *httpc.Client
	project bool // a project wiki connection

	mu       sync.Mutex
	resolved bool
	repoID   string
}

var (
	_ connectors.Source = (*Repo)(nil)
	_ connectors.Target = (*Repo)(nil)
)

// New builds a code wiki connection.
func New(c Config, token secrets.String) (*Repo, error) {
	addr, err := ParseURL(c.URL)
	if err != nil {
		return nil, err
	}
	return newRepo(addr, token, false)
}

// NewProject builds a project wiki connection.
func NewProject(c Config, token secrets.String) (*Repo, error) {
	addr, err := ParseProjectURL(c.URL)
	if err != nil {
		return nil, err
	}
	return newRepo(addr, token, true)
}

func newRepo(addr Address, token secrets.String, project bool) (*Repo, error) {
	if !token.IsSet() {
		return nil, errors.New("the personal access token is missing: create one in Azure DevOps (User settings → Personal access tokens) with the Code (Read & write) scope and paste it here")
	}
	auth := func(r *http.Request) {
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+token.Reveal())))
	}
	return &Repo{addr: addr, client: httpc.New(addr.BaseURL, auth), project: project}, nil
}

// Client exposes the HTTP client (tests shorten its retries).
func (r *Repo) Client() *httpc.Client { return r.client }

// ParseURL reads the address of a code wiki or a repository as copied from
// the browser.
func ParseURL(raw string) (Address, error) { return parseURL(raw, false) }

// ParseProjectURL reads the address of a project wiki: …/_wiki/wikis/<name>,
// or just …/<project>/_wiki (the project's wiki is looked up).
func ParseProjectURL(raw string) (Address, error) {
	a, err := parseURL(raw, true)
	if err != nil {
		return Address{}, err
	}
	if a.Repo != "" {
		return Address{}, fmt.Errorf("%q is a repository address; for a project wiki, open the wiki in Azure DevOps (Overview → Wiki) and copy that address", strings.TrimSpace(raw))
	}
	return a, nil
}

func parseURL(raw string, bareWiki bool) (Address, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Address{}, errors.New("the address is missing: open the wiki in Azure DevOps and copy the address from the browser")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Address{}, fmt.Errorf("%q is not a web address; copy it from the browser (it starts with https://)", raw)
	}
	var segs []string
	for _, s := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
		if v, err := url.PathUnescape(s); err == nil {
			s = v
		}
		if s != "" {
			segs = append(segs, s)
		}
	}
	marker := -1
	for i, s := range segs {
		if s == "_git" || s == "_wiki" {
			marker = i
			break
		}
	}
	notWiki := fmt.Errorf("%q does not look like the address of an Azure DevOps wiki or repository. Open the wiki (or the repository folder it is published from) and copy the address from the browser", raw)
	bare := bareWiki && marker >= 0 && marker == len(segs)-1 && segs[marker] == "_wiki"
	if marker < 0 || (marker+1 >= len(segs) && !bare) {
		return Address{}, notWiki
	}
	// How many leading segments name the organisation or collection.
	host := strings.ToLower(u.Hostname())
	orgSegs := marker - 1 // Server: everything before the project
	switch {
	case host == "dev.azure.com":
		orgSegs = 1
	case strings.HasSuffix(host, ".visualstudio.com"):
		orgSegs = 0
		if len(segs) > 0 && strings.EqualFold(segs[0], "DefaultCollection") {
			orgSegs = 1
		}
	}
	if orgSegs < 0 || orgSegs > marker {
		return Address{}, notWiki
	}
	a := Address{BaseURL: u.Scheme + "://" + u.Host}
	for _, s := range segs[:orgSegs] {
		a.BaseURL += "/" + url.PathEscape(s)
	}
	q := u.Query()
	if v := q.Get("version"); strings.HasPrefix(v, "GB") {
		a.Branch = strings.TrimPrefix(v, "GB")
	}
	switch segs[marker] {
	case "_git":
		a.Repo = segs[marker+1]
		a.Project = a.Repo // …/org/_git/repo: the project shares the repo's name
		if marker > orgSegs {
			a.Project = segs[marker-1]
		}
		a.Folder = cleanFolder(q.Get("path"))
		if path.Ext(a.Folder) != "" { // a file was open: use its folder
			a.Folder = cleanFolder(path.Dir(a.Folder))
		}
	case "_wiki":
		if marker == orgSegs {
			return Address{}, notWiki
		}
		a.Project = segs[marker-1]
		if bare {
			break
		}
		if marker+2 >= len(segs) || segs[marker+1] != "wikis" {
			return Address{}, notWiki
		}
		a.Wiki = segs[marker+2]
	}
	if a.Project == "" {
		return Address{}, notWiki
	}
	return a, nil
}

func cleanFolder(p string) string {
	p = strings.Trim(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"), "/")
	if p == "" || p == "." {
		return ""
	}
	return path.Clean(p)
}

// api returns a project-level API path.
func (r *Repo) api(p string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	query.Set("api-version", apiVersion)
	return "/" + url.PathEscape(r.addr.Project) + "/_apis/" + p + "?" + query.Encode()
}

// git returns a path under this repository's Git API.
func (r *Repo) git(p string, query url.Values) string {
	return r.api("git/repositories/"+url.PathEscape(r.repoID)+p, query)
}

// resolve finds the repository, branch and folder behind the address.
func (r *Repo) resolve(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved {
		return nil
	}
	a := &r.addr
	if a.Wiki != "" || r.project {
		w, err := r.wiki(ctx)
		if err != nil {
			return err
		}
		switch {
		case r.project && w.Type != "projectWiki":
			return fmt.Errorf("%q is a code wiki (published from a repository); create the connection as \"Azure DevOps Code wiki\" instead", w.Name)
		case !r.project && w.Type == "projectWiki":
			return fmt.Errorf("%q is a project wiki; create the connection as \"Azure DevOps Project wiki\" instead", w.Name)
		}
		a.Wiki = w.Name
		a.Repo = w.RepositoryID
		a.Folder = cleanFolder(w.MappedPath)
		if a.Branch == "" && len(w.Versions) > 0 {
			a.Branch = w.Versions[0].Version
		}
		if a.Branch == "" && r.project {
			a.Branch = projectBranch
		}
	}
	var repo repoInfo
	if err := r.client.Get(ctx, r.api("git/repositories/"+url.PathEscape(a.Repo), nil), &repo); err != nil {
		return r.friendly(err, "the repository "+a.Repo)
	}
	if repo.IsDisabled {
		return fmt.Errorf("the repository %s is disabled; ask a project admin to enable it", repo.Name)
	}
	r.repoID = repo.ID
	if a.Branch == "" {
		a.Branch = strings.TrimPrefix(repo.DefaultBranch, "refs/heads/")
	}
	if a.Branch == "" {
		a.Branch = "main" // empty repository: the first push creates it
	}
	r.resolved = true
	return nil
}

// wiki reads the wiki the address names; for a project wiki address without
// a name, the project's wiki.
func (r *Repo) wiki(ctx context.Context) (wikiInfo, error) {
	a := &r.addr
	if a.Wiki != "" {
		var w wikiInfo
		if err := r.client.Get(ctx, r.api("wiki/wikis/"+url.PathEscape(a.Wiki), nil), &w); err != nil {
			return wikiInfo{}, r.friendly(err, "the wiki "+a.Wiki)
		}
		return w, nil
	}
	var all list[wikiInfo]
	if err := r.client.Get(ctx, r.api("wiki/wikis", nil), &all); err != nil {
		return wikiInfo{}, r.friendly(err, "the wikis of the project")
	}
	for _, w := range all.Value {
		if w.Type == "projectWiki" {
			return w, nil
		}
	}
	return wikiInfo{}, fmt.Errorf("the project %s has no project wiki yet: create it in Azure DevOps (Overview → Wiki → Create project wiki), or check the address", a.Project)
}

// head returns the commit the branch points at, or "" when it does not exist.
func (r *Repo) head(ctx context.Context) (string, error) {
	var refs list[gitRef]
	if err := r.client.Get(ctx, r.git("/refs", url.Values{"filter": {"heads/" + r.addr.Branch}}), &refs); err != nil {
		return "", r.friendly(err, "the branch "+r.addr.Branch)
	}
	for _, ref := range refs.Value {
		if ref.Name == "refs/heads/"+r.addr.Branch {
			return ref.ObjectID, nil
		}
	}
	return "", nil
}

// Test checks the address, the token and access to the repository.
func (r *Repo) Test(ctx context.Context) error {
	if err := r.resolve(ctx); err != nil {
		return err
	}
	_, err := r.head(ctx)
	return err
}

// friendly turns API errors into messages a user can act on.
func (r *Repo) friendly(err error, what string) error {
	switch httpc.StatusOf(err) {
	case 0:
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("cannot reach Azure DevOps at %s; check the address and the network (proxy, VPN): %w", r.addr.BaseURL, err)
	case http.StatusUnauthorized:
		return errors.New("the token was rejected by Azure DevOps: check that the personal access token is correct, not expired, and valid for this organisation")
	case http.StatusForbidden:
		return fmt.Errorf("the token works but cannot access %s: give it the Code (Read & write) scope, or ask a project admin for access", what)
	case http.StatusNotFound:
		return fmt.Errorf("%s does not exist in the project %s, or this token cannot see it; check the address", what, r.addr.Project)
	default:
		return err
	}
}

// ---- API types ----

type list[T any] struct {
	Count int `json:"count"`
	Value []T `json:"value"`
}

type wikiInfo struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	RepositoryID string `json:"repositoryId"`
	MappedPath   string `json:"mappedPath"`
	Versions     []struct {
		Version string `json:"version"`
	} `json:"versions"`
}

type repoInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DefaultBranch string `json:"defaultBranch"`
	IsDisabled    bool   `json:"isDisabled"`
}

type gitRef struct {
	Name     string `json:"name"`
	ObjectID string `json:"objectId"`
}

type gitItem struct {
	ObjectID      string `json:"objectId"`
	GitObjectType string `json:"gitObjectType"` // blob | tree
	Path          string `json:"path"`
	IsFolder      bool   `json:"isFolder"`
}

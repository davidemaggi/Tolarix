// Package azuredevops is the Azure DevOps Code wiki connector: a wiki
// published from a folder of a Git repository. It reads and writes the
// repository through the Git REST API (no clone), and a run's writes become
// a single push.
//
// The reader and writer work on any repository + branch + folder, so the
// Project wiki (a hidden "<project>.wiki" repository) can reuse them.
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

// Kind is the registry key.
const Kind connectors.Kind = "azure-devops-code"

const apiVersion = "7.1"

func init() {
	connectors.Register(connectors.Spec{
		Kind:        Kind,
		DisplayName: "Azure DevOps Code wiki",
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
			return New(c, secret)
		},
	})
}

// Config is the stored connection configuration. The personal access token
// is the connection secret.
type Config struct {
	// URL is the address of the wiki (…/_wiki/wikis/<name>) or of the
	// repository folder the wiki is published from (…/_git/<repo>?path=/docs).
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
	addr   Address
	client *httpc.Client

	mu       sync.Mutex
	resolved bool
	repoID   string
}

var (
	_ connectors.Source = (*Repo)(nil)
	_ connectors.Target = (*Repo)(nil)
)

// New builds a connection.
func New(c Config, token secrets.String) (*Repo, error) {
	addr, err := ParseURL(c.URL)
	if err != nil {
		return nil, err
	}
	if !token.IsSet() {
		return nil, errors.New("the personal access token is missing: create one in Azure DevOps (User settings → Personal access tokens) with the Code (Read & write) scope and paste it here")
	}
	auth := func(r *http.Request) {
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+token.Reveal())))
	}
	return &Repo{addr: addr, client: httpc.New(addr.BaseURL, auth)}, nil
}

// Client exposes the HTTP client (tests shorten its retries).
func (r *Repo) Client() *httpc.Client { return r.client }

// ParseURL reads the address of a wiki or a repository as copied from the
// browser.
func ParseURL(raw string) (Address, error) {
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
	if marker < 0 || marker+1 >= len(segs) {
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
		if marker+2 >= len(segs) || segs[marker+1] != "wikis" || marker == orgSegs {
			return Address{}, notWiki
		}
		a.Project = segs[marker-1]
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
	if a.Wiki != "" {
		var w wikiInfo
		if err := r.client.Get(ctx, r.api("wiki/wikis/"+url.PathEscape(a.Wiki), nil), &w); err != nil {
			return r.friendly(err, "the wiki "+a.Wiki)
		}
		if w.Type != "codeWiki" {
			return fmt.Errorf("%q is a project wiki; this connection type is for code wikis (wikis published from a repository). Project wikis are supported in a later version", w.Name)
		}
		a.Repo = w.RepositoryID
		a.Folder = cleanFolder(w.MappedPath)
		if a.Branch == "" && len(w.Versions) > 0 {
			a.Branch = w.Versions[0].Version
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

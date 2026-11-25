// Package confluence is the Confluence Data Center / Server connector
// (REST API v1, storage format). It reads a space or a page subtree as a
// Master and writes Mirrors under a parent page or at the top of a space.
//
// See docs/dev/connectors.md for the mapping, identity and archive rules.
package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/connectors"
	"github.com/davidemaggi/tolarix/core/internal/httpc"
	"github.com/davidemaggi/tolarix/core/internal/secrets"
)

// Kind is the registry key.
const Kind connectors.Kind = "confluence-dc"

func init() {
	connectors.Register(connectors.Spec{
		Kind:        Kind,
		DisplayName: "Confluence Data Center / Server",
		CanRead:     true,
		CanWrite:    true,
		Fields: []connectors.Field{
			{Name: "url", Type: "url", Required: true},
			{Name: "token", Type: "secret", Required: true, Secret: true},
			{Name: "username", Type: "string"},
		},
		LocationFields: []connectors.Field{{Name: "parent", Type: "string"}},
		New: func(raw json.RawMessage, secret secrets.String) (connectors.Connector, error) {
			var c Config
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &c); err != nil {
					return nil, fmt.Errorf("invalid Confluence connection settings: %w", err)
				}
			}
			return New(c, secret)
		},
	})
}

// Config is the stored connection configuration. The token is the
// connection secret.
type Config struct {
	URL string `json:"url"`
	// Username switches to basic authentication (username + token as the
	// password) for servers without personal access tokens.
	Username string `json:"username,omitempty"`
}

// Location is a flow target inside the space.
type Location struct {
	// Parent is the title or numeric id of the page mirrored pages go under.
	// Empty: the connection's page, or the top of the space.
	Parent string `json:"parent,omitempty"`
}

// Address is what a Confluence URL points at.
type Address struct {
	BaseURL   string // scheme://host[/context]
	SpaceKey  string
	PageID    string
	PageTitle string
}

// Confluence is a configured connection; it is a Source and a Target.
type Confluence struct {
	addr   Address
	client *httpc.Client

	mu       sync.Mutex
	resolved bool
}

var (
	_ connectors.Source = (*Confluence)(nil)
	_ connectors.Target = (*Confluence)(nil)
)

// New builds a connection.
func New(c Config, token secrets.String) (*Confluence, error) {
	addr, err := ParseURL(c.URL)
	if err != nil {
		return nil, err
	}
	if !token.IsSet() {
		return nil, errors.New("the access token is missing: create a personal access token in Confluence (your profile → Personal Access Tokens) and paste it here")
	}
	user := strings.TrimSpace(c.Username)
	auth := func(r *http.Request) {
		if user != "" {
			r.SetBasicAuth(user, token.Reveal())
			return
		}
		r.Header.Set("Authorization", "Bearer "+token.Reveal())
	}
	return &Confluence{addr: addr, client: httpc.New(addr.BaseURL, auth)}, nil
}

// Client exposes the HTTP client (tests shorten its retries).
func (c *Confluence) Client() *httpc.Client { return c.client }

var (
	reDisplay  = regexp.MustCompile(`^(.*?)/display/([^/]+)(?:/([^/]+))?/?$`)
	reSpaces   = regexp.MustCompile(`^(.*?)/spaces/([^/]+)(?:/pages/(\d+)(?:/[^/]*)?|/overview)?/?$`)
	reViewPage = regexp.MustCompile(`^(.*?)/pages/viewpage\.action$`)
	reViewSp   = regexp.MustCompile(`^(.*?)/spaces/viewspace\.action$`)
	reTiny     = regexp.MustCompile(`^(.*?)/x/[^/]+$`)
)

// ParseURL reads the address of a space or a page as copied from the browser.
func ParseURL(raw string) (Address, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Address{}, errors.New("the address is missing: open the space or page in Confluence and copy the address from the browser")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Address{}, fmt.Errorf("%q is not a web address; copy it from the browser (it starts with https://)", raw)
	}
	p := strings.TrimRight(u.EscapedPath(), "/")
	base := func(ctx string) string { return u.Scheme + "://" + u.Host + ctx }
	unescape := func(s string) string {
		if v, err := url.PathUnescape(s); err == nil {
			s = v
		}
		return strings.ReplaceAll(s, "+", " ")
	}
	q := u.Query()
	switch {
	case reTiny.MatchString(p):
		return Address{}, errors.New("short links (/x/…) are not supported: open the page and copy its full address, or use Page information to find its pageId address")
	case reViewPage.MatchString(p):
		m := reViewPage.FindStringSubmatch(p)
		if id := q.Get("pageId"); id != "" {
			return Address{BaseURL: base(m[1]), PageID: id}, nil
		}
		if sk, t := q.Get("spaceKey"), q.Get("title"); sk != "" && t != "" {
			return Address{BaseURL: base(m[1]), SpaceKey: sk, PageTitle: t}, nil
		}
	case reViewSp.MatchString(p):
		m := reViewSp.FindStringSubmatch(p)
		if k := q.Get("key"); k != "" {
			return Address{BaseURL: base(m[1]), SpaceKey: k}, nil
		}
	case reDisplay.MatchString(p):
		m := reDisplay.FindStringSubmatch(p)
		a := Address{BaseURL: base(m[1]), SpaceKey: unescape(m[2])}
		if m[3] != "" {
			a.PageTitle = unescape(m[3])
		}
		return a, nil
	case reSpaces.MatchString(p):
		m := reSpaces.FindStringSubmatch(p)
		return Address{BaseURL: base(m[1]), SpaceKey: unescape(m[2]), PageID: m[3]}, nil
	}
	return Address{}, fmt.Errorf("%q does not look like the address of a Confluence space or page. Open the space (or the page to start from) and copy the address from the browser", raw)
}

// resolve fills in what the URL did not say: the space of a page id, the id
// of a page title.
func (c *Confluence) resolve(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.resolved {
		return nil
	}
	a := &c.addr
	switch {
	case a.PageID != "":
		var p content
		if err := c.client.Get(ctx, "/rest/api/content/"+url.PathEscape(a.PageID)+"?expand=space", &p); err != nil {
			return c.friendly(err, "the page "+a.PageID)
		}
		a.PageTitle = p.Title
		if p.Space != nil && a.SpaceKey == "" {
			a.SpaceKey = p.Space.Key
		}
	case a.PageTitle != "":
		p, err := c.findByTitle(ctx, a.SpaceKey, a.PageTitle)
		if err != nil {
			return c.friendly(err, "the space "+a.SpaceKey)
		}
		if p == nil {
			return fmt.Errorf("the page %q was not found in the space %s; check the address", a.PageTitle, a.SpaceKey)
		}
		a.PageID = p.ID
	}
	c.resolved = true
	return nil
}

func (c *Confluence) findByTitle(ctx context.Context, space, title string) (*content, error) {
	q := url.Values{"spaceKey": {space}, "title": {title}, "type": {"page"}, "expand": {"version"}}
	var res results[content]
	if err := c.client.Get(ctx, "/rest/api/content?"+q.Encode(), &res); err != nil {
		return nil, err
	}
	if len(res.Results) == 0 {
		return nil, nil
	}
	return &res.Results[0], nil
}

// Test checks the address, the token and access to the space.
func (c *Confluence) Test(ctx context.Context) error {
	if err := c.resolve(ctx); err != nil {
		return err
	}
	var sp struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := c.client.Get(ctx, "/rest/api/space/"+url.PathEscape(c.addr.SpaceKey), &sp); err != nil {
		return c.friendly(err, "the space "+c.addr.SpaceKey)
	}
	return nil
}

// friendly turns API errors into messages a user can act on.
func (c *Confluence) friendly(err error, what string) error {
	switch httpc.StatusOf(err) {
	case 0:
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("cannot reach Confluence at %s; check the address and the network (proxy, VPN): %w", c.addr.BaseURL, err)
	case http.StatusUnauthorized:
		return errors.New("Confluence rejected the token: check that the personal access token is correct and not expired (with a username set, the token is used as the password)")
	case http.StatusForbidden:
		return fmt.Errorf("the token works but has no permission on %s: ask a space admin for access", what)
	case http.StatusNotFound:
		return fmt.Errorf("%s does not exist or this token cannot see it; check the address", what)
	default:
		return err
	}
}

// ---- API types ----

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
	Ancestors []struct {
		ID string `json:"id"`
	} `json:"ancestors,omitempty"`
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

func (p *content) storage() string {
	if p.Body == nil {
		return ""
	}
	return p.Body.Storage.Value
}

// list fetches every page of a paged collection.
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

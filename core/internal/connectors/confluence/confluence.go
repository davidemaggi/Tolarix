// Package confluence holds the Confluence connectors: Data Center / Server
// (REST API v1) and Cloud (REST API v2), both on the storage format. Each
// reads a space or a page subtree as a Master and writes Mirrors under a
// parent page or at the top of a space.
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

// Registry keys.
const (
	Kind      connectors.Kind = "confluence-dc"
	CloudKind connectors.Kind = "confluence-cloud"
)

func init() {
	location := []connectors.Field{{Name: "parent", Type: "string"}}
	decode := func(raw json.RawMessage) (Config, error) {
		var c Config
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c); err != nil {
				return c, fmt.Errorf("invalid Confluence connection settings: %w", err)
			}
		}
		return c, nil
	}
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
		LocationFields: location,
		New: func(raw json.RawMessage, secret secrets.String) (connectors.Connector, error) {
			c, err := decode(raw)
			if err != nil {
				return nil, err
			}
			return New(c, secret)
		},
	})
	connectors.Register(connectors.Spec{
		Kind:        CloudKind,
		DisplayName: "Confluence Cloud",
		CanRead:     true,
		CanWrite:    true,
		Fields: []connectors.Field{
			{Name: "url", Type: "url", Required: true},
			{Name: "email", Type: "string", Required: true},
			{Name: "token", Type: "secret", Required: true, Secret: true},
		},
		LocationFields: location,
		New: func(raw json.RawMessage, secret secrets.String) (connectors.Connector, error) {
			c, err := decode(raw)
			if err != nil {
				return nil, err
			}
			return NewCloud(c, secret)
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
	// Email is the Atlassian account of a Cloud connection; the token is
	// an API token of that account.
	Email string `json:"email,omitempty"`
}

// Location is a flow target inside the space.
type Location struct {
	// Parent is the title or numeric id of the page mirrored pages go under.
	// Empty: the connection's page, else the top of the space (Data Center)
	// or the space homepage (Cloud).
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
	api    api
	cloud  bool

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
	c2 := &Confluence{addr: addr, client: httpc.New(addr.BaseURL, auth)}
	c2.api = &v1{c: c2}
	return c2, nil
}

// NewCloud builds a Confluence Cloud connection (email + API token).
func NewCloud(c Config, token secrets.String) (*Confluence, error) {
	addr, err := ParseURL(c.URL)
	if err != nil {
		return nil, err
	}
	// Cloud sites live under /wiki; accept an address that lost it.
	if u, err := url.Parse(addr.BaseURL); err == nil && strings.HasSuffix(u.Hostname(), ".atlassian.net") && u.Path == "" {
		addr.BaseURL += "/wiki"
	}
	email := strings.TrimSpace(c.Email)
	if email == "" {
		return nil, errors.New("the email is missing: enter the email of the Atlassian account the API token belongs to")
	}
	if !token.IsSet() {
		return nil, errors.New("the API token is missing: create one at id.atlassian.com (Security → API tokens) and paste it here")
	}
	auth := func(r *http.Request) { r.SetBasicAuth(email, token.Reveal()) }
	c2 := &Confluence{addr: addr, client: httpc.New(addr.BaseURL, auth), cloud: true}
	c2.api = &v2{c: c2}
	return c2, nil
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
		p, err := c.api.page(ctx, a.PageID, false)
		if err != nil {
			return c.friendly(err, "the page "+a.PageID)
		}
		a.PageTitle = p.Title
		if p.SpaceKey != "" && a.SpaceKey == "" {
			a.SpaceKey = p.SpaceKey
		}
	case a.PageTitle != "":
		p, err := c.api.pageByTitle(ctx, a.PageTitle)
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

// Test checks the address, the token and access to the space.
func (c *Confluence) Test(ctx context.Context) error {
	if err := c.resolve(ctx); err != nil {
		return err
	}
	if err := c.api.space(ctx); err != nil {
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
		if c.cloud {
			return errors.New("Confluence Cloud rejected the credentials: check the email and the API token (id.atlassian.com → Security → API tokens); scoped API tokens are not supported, use a classic one")
		}
		return errors.New("Confluence rejected the token: check that the personal access token is correct and not expired (with a username set, the token is used as the password)")
	case http.StatusForbidden:
		return fmt.Errorf("the token works but has no permission on %s: ask a space admin for access", what)
	case http.StatusNotFound:
		return fmt.Errorf("%s does not exist or this token cannot see it; check the address", what)
	default:
		return err
	}
}

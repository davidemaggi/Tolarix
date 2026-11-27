package confluence

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/davidemaggi/tolarix/core/internal/httpc"
)

// pageInfo is what the front matter options read about a page.
type pageInfo struct {
	CreatedBy userRef
	CreatedAt string
	UpdatedBy userRef
	UpdatedAt string
	Version   int
	Labels    []string
	WebUI     string // path of the page's web address, relative to the base URL
}

// userRef names a user the way the API does: Cloud gives an account id,
// Data Center a user key and a username. DisplayName, when the API already
// gave it, saves a lookup.
type userRef struct {
	AccountID   string `json:"accountId"`
	UserKey     string `json:"userKey"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

func (a *v1) info(ctx context.Context, id string) (*pageInfo, error) {
	var res struct {
		History struct {
			CreatedBy   userRef `json:"createdBy"`
			CreatedDate string  `json:"createdDate"`
		} `json:"history"`
		Version struct {
			By     userRef `json:"by"`
			When   string  `json:"when"`
			Number int     `json:"number"`
		} `json:"version"`
		Metadata struct {
			Labels struct {
				Results []label `json:"results"`
			} `json:"labels"`
		} `json:"metadata"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	}
	if err := a.c.client.Get(ctx, "/rest/api/content/"+url.PathEscape(id)+"?expand=history,version,metadata.labels", &res); err != nil {
		return nil, err
	}
	return &pageInfo{CreatedBy: res.History.CreatedBy, CreatedAt: res.History.CreatedDate,
		UpdatedBy: res.Version.By, UpdatedAt: res.Version.When, Version: res.Version.Number,
		Labels: labelNames(res.Metadata.Labels.Results), WebUI: res.Links.WebUI}, nil
}

func (a *v2) info(ctx context.Context, id string) (*pageInfo, error) {
	var res struct {
		AuthorID  string `json:"authorId"`
		CreatedAt string `json:"createdAt"`
		Version   struct {
			Number    int    `json:"number"`
			AuthorID  string `json:"authorId"`
			CreatedAt string `json:"createdAt"`
		} `json:"version"`
		Labels struct {
			Results []label `json:"results"`
		} `json:"labels"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	}
	if err := a.c.client.Get(ctx, v2Path("/pages/"+url.PathEscape(id), url.Values{"include-labels": {"true"}, "include-version": {"true"}}), &res); err != nil {
		return nil, err
	}
	return &pageInfo{CreatedBy: userRef{AccountID: res.AuthorID}, CreatedAt: res.CreatedAt,
		UpdatedBy: userRef{AccountID: res.Version.AuthorID}, UpdatedAt: res.Version.CreatedAt, Version: res.Version.Number,
		Labels: labelNames(res.Labels.Results), WebUI: res.Links.WebUI}, nil
}

type label struct {
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
}

// labelNames keeps the labels everyone sees (not personal "my:" labels).
func labelNames(ls []label) []string {
	var out []string
	for _, l := range ls {
		if l.Name != "" && (l.Prefix == "" || l.Prefix == "global") {
			out = append(out, l.Name)
		}
	}
	return out
}

// users looks up display names, once per user, for one Snapshot call.
type users struct {
	c     *Confluence
	ctx   context.Context //nolint:containedctx // scoped to one Snapshot call
	mu    sync.Mutex
	cache map[string]string // "a:"+account id, "k:"+user key, "u:"+username -> display name ("" = unknown)
}

// name returns the display name of a user. Lookups that fail are not
// retried in the same run; the caller falls back to what it has.
func (u *users) name(ref userRef) (string, bool) {
	if ref.DisplayName != "" {
		return ref.DisplayName, true
	}
	var key string
	var q url.Values
	switch {
	case ref.AccountID != "":
		key, q = "a:"+ref.AccountID, url.Values{"accountId": {ref.AccountID}}
	case ref.UserKey != "":
		key, q = "k:"+ref.UserKey, url.Values{"key": {ref.UserKey}}
	case ref.Username != "":
		key, q = "u:"+ref.Username, url.Values{"username": {ref.Username}}
	default:
		return "", false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if n, ok := u.cache[key]; ok {
		return n, n != ""
	}
	var res struct {
		DisplayName string `json:"displayName"`
	}
	if err := u.c.client.Get(u.ctx, "/rest/api/user?"+q.Encode(), &res); err != nil && httpc.StatusOf(err) == 0 {
		// A network problem: do not remember it, the next mention may work.
		return "", false
	}
	u.cache[key] = strings.TrimSpace(res.DisplayName)
	return u.cache[key], u.cache[key] != ""
}

// reTinyCode is a short link (/x/<code>), as Confluence's "Share" button
// copies it.
var (
	reTinyCode  = regexp.MustCompile(`/x/([A-Za-z0-9_-]{1,11})$`)
	reTinyChars = regexp.MustCompile(`^[A-Za-z0-9_-]{1,11}$`)
)

// tinyPageID decodes the code of a short link: the page id as little-endian
// bytes in URL-safe base64, with trailing zero bytes ("A"s) and padding cut.
func tinyPageID(code string) (string, bool) {
	if !reTinyChars.MatchString(code) {
		return "", false
	}
	s := strings.NewReplacer("-", "/", "_", "+").Replace(code)
	s += strings.Repeat("A", 11-len(s)) + "="
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 8 {
		return "", false
	}
	id := binary.LittleEndian.Uint64(b)
	if id == 0 || id > math.MaxInt64 {
		return "", false
	}
	return strconv.FormatUint(id, 10), true
}

// Package httpc is the HTTP client shared by the remote connectors
// (Confluence, Azure DevOps): base URL, authentication, JSON helpers and
// retries with backoff for rate limits (429) and transient server errors.
//
// Retry rules:
//   - GET, HEAD, DELETE, OPTIONS: retried on 429, 500, 502, 503, 504 and on
//     network errors.
//   - POST, PUT, PATCH: retried only on 429 and 503, where the server did not
//     process the request, so a write is never applied twice.
//
// The wait honours Retry-After (seconds or an HTTP date), then
// X-RateLimit-Reset (Unix seconds), else grows exponentially with jitter.
package httpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/davidemaggi/tolarix/core/internal/buildinfo"
)

// Client talks to one service.
type Client struct {
	HTTP    *http.Client
	BaseURL string // without a trailing slash
	// Auth adds credentials to every request. It must not log them.
	Auth func(r *http.Request)
	// MaxRetries is the number of retries after the first attempt (default 5).
	MaxRetries int
	// MaxWait caps a single wait (default 2 minutes).
	MaxWait time.Duration
	// Sleep waits between attempts; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Request is one call. Body is a function so a retry can send it again.
type Request struct {
	Method string
	Path   string // appended to BaseURL; may include a query string
	Header http.Header
	Body   func() (io.Reader, error)
}

// Error is a non-2xx answer.
type Error struct {
	Method  string
	URL     string
	Status  int
	Message string // the service's own message, when it sent one
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s %s: %d %s", e.Method, e.URL, e.Status, http.StatusText(e.Status))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// StatusOf returns the HTTP status of an *Error in err's chain, or 0.
func StatusOf(err error) int {
	var he *Error
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

// New returns a client with sensible timeouts.
func New(baseURL string, auth func(*http.Request)) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
		BaseURL: strings.TrimRight(baseURL, "/"),
		Auth:    auth,
	}
}

func (c *Client) maxRetries() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return 5
}

func (c *Client) maxWait() time.Duration {
	if c.MaxWait > 0 {
		return c.MaxWait
	}
	return 2 * time.Minute
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// URL returns the absolute URL for path.
func (c *Client) URL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.BaseURL + path
}

// Do sends the request, retrying as described in the package comment. A
// 2xx response is returned open (the caller closes the body); anything else
// is an *Error.
func (c *Client) Do(ctx context.Context, r Request) (*http.Response, error) {
	idempotent := r.Method == http.MethodGet || r.Method == http.MethodHead ||
		r.Method == http.MethodDelete || r.Method == http.MethodOptions
	url := c.URL(r.Path)
	for attempt := 0; ; attempt++ {
		req, err := c.build(ctx, r, url)
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !idempotent || attempt >= c.maxRetries() {
				return nil, fmt.Errorf("%s %s: %w", r.Method, url, err)
			}
			if err := c.sleep(ctx, c.backoff(attempt, nil)); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable ||
			(idempotent && (resp.StatusCode == http.StatusInternalServerError ||
				resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout))
		if retry && attempt < c.maxRetries() {
			wait := c.backoff(attempt, resp.Header)
			drain(resp)
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		return nil, readError(r.Method, url, resp)
	}
}

func (c *Client) build(ctx context.Context, r Request, url string) (*http.Request, error) {
	var body io.Reader
	if r.Body != nil {
		b, err := r.Body()
		if err != nil {
			return nil, err
		}
		body = b
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, url, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("User-Agent", "Tolarix/"+buildinfo.Version)
	if c.Auth != nil {
		c.Auth(req)
	}
	return req, nil
}

// backoff picks the wait before the next attempt.
func (c *Client) backoff(attempt int, h http.Header) time.Duration {
	limit := c.maxWait()
	if d, ok := serverWait(h, time.Now()); ok {
		return min(max(d, 0), limit)
	}
	base := time.Second << min(attempt, 6)
	jitter := time.Duration(rand.Int64N(int64(base) / 2)) //nolint:gosec // jitter, not security
	return min(base+jitter, limit)
}

// serverWait reads Retry-After (seconds or HTTP date) or X-RateLimit-Reset
// (Unix seconds).
func serverWait(h http.Header, now time.Time) (time.Duration, bool) {
	if h == nil {
		return 0, false
	}
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if s, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(s * float64(time.Second)), true
		}
		if t, err := http.ParseTime(v); err == nil {
			return t.Sub(now), true
		}
	}
	if v := strings.TrimSpace(h.Get("X-RateLimit-Reset")); v != "" {
		if s, err := strconv.ParseInt(v, 10, 64); err == nil && s > 1_000_000_000 {
			return time.Unix(s, 0).Sub(now), true
		}
	}
	return 0, false
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// readError builds an *Error, keeping the service's message when the body
// is JSON with "message" (Confluence, Azure DevOps) or "errorMessages".
func readError(method, url string, resp *http.Response) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	e := &Error{Method: method, URL: url, Status: resp.StatusCode}
	var body struct {
		Message       string   `json:"message"`
		ErrorMessages []string `json:"errorMessages"`
	}
	if json.Unmarshal(b, &body) == nil {
		switch {
		case body.Message != "":
			e.Message = body.Message
		case len(body.ErrorMessages) > 0:
			e.Message = strings.Join(body.ErrorMessages, "; ")
		}
	}
	if len(e.Message) > 500 {
		e.Message = e.Message[:500] + "…"
	}
	return e
}

// JSON sends in (if not nil) as JSON and decodes the answer into out (if not nil).
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) error {
	r := Request{Method: method, Path: path}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		r.Header = http.Header{"Content-Type": {"application/json"}}
		r.Body = func() (io.Reader, error) { return bytes.NewReader(b), nil }
	}
	resp, err := c.Do(ctx, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		// Azure DevOps answers a bad or expired token with 203 and a sign-in page.
		return &Error{Method: method, URL: c.URL(path), Status: http.StatusUnauthorized,
			Message: "the server answered with a sign-in page instead of data: the token is wrong, expired or lacks access"}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: unexpected answer (not JSON?): %w", method, c.URL(path), err)
	}
	return nil
}

// Get is JSON with GET.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.JSON(ctx, http.MethodGet, path, nil, out)
}

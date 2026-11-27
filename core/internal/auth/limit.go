package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Limiter counts failed sign-ins per client in a fixed window. After Max
// failures the client is refused until the window ends. In memory only: a
// restart clears it, which is acceptable for a single-instance server.
type Limiter struct {
	Max    int
	Window time.Duration
	Now    func() time.Time

	mu      sync.Mutex
	entries map[string]*limitEntry
}

type limitEntry struct {
	failures int
	start    time.Time
}

// NewLimiter allows max failures per window.
func NewLimiter(maxFailures int, window time.Duration) *Limiter {
	return &Limiter{Max: maxFailures, Window: window, Now: time.Now, entries: map[string]*limitEntry{}}
}

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Allow reports whether key may try again, and if not, how long to wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return true, 0
	}
	t := l.now()
	if t.Sub(e.start) >= l.Window {
		delete(l.entries, key)
		return true, 0
	}
	if e.failures >= l.Max {
		return false, e.start.Add(l.Window).Sub(t)
	}
	return true, 0
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	if l.entries == nil {
		l.entries = map[string]*limitEntry{}
	}
	if len(l.entries) > 10000 {
		l.prune(t)
	}
	e := l.entries[key]
	if e == nil || t.Sub(e.start) >= l.Window {
		e = &limitEntry{start: t}
		l.entries[key] = e
	}
	e.failures++
}

// Reset forgets key (after a successful sign-in).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func (l *Limiter) prune(t time.Time) {
	for k, e := range l.entries {
		if t.Sub(e.start) >= l.Window {
			delete(l.entries, k)
		}
	}
}

// ParseTrustedProxies parses a comma-separated list of IPs and CIDRs
// (TOLARIX_TRUSTED_PROXIES), e.g. "10.0.0.0/8, 172.16.0.1".
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "/") {
			p, err := netip.ParsePrefix(part)
			if err != nil {
				return nil, err
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return nil, err
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// ClientIP returns the address of the client. X-Forwarded-For is only used
// when the direct peer is a trusted proxy; then the right-most address that
// is not itself a trusted proxy wins (left-most entries are client-supplied
// and can be forged).
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	peer := remoteAddr(r)
	if !peer.IsValid() {
		return r.RemoteAddr
	}
	if !isTrusted(peer, trusted) {
		return peer.String()
	}
	hops := forwardedFor(r)
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(hops[i])
		if err != nil {
			break
		}
		a = a.Unmap()
		if !isTrusted(a, trusted) {
			return a.String()
		}
	}
	return peer.String()
}

// ForwardedHTTPS reports whether a trusted proxy says the client used HTTPS.
func ForwardedHTTPS(r *http.Request, trusted []netip.Prefix) bool {
	if r.TLS != nil {
		return true
	}
	peer := remoteAddr(r)
	return peer.IsValid() && isTrusted(peer, trusted) &&
		strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func remoteAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

func forwardedFor(r *http.Request) []string {
	var out []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, p := range strings.Split(h, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

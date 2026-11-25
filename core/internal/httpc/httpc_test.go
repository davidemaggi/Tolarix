package httpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var waits []time.Duration
	c := New(srv.URL, func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") })
	c.Sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	return c, &waits
}

func TestRetriesRateLimitHonouringRetryAfter(t *testing.T) {
	var n atomic.Int32
	c, waits := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		if n.Add(1) < 3 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	var out struct{ OK bool }
	if err := c.Get(context.Background(), "/x", &out); err != nil || !out.OK {
		t.Fatalf("Get = %v, %+v", err, out)
	}
	if len(*waits) != 2 || (*waits)[0] != 7*time.Second {
		t.Fatalf("waits = %v", *waits)
	}
}

func TestPostIsNotRetriedOnServerError(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	})
	err := c.JSON(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}, nil)
	var he *Error
	if !errors.As(err, &he) || he.Status != 500 || he.Message != "boom" {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", n.Load())
	}
}

func TestGetRetriesServerErrorsThenGivesUp(t *testing.T) {
	var n atomic.Int32
	c, waits := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})
	c.MaxRetries = 2
	err := c.Get(context.Background(), "/x", nil)
	if StatusOf(err) != http.StatusBadGateway || n.Load() != 3 || len(*waits) != 2 {
		t.Fatalf("err=%v attempts=%d waits=%v", err, n.Load(), *waits)
	}
	if (*waits)[0] < time.Second || (*waits)[1] < 2*time.Second {
		t.Fatalf("backoff too short: %v", *waits)
	}
}

func TestErrorMessagesFromErrorMessagesArray(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errorMessages":["No space with key : X"]}`))
	})
	err := c.Get(context.Background(), "/x", nil)
	var he *Error
	if !errors.As(err, &he) || he.Message != "No space with key : X" {
		t.Fatalf("err = %v", err)
	}
}

func TestServerWait(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h := http.Header{}
	h.Set("X-RateLimit-Reset", "1767268830") // now + 30 s
	if d, ok := serverWait(h, now); !ok || d != 30*time.Second {
		t.Fatalf("reset: %v %v", d, ok)
	}
	h = http.Header{}
	h.Set("Retry-After", now.Add(10*time.Second).Format(http.TimeFormat))
	if d, ok := serverWait(h, now); !ok || d != 10*time.Second {
		t.Fatalf("date: %v %v", d, ok)
	}
}

func TestHTMLSignInPageIsAnAuthError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNonAuthoritativeInfo)
		_, _ = w.Write([]byte("<html>Sign in</html>"))
	})
	if err := c.Get(context.Background(), "/x", nil); StatusOf(err) != http.StatusUnauthorized {
		t.Fatalf("err = %v", err)
	}
}

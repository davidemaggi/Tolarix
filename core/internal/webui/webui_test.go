package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":        {Data: []byte("<html>app</html>")},
		"assets/app-1a2.js": {Data: []byte("console.log(1)")},
		"favicon.svg":       {Data: []byte("<svg/>")},
	}
	h := Handler(fsys)

	cases := []struct {
		path, wantBody, wantCache string
		wantCode                  int
	}{
		{"/", "app", "no-cache", 200},
		{"/flows/123", "app", "no-cache", 200}, // client route -> index
		{"/assets/app-1a2.js", "console.log", "immutable", 200},
		{"/favicon.svg", "<svg/>", "", 200},
		{"/missing.js", "", "", 404},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rr.Code != c.wantCode {
			t.Errorf("%s: code %d want %d", c.path, rr.Code, c.wantCode)
			continue
		}
		if !strings.Contains(rr.Body.String(), c.wantBody) {
			t.Errorf("%s: body %q", c.path, rr.Body.String())
		}
		if c.wantCache != "" && !strings.Contains(rr.Header().Get("Cache-Control"), c.wantCache) {
			t.Errorf("%s: cache-control %q", c.path, rr.Header().Get("Cache-Control"))
		}
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: code %d", rr.Code)
	}
}

func TestHandlerNotBuilt(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler(fstest.MapFS{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "not been built") {
		t.Fatalf("code=%d body=%q", rr.Code, rr.Body.String())
	}
}

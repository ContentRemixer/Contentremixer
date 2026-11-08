package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve drives Handler() against a path and returns the response recorder.
func serve(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	Handler().ServeHTTP(rec, req)
	return rec
}

func TestAssetsHasIndex(t *testing.T) {
	f, err := Assets().Open("index.html")
	if err != nil {
		t.Fatalf("embedded index.html missing: %v", err)
	}
	_ = f.Close()
}

func TestHandlerServesIndexAtRoot(t *testing.T) {
	rec := serve(t, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "<div id=\"root\">") {
		t.Fatalf("GET / did not return the SPA shell; body=%.80q", string(body))
	}
}

func TestHandlerServesStaticAsset(t *testing.T) {
	// favicon.svg is a real, committed static asset (not hashed), so the
	// file-serving branch is exercised deterministically across builds.
	rec := serve(t, "/favicon.svg")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /favicon.svg = %d, want 200", rec.Code)
	}
}

func TestHandlerFallsBackToIndexForClientRoutes(t *testing.T) {
	// A deep-linked client route has no matching file → SPA fallback to
	// index.html with a no-cache header (so hashed-bundle refs stay fresh).
	rec := serve(t, "/content/some/deep/route")
	if rec.Code != http.StatusOK {
		t.Fatalf("deep link = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("fallback Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("fallback Cache-Control = %q, want no-cache", cc)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "<div id=\"root\">") {
		t.Fatalf("fallback did not return index.html; body=%.80q", string(body))
	}
}

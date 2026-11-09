package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// Serve-from-Booth integration tests (ISI-7214, W8). These exercise the embedded
// SPA (booth/web/embed.go) through the *fully wired* Booth mux built by New(...),
// not web.Handler() in isolation (that is unit-covered in booth/web/embed_test.go).
// The point is the mount ORDER in server.go: health probes and /v1/* API routes
// must win over the SPA catch-all registered last at "/", while every unknown
// path must fall through to index.html so client-side routes deep-link.

const spaMarker = `<div id="root">`

func readBody(t *testing.T, w interface{ Result() *http.Response }) string {
	t.Helper()
	b, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// GET / serves the embedded SPA shell through the full Booth handler.
func TestServeWeb_RootServesSPA(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, "GET", "/", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if body := readBody(t, w); !strings.Contains(body, spaMarker) {
		t.Fatalf("GET / did not serve the SPA shell; body=%.80q", body)
	}
}

// An unknown, non-API path falls back to index.html (SPA client route deep-link)
// rather than 404 — this is what lets /content, /admin, /scraper-review reload.
func TestServeWeb_ClientRouteFallsBackToIndex(t *testing.T) {
	h, _ := newTestServer(t)
	for _, p := range []string{"/content/abc123", "/admin", "/scraper-review", "/states"} {
		w := do(t, h, "GET", p, "", "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (SPA fallback)", p, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("GET %s Content-Type = %q, want text/html", p, ct)
		}
		if body := readBody(t, w); !strings.Contains(body, spaMarker) {
			t.Fatalf("GET %s did not fall back to the SPA shell; body=%.80q", p, body)
		}
	}
}

// Health probes are registered before the SPA catch-all, so they must NOT be
// swallowed by the index.html fallback.
func TestServeWeb_HealthNotSwallowedBySPA(t *testing.T) {
	h, _ := newTestServer(t)
	for _, p := range []string{"/healthz", "/readyz"} {
		w := do(t, h, "GET", p, "", "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", p, w.Code)
		}
		if body := readBody(t, w); strings.Contains(body, spaMarker) {
			t.Fatalf("GET %s was swallowed by the SPA fallback; body=%.80q", p, body)
		}
	}
}

// /v1/* API routes take precedence over the SPA catch-all: an unauthenticated
// API call returns 401 JSON, never the SPA index.html with a 200.
func TestServeWeb_APIRoutesWinOverSPA(t *testing.T) {
	h, _ := newTestServer(t, mp("m1"))
	for _, p := range []string{"/v1/auth/whoami", "/v1/modelprofiles"} {
		w := do(t, h, "GET", p, "", "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s (no token) = %d, want 401 (API, not SPA)", p, w.Code)
		}
		if body := readBody(t, w); strings.Contains(body, spaMarker) {
			t.Fatalf("GET %s fell through to the SPA shell; body=%.80q", p, body)
		}
	}
}

// A real embedded static asset (favicon) is served by path through the full mux,
// not rewritten to index.html.
func TestServeWeb_StaticAssetServed(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, "GET", "/favicon.svg", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /favicon.svg = %d, want 200", w.Code)
	}
	if body := readBody(t, w); strings.Contains(body, spaMarker) {
		t.Fatalf("GET /favicon.svg returned the SPA shell, not the asset; body=%.80q", body)
	}
}

package scrape

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// stubDoer routes requests to canned responses keyed by exact URL. An absent
// URL returns the configured default (404 by default) so tests assert exactly
// which URLs a scraper fetches.
type stubDoer struct {
	routes map[string]stubResp
	calls  []string
}

type stubResp struct {
	status int
	body   []byte
	err    error
}

func (d *stubDoer) Do(req *http.Request) (*http.Response, error) {
	d.calls = append(d.calls, req.URL.String())
	r, ok := d.routes[req.URL.String()]
	if !ok {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("not found"))}, nil
	}
	if r.err != nil {
		return nil, r.err
	}
	status := r.status
	if status == 0 {
		status = 200
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(r.body)),
		Header:     http.Header{},
	}, nil
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func fixedClock(t *testing.T) {
	t.Helper()
	orig := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFunc = orig })
}

func TestScrapeYouTube(t *testing.T) {
	fixedClock(t)
	watchURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	capURL := "https://www.youtube.com/api/timedtext?v=dQw4w9WgXcQ&lang=en"
	doer := &stubDoer{routes: map[string]stubResp{
		watchURL: {body: fixture(t, "youtube_watch.html")},
		capURL:   {body: fixture(t, "youtube_captions.xml")},
	}}
	s := New(&Options{HTTP: doer})

	docs, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{
		URL: "https://youtu.be/dQw4w9WgXcQ", Kind: "youtube",
	})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("want 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if d.SourceKind != "youtube" {
		t.Errorf("SourceKind = %q", d.SourceKind)
	}
	if d.Title != "Observability in Go — DevRel Talk" {
		t.Errorf("Title = %q", d.Title)
	}
	// Human English track preferred over the asr track (both en).
	if got := d.Meta["captionLanguage"]; got != "en" {
		t.Errorf("captionLanguage = %v", got)
	}
	if got := d.Meta["captionAutoGen"]; got != false {
		t.Errorf("captionAutoGen = %v, want false (human track preferred)", got)
	}
	// Entity double-escaping (&amp;#39;) resolves to an apostrophe.
	if !strings.Contains(d.Text, "we're talking about observability") {
		t.Errorf("transcript not normalized: %q", d.Text)
	}
	if strings.Contains(d.Text, "\n") {
		t.Errorf("transcript should be single-line, got newline: %q", d.Text)
	}
	if !d.FetchedAt.Equal(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("FetchedAt = %v", d.FetchedAt)
	}
	// It must have fetched the human track, not the asr one.
	for _, c := range doer.calls {
		if strings.Contains(c, "kind=asr") {
			t.Errorf("fetched auto-generated track when a human track existed: %s", c)
		}
	}
}

func TestScrapeYouTubeNoCaptions(t *testing.T) {
	watchURL := "https://www.youtube.com/watch?v=noCaps12345"
	noCapPage := `<html><body><script>var ytInitialPlayerResponse = {"videoDetails":{"title":"x"},"captions":{}};</script></body></html>`
	doer := &stubDoer{routes: map[string]stubResp{watchURL: {body: []byte(noCapPage)}}}
	s := New(&Options{HTTP: doer})

	_, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{
		URL: "https://www.youtube.com/watch?v=noCaps12345", Kind: "youtube",
	})
	if err == nil || !strings.Contains(err.Error(), "no caption track") {
		t.Fatalf("want no-caption error, got %v", err)
	}
}

func TestScrapeArticle(t *testing.T) {
	url := "https://example.com/blog/devrel-metrics"
	doer := &stubDoer{routes: map[string]stubResp{
		url:                              {body: fixture(t, "article.html")},
		"https://example.com/robots.txt": {status: 404},
	}}
	s := New(&Options{HTTP: doer})

	docs, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{URL: url, Kind: "article"})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("want 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if d.Title != "Why DevRel Teams Should Own Their Metrics" {
		t.Errorf("Title = %q (want og:title)", d.Title)
	}
	// Main content present...
	for _, want := range []string{"Developer relations lives or dies by trust", "Own the metric, own the narrative"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("missing article body %q in:\n%s", want, d.Text)
		}
	}
	// ...boilerplate stripped.
	for _, bad := range []string{"console.log", "All rights reserved", "Related posts", "Privacy"} {
		if strings.Contains(d.Text, bad) {
			t.Errorf("boilerplate %q leaked into article text:\n%s", bad, d.Text)
		}
	}
}

func TestScrapeArticleRobotsDisallow(t *testing.T) {
	url := "https://blocked.example.com/post"
	robots := "User-agent: *\nDisallow: /post\n"
	doer := &stubDoer{routes: map[string]stubResp{
		"https://blocked.example.com/robots.txt": {body: []byte(robots)},
		url:                                      {body: fixture(t, "article.html")},
	}}
	s := New(&Options{HTTP: doer})

	_, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{URL: url, Kind: "article"})
	if err == nil || !strings.Contains(err.Error(), "robots.txt") {
		t.Fatalf("want robots disallow error, got %v", err)
	}
	// The page itself must never be fetched when robots disallows.
	for _, c := range doer.calls {
		if c == url {
			t.Errorf("fetched disallowed url despite robots.txt")
		}
	}
}

func TestScrapeRobotsAllowOverride(t *testing.T) {
	url := "https://ok.example.com/post"
	// Disallow everything for a different agent; our UA falls to * (allow).
	robots := "User-agent: BadBot\nDisallow: /\n\nUser-agent: *\nAllow: /\n"
	doer := &stubDoer{routes: map[string]stubResp{
		"https://ok.example.com/robots.txt": {body: []byte(robots)},
		url:                                 {body: fixture(t, "article.html")},
	}}
	s := New(&Options{HTTP: doer})
	if _, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{URL: url, Kind: "article"}); err != nil {
		t.Fatalf("Scrape: %v", err)
	}
}

func TestScrapeUnknownKind(t *testing.T) {
	s := New(&Options{HTTP: &stubDoer{routes: map[string]stubResp{}}})
	_, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{URL: "https://x.com", Kind: "podcast"})
	if err == nil || !strings.Contains(err.Error(), "unsupported reference kind") {
		t.Fatalf("want unsupported-kind error, got %v", err)
	}
}

func TestScrapeEmptyURL(t *testing.T) {
	s := New(nil)
	if _, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{Kind: "article"}); err == nil {
		t.Fatalf("want empty-url error")
	}
}

func TestVideoIDExtraction(t *testing.T) {
	cases := map[string]string{
		"https://youtu.be/dQw4w9WgXcQ":                      "dQw4w9WgXcQ",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10s": "dQw4w9WgXcQ",
		"https://youtube.com/embed/dQw4w9WgXcQ":             "dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":        "dQw4w9WgXcQ",
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ":         "dQw4w9WgXcQ",
	}
	for in, want := range cases {
		got, err := youTubeVideoID(in)
		if err != nil || got != want {
			t.Errorf("youTubeVideoID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := youTubeVideoID("https://example.com/video"); err == nil {
		t.Errorf("want error for non-youtube url")
	}
}

func TestFetchNon2xxIsError(t *testing.T) {
	url := "https://example.com/missing"
	doer := &stubDoer{routes: map[string]stubResp{
		url:                              {status: 500, body: []byte("boom")},
		"https://example.com/robots.txt": {status: 404},
	}}
	s := New(&Options{HTTP: doer})
	if _, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{URL: url, Kind: "article"}); err == nil {
		t.Fatalf("want error on 500")
	}
}

// Package scrape is P2-B's (ISI-7145) reference-fetch stage (design §3.1): it
// resolves a DevRelPersona spec.references[] entry to normalized plain text +
// metadata (rag.RawDoc), keyed by the reference Kind. It produces TEXT ONLY —
// no vectors — so it is independent of decision #6 (pgvector) and hands its
// RawDocs to the chunk stage (internal/rag/chunk), which hands chunks to P2-C.
//
// Dispatch covers every v1alpha1.KnownReferenceKinds entry (and the CRD enum).
// Two kinds fetch today; the three DevRel-onboarding kinds (ISI-7356 contract)
// are registered as fail-closed placeholders until D2 (ISI-7357) lands their
// fetchers, so byKind's key set always equals the enum:
//
//	youtube             → fetch the video's caption track → transcript text
//	article             → fetch the page HTML → readable article body (also blog)
//	github-repo         → markdown-only repo fetch              (D2: ISI-7357)
//	teleprompter-script → script document → plain text          (D2: ISI-7357)
//	talk-recording      → captioned recording URL → captions    (D2: ISI-7357)
//
// Every network call goes through an injectable Doer seam and is bounded by a
// per-scrape timeout, so the whole stage is unit-testable with fixtures and
// never performs an unbounded crawl. Fetching respects robots.txt by default
// (design: "respect robots/ToS and timeouts").
package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
)

// Doer is the minimal HTTP seam the scrapers depend on. *http.Client satisfies
// it; tests inject a fixture-backed stub so no test touches the network.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Defaults for the scrape budget (design §3.1 "scraping is bounded").
const (
	// DefaultTimeout bounds a single reference scrape (watch-page fetch +
	// caption/article fetch combined share this deadline via the context).
	DefaultTimeout = 20 * time.Second
	// DefaultMaxDocs caps the RawDocs a single reference may fan out to. A
	// youtube/article reference resolves to exactly one today, but the cap is
	// enforced centrally so future fan-out kinds (channel/feed) cannot trigger
	// an unbounded crawl.
	DefaultMaxDocs = 50
	// DefaultUserAgent identifies the scraper to origins and robots.txt.
	DefaultUserAgent = "ContentRemixer-PersonaScraper/1.0 (+https://github.com/ContentRemixer/Contentremixer)"
	// maxBodyBytes caps how much of any single response body we read, so a
	// hostile or runaway origin cannot exhaust memory.
	maxBodyBytes = 8 << 20 // 8 MiB
)

// Options configures a Scraper. The zero value is usable: a Scraper built with
// (nil Options) uses DefaultTimeout/DefaultMaxDocs/DefaultUserAgent, a default
// *http.Client, and robots.txt enforcement enabled.
type Options struct {
	// HTTP is the client used for every fetch. Defaults to an *http.Client
	// with DefaultTimeout when nil.
	HTTP Doer
	// Timeout bounds one reference scrape. Defaults to DefaultTimeout when 0.
	Timeout time.Duration
	// MaxDocs caps fanned-out docs per reference. Defaults to DefaultMaxDocs
	// when 0; a negative value disables the cap (not recommended).
	MaxDocs int
	// UserAgent is sent on every request and matched against robots.txt.
	// Defaults to DefaultUserAgent when "".
	UserAgent string
	// DisableRobots turns off robots.txt enforcement. Default (false) means a
	// Disallow for our UserAgent on the target path fails the scrape closed.
	DisableRobots bool
}

func (o *Options) timeout() time.Duration {
	if o != nil && o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultTimeout
}

func (o *Options) maxDocs() int {
	if o != nil && o.MaxDocs != 0 {
		return o.MaxDocs
	}
	return DefaultMaxDocs
}

func (o *Options) userAgent() string {
	if o != nil && o.UserAgent != "" {
		return o.UserAgent
	}
	return DefaultUserAgent
}

// kindScraper fetches one already-validated reference of a single Kind.
type kindScraper interface {
	scrape(ctx context.Context, ref v1alpha1.PersonaReference) ([]rag.RawDoc, error)
}

// Scraper is the rag.Scraper implementation: it dispatches by reference Kind
// to the registered per-kind scraper.
type Scraper struct {
	opts    Options
	robots  *robotsCache
	byKind  map[string]kindScraper
	maxDocs int
}

var _ rag.Scraper = (*Scraper)(nil)

// New builds a Scraper wired for the youtube and article kinds. Passing nil
// opts uses all defaults.
func New(opts *Options) *Scraper {
	var o Options
	if opts != nil {
		o = *opts
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: o.timeout()}
	}
	o.UserAgent = o.userAgent()

	f := &fetcher{do: o.HTTP, userAgent: o.UserAgent}
	s := &Scraper{
		opts:    o,
		maxDocs: o.maxDocs(),
		robots:  newRobotsCache(f, o.DisableRobots),
	}
	s.byKind = map[string]kindScraper{
		KindYouTube: &youtubeScraper{fetch: f},
		KindArticle: &articleScraper{fetch: f, robots: s.robots},
		// DevRel onboarding kinds (ISI-7356 contract). Their fetchers are D2
		// (ISI-7357); until then they fail closed with a clear "not yet
		// implemented" error so a reference that admits against the CRD enum
		// surfaces an actionable scrape failure rather than the misleading
		// "unsupported kind" path (which, now that the enum lists these, would
		// name the kind as both wanted and unsupported). byKind's key set MUST
		// equal v1alpha1.KnownReferenceKinds — asserted by TestByKindCoversKnownKinds.
		KindGitHubRepo:         notImplemented(KindGitHubRepo, "ISI-7357"),
		KindTeleprompterScript: notImplemented(KindTeleprompterScript, "ISI-7357"),
		KindTalkRecording:      notImplemented(KindTalkRecording, "ISI-7357"),
	}
	return s
}

// Kind identifiers alias the v1alpha1 contract constants (the single source of
// truth) so the dispatch cannot drift from KnownReferenceKinds.
const (
	KindYouTube            = v1alpha1.ReferenceKindYouTube
	KindArticle            = v1alpha1.ReferenceKindArticle
	KindGitHubRepo         = v1alpha1.ReferenceKindGitHubRepo
	KindTeleprompterScript = v1alpha1.ReferenceKindTeleprompterScript
	KindTalkRecording      = v1alpha1.ReferenceKindTalkRecording
)

// notImplementedScraper is a placeholder for a reference kind that is in the
// contract (v1alpha1.KnownReferenceKinds) but whose fetcher has not landed yet.
// It keeps byKind's key set equal to the enum (lock-step) while failing closed
// with an actionable error naming the tracking issue. D2 replaces each entry
// with the real per-kind scraper (ISI-7357).
type notImplementedScraper struct {
	kind  string
	issue string
}

func notImplemented(kind, issue string) kindScraper {
	return notImplementedScraper{kind: kind, issue: issue}
}

func (n notImplementedScraper) scrape(context.Context, v1alpha1.PersonaReference) ([]rag.RawDoc, error) {
	return nil, fmt.Errorf("scrape: reference kind %q is not yet implemented (tracked by %s)", n.kind, n.issue)
}

// Scrape resolves one reference to RawDocs. It validates the reference, applies
// the per-scrape timeout, dispatches by Kind, stamps FetchedAt/SourceKind, and
// enforces the fan-out cap. Unknown kinds and malformed references fail closed
// with a descriptive error the controller isolates per-reference.
func (s *Scraper) Scrape(ctx context.Context, ref v1alpha1.PersonaReference) (out []rag.RawDoc, err error) {
	kind := strings.TrimSpace(ref.Kind)
	url := strings.TrimSpace(ref.URL)

	// §6.1 rag.scrape span (source.url is a span attr, never a metric label) +
	// §4.2 duration/docs/errors. A single deferred recorder covers every return
	// path so a failure and its reason are always counted.
	tel := telemetry.Default()
	ctx, span := tel.StartScrape(ctx, kind, url)
	started := time.Now()
	defer func() {
		result := metricconv.ResultSuccess
		if err != nil {
			result = metricconv.ResultError
			tel.RecordScrapeError(ctx, metricconv.ReferenceKind(kind), classifyScrapeErr(err))
		}
		tel.RecordScrape(ctx, metricconv.ReferenceKind(kind), time.Since(started), result, len(out))
		telemetry.EndSpan(span, err)
	}()

	if url == "" {
		return nil, fmt.Errorf("scrape: reference has empty url (kind %q)", kind)
	}
	ks, ok := s.byKind[kind]
	if !ok {
		return nil, fmt.Errorf("scrape: unsupported reference kind %q for url %q (want one of %s)",
			kind, url, strings.Join(v1alpha1.KnownReferenceKinds, ", "))
	}

	ctx, cancel := context.WithTimeout(ctx, s.opts.timeout())
	defer cancel()

	docs, err := ks.scrape(ctx, v1alpha1.PersonaReference{URL: url, Kind: kind})
	if err != nil {
		return nil, err
	}

	now := nowFunc()
	out = make([]rag.RawDoc, 0, len(docs))
	for i := range docs {
		d := docs[i]
		if strings.TrimSpace(d.Text) == "" {
			// A doc with no extractable text is useless to the embed stage;
			// drop it rather than store an empty exemplar.
			continue
		}
		d.SourceKind = kind
		if d.SourceURL == "" {
			d.SourceURL = url
		}
		if d.FetchedAt.IsZero() {
			d.FetchedAt = now
		}
		if d.Meta == nil {
			d.Meta = map[string]any{}
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("scrape: %s reference %q yielded no usable text", kind, url)
	}
	if s.maxDocs > 0 && len(out) > s.maxDocs {
		dropped := len(out) - s.maxDocs
		logDropped(ctx, kind, url, s.maxDocs, dropped)
		// §4.2 scrape.dropped — honour the fan-out cap with no silent truncation.
		tel.RecordScrapeDropped(ctx, metricconv.ReferenceKind(kind), metricconv.ReferenceFailReasonCapExceeded, dropped)
		out = out[:s.maxDocs]
	}
	return out, nil
}

// classifyScrapeErr buckets a scrape failure into the §4.2/§4.1 reason enum for
// the scrape.errors / reference.failed labels. Classification is best-effort by
// message shape — the reason is a low-cardinality diagnostic label, not load-
// bearing control flow — and defaults to network (the dominant transport case).
func classifyScrapeErr(err error) metricconv.ReferenceFailReason {
	if err == nil {
		return metricconv.ReferenceFailReasonNetwork
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no caption") || strings.Contains(msg, "caption track was empty"):
		return metricconv.ReferenceFailReasonNoCaptions
	case strings.Contains(msg, "no usable text") || strings.Contains(msg, "yielded no"):
		return metricconv.ReferenceFailReasonEmpty
	case strings.Contains(msg, "unsupported reference kind") ||
		strings.Contains(msg, "not yet implemented") ||
		strings.Contains(msg, "could not locate") ||
		strings.Contains(msg, "decode") ||
		strings.Contains(msg, "robots") ||
		strings.Contains(msg, "parse") ||
		strings.Contains(msg, "empty url"):
		return metricconv.ReferenceFailReasonParse
	default:
		return metricconv.ReferenceFailReasonNetwork
	}
}

// fetcher performs a single bounded HTTP GET with the configured User-Agent.
type fetcher struct {
	do        Doer
	userAgent string
}

// get issues a GET and returns the body (capped at maxBodyBytes) on a 2xx.
// Non-2xx and transport errors are returned as errors.
func (f *fetcher) get(ctx context.Context, url string) ([]byte, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("scrape: build request for %q: %w", url, err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept-Language", "en")
	resp, err := f.do.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("scrape: fetch %q: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp, fmt.Errorf("scrape: fetch %q: unexpected status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, resp, fmt.Errorf("scrape: read body of %q: %w", url, err)
	}
	return body, resp, nil
}

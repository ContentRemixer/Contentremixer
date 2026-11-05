package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

const testCommit = "3ae0a89b00000000000000000000000000000000"

// fixtureFS serves the testdata tree with a fixed commit so tests are
// hermetic (no git dependency); ReadFile is the real LocalFS implementation.
type fixtureFS struct {
	LocalFS
}

func (fixtureFS) Commit() (string, error) { return testCommit, nil }

func newAnalyzer(transcripts TranscriptFetcher, fetchURL URLFetcher) *Analyzer {
	return &Analyzer{
		FS:          fixtureFS{LocalFS{Root: "testdata"}},
		Transcripts: transcripts,
		FetchURL:    fetchURL,
	}
}

func run(t *testing.T, a *Analyzer, source, inputFormat string) pipeline.Sample {
	t.Helper()
	s, err := a.Analyze(context.Background(), pipeline.RunContext{
		Repo:         "ContentRemixer/Contentremixer",
		IssueNumber:  42,
		SourcePath:   source,
		InputFormat:  inputFormat,
		OutputFormat: "blog",
	})
	if err != nil {
		t.Fatalf("Analyze(%q, %q) unexpected error: %v", source, inputFormat, err)
	}
	return s
}

func wantError(t *testing.T, a *Analyzer, source, inputFormat, wantSub string) {
	t.Helper()
	_, err := a.Analyze(context.Background(), pipeline.RunContext{
		IssueNumber: 42,
		SourcePath:  source,
		InputFormat: inputFormat,
	})
	if err == nil {
		t.Fatalf("Analyze(%q, %q): expected error containing %q, got nil", source, inputFormat, wantSub)
	}
	if !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("Analyze(%q, %q): error %q does not contain %q", source, inputFormat, err.Error(), wantSub)
	}
}

// --- teleprompter (video sources) ---

func TestAnalyzeTeleprompterAuto(t *testing.T) {
	s := run(t, newAnalyzer(nil, nil), "content/videos/2026/demo-video/", FormatAuto)
	if s.DetectedInput != FormatTeleprompter {
		t.Fatalf("DetectedInput = %q, want teleprompter", s.DetectedInput)
	}
	if !strings.Contains(s.Body, "Welcome back to the show") {
		t.Fatalf("Body missing teleprompter script text: %q", s.Body)
	}
	if s.SourcePath != "content/videos/2026/demo-video" {
		t.Fatalf("SourcePath = %q, want normalized content/videos/2026/demo-video", s.SourcePath)
	}
	if s.SourceCommit != testCommit {
		t.Fatalf("SourceCommit = %q, want %q", s.SourceCommit, testCommit)
	}
	if s.Meta["title"] != "Demo video" || s.Meta["author"] != "henrik" {
		t.Fatalf("Meta not parsed: %+v", s.Meta)
	}
}

func TestAnalyzeTeleprompterExplicitMetaPath(t *testing.T) {
	s := run(t, newAnalyzer(nil, nil), "./content/videos/2026/demo-video/meta.yaml", FormatTeleprompter)
	if s.DetectedInput != FormatTeleprompter || !strings.Contains(s.Body, "OpenTelemetry") {
		t.Fatalf("explicit meta.yaml path not resolved: %+v", s)
	}
	if s.SourcePath != "content/videos/2026/demo-video" {
		t.Fatalf("SourcePath = %q, want the source folder", s.SourcePath)
	}
}

// --- blog ---

func TestAnalyzeBlogAuto(t *testing.T) {
	s := run(t, newAnalyzer(nil, nil), "content/blog/demo-post", FormatAuto)
	if s.DetectedInput != FormatBlog {
		t.Fatalf("DetectedInput = %q, want blog", s.DetectedInput)
	}
	if !strings.Contains(s.Body, "long-form article body") {
		t.Fatalf("Body missing files.post content: %q", s.Body)
	}
}

func TestAnalyzeStandaloneMarkdown(t *testing.T) {
	s := run(t, newAnalyzer(nil, nil), "content/videos/2026/loose/notes.md", FormatAuto)
	if s.DetectedInput != FormatBlog {
		t.Fatalf("DetectedInput = %q, want blog for standalone .md", s.DetectedInput)
	}
	if s.SourcePath != "content/videos/2026/loose/notes.md" {
		t.Fatalf("SourcePath = %q, want the file itself", s.SourcePath)
	}
	if len(s.Meta) != 0 {
		t.Fatalf("Meta = %+v, want empty for standalone file", s.Meta)
	}
}

// --- livestream / YouTube transcript ---

func TestAnalyzeLivestreamLocalTranscript(t *testing.T) {
	s := run(t, newAnalyzer(nil, nil), "content/livestreams/demo-stream", FormatAuto)
	if s.DetectedInput != FormatLivestream {
		t.Fatalf("DetectedInput = %q, want livestream", s.DetectedInput)
	}
	if !strings.Contains(s.Body, "Hello and welcome to the stream") {
		t.Fatalf("Body missing files.transcript content: %q", s.Body)
	}
}

func TestAnalyzeLivestreamViaFetcher(t *testing.T) {
	var gotURL string
	a := newAnalyzer(func(_ context.Context, ytURL string) (string, error) {
		gotURL = ytURL
		return "fetched transcript text", nil
	}, nil)
	s := run(t, a, "content/livestreams/fetch-stream", FormatAuto)
	if s.DetectedInput != FormatLivestream || s.Body != "fetched transcript text" {
		t.Fatalf("livestream fetch path broken: %+v", s)
	}
	if gotURL != "https://www.youtube.com/watch?v=fetchstream" {
		t.Fatalf("fetcher received %q, want the meta youtube_url", gotURL)
	}
}

func TestAnalyzeURLSourceYouTubeAuto(t *testing.T) {
	a := newAnalyzer(func(_ context.Context, ytURL string) (string, error) {
		return "transcript of " + ytURL, nil
	}, nil)
	s := run(t, a, "https://www.youtube.com/watch?v=abc123", FormatAuto)
	if s.DetectedInput != FormatLivestream {
		t.Fatalf("DetectedInput = %q, want livestream for YouTube URL", s.DetectedInput)
	}
	if !strings.Contains(s.Body, "transcript of https://www.youtube.com/watch?v=abc123") {
		t.Fatalf("Body = %q", s.Body)
	}
	if s.Meta["url"] != "https://www.youtube.com/watch?v=abc123" {
		t.Fatalf("Meta.url = %v", s.Meta["url"])
	}
}

func TestAnalyzeURLSourceShortLink(t *testing.T) {
	a := newAnalyzer(func(_ context.Context, _ string) (string, error) { return "t", nil }, nil)
	s := run(t, a, "https://youtu.be/abc123", "")
	if s.DetectedInput != FormatLivestream {
		t.Fatalf("DetectedInput = %q, want livestream for youtu.be URL", s.DetectedInput)
	}
}

func TestAnalyzeURLSourceArticleAuto(t *testing.T) {
	var gotURL string
	a := newAnalyzer(nil, func(_ context.Context, raw string) (string, error) {
		gotURL = raw
		return "article text", nil
	})
	s := run(t, a, "https://example.com/some/post", FormatAuto)
	if s.DetectedInput != FormatBlog || s.Body != "article text" {
		t.Fatalf("URL article path broken: %+v", s)
	}
	if gotURL != "https://example.com/some/post" {
		t.Fatalf("fetcher received %q", gotURL)
	}
}

// --- podcast ---

func TestAnalyzePodcastLocalTranscript(t *testing.T) {
	s := run(t, newAnalyzer(nil, nil), "content/podcasts/demo-episode", FormatAuto)
	if s.DetectedInput != FormatPodcast {
		t.Fatalf("DetectedInput = %q, want podcast", s.DetectedInput)
	}
	if !strings.Contains(s.Body, "platform engineering") {
		t.Fatalf("Body missing files.transcript content: %q", s.Body)
	}
}

func TestAnalyzePodcastViaURL(t *testing.T) {
	var gotURL string
	a := newAnalyzer(nil, func(_ context.Context, raw string) (string, error) {
		gotURL = raw
		return "episode page text", nil
	})
	s := run(t, a, "content/podcasts/url-episode", FormatPodcast)
	if s.Body != "episode page text" {
		t.Fatalf("Body = %q", s.Body)
	}
	if gotURL != "https://podcast.example.com/ep8" {
		t.Fatalf("fetcher received %q, want meta url", gotURL)
	}
}

// --- auto detection ---

func TestAutoDetectByMetaShapeOutsideContentTree(t *testing.T) {
	s := run(t, newAnalyzer(nil, nil), "drafts/outside-tree", FormatAuto)
	if s.DetectedInput != FormatBlog {
		t.Fatalf("DetectedInput = %q, want blog via meta-shape fallback (files.post)", s.DetectedInput)
	}
}

func TestAutoDetectLivestreamByMetaShape(t *testing.T) {
	a := newAnalyzer(func(_ context.Context, _ string) (string, error) { return "t", nil }, nil)
	s := run(t, a, "replays/2026/one", FormatAuto) // outside content/: youtube_url meta shape
	if s.DetectedInput != FormatLivestream {
		t.Fatalf("DetectedInput = %q, want livestream via meta-shape fallback (youtube_url)", s.DetectedInput)
	}
}

// --- route inputFormat override ---

func TestExplicitInputFormatOverridesDetection(t *testing.T) {
	var gotURL string
	a := newAnalyzer(func(_ context.Context, ytURL string) (string, error) {
		gotURL = ytURL
		return "transcript via override", nil
	}, nil)
	// A video source routed explicitly as livestream: transcript of its
	// youtube_url instead of the teleprompter script.
	s := run(t, a, "content/videos/2026/live-only", FormatLivestream)
	if s.DetectedInput != FormatLivestream || s.Body != "transcript via override" {
		t.Fatalf("override path broken: %+v", s)
	}
	if gotURL != "https://www.youtube.com/watch?v=liveonly" {
		t.Fatalf("fetcher received %q", gotURL)
	}
}

// --- fail-closed errors ---

func TestAnalyzeErrors(t *testing.T) {
	cases := []struct {
		name, source, input, wantSub string
	}{
		{"unknown inputFormat", "content/blog/demo-post", "video", "unsupported inputFormat"},
		{"empty source path", "", FormatAuto, "empty source path"},
		{"no meta.yaml", "content/videos/2026/does-not-exist", FormatAuto, "no meta.yaml found"},
		{"blog route on video source", "content/videos/2026/demo-video", FormatBlog, "files.post"},
		{"teleprompter missing file", "content/videos/2026/broken-video", FormatTeleprompter, "gone.md"},
		{"teleprompter from URL", "https://example.com/script.md", FormatTeleprompter, "teleprompter input requires a content/ path"},
		{"livestream no transcript no fetcher", "content/livestreams/fetch-stream", FormatLivestream, "not wired in Phase 1"},
		{"podcast url no fetcher", "content/podcasts/url-episode", FormatPodcast, "not wired in Phase 1"},
		{"undetectable", "drafts/empty", FormatAuto, "cannot detect input format"},
		{"escaping path", "../outside", FormatAuto, "invalid source path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantError(t, newAnalyzer(nil, nil), tc.source, tc.input, tc.wantSub)
		})
	}
}

func TestAnalyzeURLArticleWithoutFetcherFailsClosed(t *testing.T) {
	a := &Analyzer{FS: fixtureFS{LocalFS{Root: "testdata"}}}
	wantError(t, a, "https://example.com/x", FormatAuto, "not wired in Phase 1")
}

// --- path normalization + LocalFS guards ---

func TestNormalizeRepoPath(t *testing.T) {
	cases := map[string]string{
		"./content/blog/x/": "content/blog/x",
		"content//blog//x":  "content/blog/x",
		"/content/blog/x":   "content/blog/x",
		"content\\blog\\x":  "content/blog/x",
		"../escape":         "",
		"content/../escape": "",
	}
	for in, want := range cases {
		if got := normalizeRepoPath(in); got != want {
			t.Errorf("normalizeRepoPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocalFSReadAndGuards(t *testing.T) {
	root := "../../.."
	fs := LocalFS{Root: root}
	if _, err := fs.ReadFile("README.md"); err != nil {
		t.Fatalf("reading repo README.md: %v", err)
	}
	// Escaping segments are cleaned against the root, not rejected by name:
	// "../../etc/passwd" resolves to <root>/etc/passwd (inside the root,
	// missing here) — never outside the checkout.
	_, err := fs.ReadFile("../../etc/passwd")
	if err != nil && strings.Contains(err.Error(), "escapes the repository root") {
		t.Fatalf("cleaned path should resolve inside root, got: %v", err)
	}
	if _, err := fs.ReadFile("no-such-file-xyz.md"); err == nil {
		t.Fatalf("expected error for missing file")
	}
}

func TestLocalFSCommit(t *testing.T) {
	fs := LocalFS{Root: "../../.."}
	sha, err := fs.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		t.Fatalf("Commit() = %q, want a 40-hex SHA", sha)
	}
}

// --- sanity: fixture tree is what the tests assume ---

func TestFixtureTreeIntact(t *testing.T) {
	for _, f := range []string{
		"content/videos/2026/demo-video/meta.yaml",
		"content/livestreams/demo-stream/transcript.txt",
		"content/blog/demo-post/index.md",
		"content/podcasts/demo-episode/transcript.md",
	} {
		if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
			t.Fatalf("fixture missing: %s (%v)", f, err)
		}
	}
}

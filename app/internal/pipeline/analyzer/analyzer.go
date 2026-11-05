// Package analyzer implements pipeline subtask E (ISI-7080, design §3
// consumer 2 + §4): it resolves the recycle source (a content/ path in the
// reference repo or an external URL), picks the parser by inputFormat,
// parses the source meta.yaml, and emits the normalized Sample brief for
// the writer.
//
// Parser selection (design §3):
//
//	teleprompter → files.teleprompter from the source meta.yaml (script text)
//	livestream  → YouTube transcript of youtube_url; Phase 1 reads a
//	              pre-fetched files.transcript when present and otherwise
//	              calls the injectable TranscriptFetcher (design §10.5:
//	              transcript fetching may be stubbed/fixtured in Phase 1)
//	blog        → files.post markdown
//	podcast     → treated like article text: files.transcript, else files.post,
//	              else the url fetched via the injectable URLFetcher
//	auto        → detected from the source meta.yaml / content path (below)
//
// "auto" detection rule (design §3 consumer 2, rule lives here):
//  1. URL sources have no meta.yaml — a YouTube URL detects livestream,
//     any other http(s) URL detects blog.
//  2. Repo-path sources detect by content type directory first
//     (content/videos → teleprompter, content/livestreams → livestream,
//     content/blog → blog, content/podcasts → podcast), then — when the
//     path does not sit under a known content/<type>/ — by meta shape:
//     files.teleprompter → teleprompter, files.post → blog,
//     youtube_url → livestream, url → podcast.
//
// Anything undetectable or unreadable fails closed with a descriptive error.
package analyzer

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// Input formats (mirrors the ReferenceRepo LabelRoute.InputFormat CRD enum,
// design §2.2).
const (
	FormatAuto         = "auto"
	FormatTeleprompter = "teleprompter"
	FormatLivestream   = "livestream"
	FormatBlog         = "blog"
	FormatPodcast      = "podcast"
)

// validFormats is the closed Phase-1 input-format set.
var validFormats = map[string]bool{
	FormatAuto:         true,
	FormatTeleprompter: true,
	FormatLivestream:   true,
	FormatBlog:         true,
	FormatPodcast:      true,
}

// TranscriptFetcher fetches the transcript text of a YouTube URL. Phase 1
// ships no live scraper (design §10.5): runs either read a pre-fetched
// files.transcript from the source folder or inject a fetcher (fixtures in
// tests, a real scraper in Phase 2).
type TranscriptFetcher func(ctx context.Context, youtubeURL string) (string, error)

// URLFetcher fetches article text from an external URL (podcast episode
// pages and raw URL sources in Phase 1). Injectable for the same reasons as
// TranscriptFetcher.
type URLFetcher func(ctx context.Context, rawURL string) (string, error)

// Analyzer is the subtask-E implementation of pipeline.Analyzer.
var _ pipeline.Analyzer = (*Analyzer)(nil)

type Analyzer struct {
	// FS provides read access to the reference repo checkout. Required.
	FS SourceFS
	// Transcripts resolves YouTube transcripts when no files.transcript is
	// present. When nil, livestream sources without files.transcript fail
	// closed with a Phase-1 guidance error.
	Transcripts TranscriptFetcher
	// FetchURL resolves external article/podcast URL sources. When nil,
	// URL sources needing a fetch fail closed with a Phase-1 guidance error.
	FetchURL URLFetcher
}

// Analyze implements pipeline.Analyzer.
func (a *Analyzer) Analyze(ctx context.Context, rc pipeline.RunContext) (pipeline.Sample, error) {
	if a == nil || a.FS == nil {
		return pipeline.Sample{}, fmt.Errorf("analyzer: no source filesystem configured")
	}
	input := rc.InputFormat
	if input == "" {
		input = FormatAuto // CRD default (design §2.2)
	}
	if !validFormats[input] {
		return pipeline.Sample{}, fmt.Errorf("analyzer: unsupported inputFormat %q (want one of teleprompter, livestream, blog, podcast, auto)", rc.InputFormat)
	}
	src := strings.TrimSpace(rc.SourcePath)
	if src == "" {
		return pipeline.Sample{}, fmt.Errorf("analyzer: empty source path in run context (issue %d)", rc.IssueNumber)
	}

	if isURL(src) {
		return a.analyzeURL(ctx, input, src)
	}
	return a.analyzeRepoPath(ctx, input, src)
}

// ---- URL sources ----

func (a *Analyzer) analyzeURL(ctx context.Context, input, raw string) (pipeline.Sample, error) {
	detected := FormatBlog
	if isYouTubeURL(raw) {
		detected = FormatLivestream
	}
	format := concreteFormat(input, detected)

	var body string
	var err error
	switch format {
	case FormatLivestream:
		body, err = a.youTubeTranscript(ctx, raw)
	case FormatBlog, FormatPodcast:
		if a.FetchURL == nil {
			return pipeline.Sample{}, fmt.Errorf("analyzer: fetching URL source %q is not wired in Phase 1; move the source under content/ or inject a URLFetcher", raw)
		}
		body, err = a.FetchURL(ctx, raw)
	case FormatTeleprompter:
		return pipeline.Sample{}, fmt.Errorf("analyzer: teleprompter input requires a content/ path with files.teleprompter in meta.yaml; got URL %q", raw)
	}
	if err != nil {
		return pipeline.Sample{}, err
	}

	commit, err := a.FS.Commit()
	if err != nil {
		return pipeline.Sample{}, fmt.Errorf("analyzer: URL source %q: %w", raw, err)
	}
	return pipeline.Sample{
		SourcePath:    raw,
		SourceCommit:  commit,
		Meta:          map[string]any{"url": raw},
		DetectedInput: format,
		Body:          body,
	}, nil
}

// ---- repo-path sources ----

func (a *Analyzer) analyzeRepoPath(ctx context.Context, input, raw string) (pipeline.Sample, error) {
	clean := normalizeRepoPath(raw)
	if clean == "" {
		return pipeline.Sample{}, fmt.Errorf("analyzer: invalid source path %q", raw)
	}

	// Resolution, in order:
	//  1. "<dir>/meta.yaml" given explicitly → source folder is its dir;
	//  2. a readable markdown file → standalone article source (no meta);
	//  3. a folder containing meta.yaml → the normal item source;
	//  4. anything else fails closed.
	itemDir := clean
	standalone := ""
	if path.Base(clean) == "meta.yaml" {
		itemDir = path.Dir(clean)
		if !fileExists(a.FS, path.Join(itemDir, "meta.yaml")) {
			return pipeline.Sample{}, fmt.Errorf("analyzer: source %q: meta.yaml not found (after normalizing to %q)", raw, itemDir)
		}
	} else if !fileExists(a.FS, path.Join(clean, "meta.yaml")) {
		if strings.HasSuffix(clean, ".md") && fileExists(a.FS, clean) {
			standalone = clean
			itemDir = path.Dir(clean)
		} else {
			return pipeline.Sample{}, fmt.Errorf("analyzer: source %q: no meta.yaml found and not a readable markdown file", clean)
		}
	}

	meta := map[string]any{}
	if standalone == "" {
		metaBytes, err := a.FS.ReadFile(path.Join(itemDir, "meta.yaml"))
		if err != nil {
			return pipeline.Sample{}, fmt.Errorf("analyzer: source %q: reading meta.yaml: %w", itemDir, err)
		}
		meta, err = parseMeta(metaBytes)
		if err != nil {
			return pipeline.Sample{}, fmt.Errorf("analyzer: source %q: parsing meta.yaml: %w", itemDir, err)
		}
	}

	detected := FormatBlog // a standalone .md is a bare article
	if standalone == "" {
		detected = detectInput(clean, meta)
		if detected == "" {
			return pipeline.Sample{}, fmt.Errorf("analyzer: source %q: cannot detect input format from path or meta.yaml; set an explicit inputFormat on the label route", clean)
		}
	}
	format := concreteFormat(input, detected)

	var body string
	var err error
	switch format {
	case FormatTeleprompter:
		body, err = a.fileFromBody(itemDir, meta, "teleprompter")
	case FormatBlog:
		if standalone != "" {
			body, err = a.readText(standalone)
		} else {
			body, err = a.fileFromBody(itemDir, meta, "post")
		}
	case FormatLivestream:
		body, err = a.livestreamBody(ctx, itemDir, meta)
	case FormatPodcast:
		body, err = a.podcastBody(ctx, itemDir, meta)
	}
	if err != nil {
		return pipeline.Sample{}, err
	}

	commit, err := a.FS.Commit()
	if err != nil {
		return pipeline.Sample{}, err
	}
	sourceRef := itemDir
	if standalone != "" {
		sourceRef = standalone
	}
	return pipeline.Sample{
		SourcePath:    sourceRef,
		SourceCommit:  commit,
		Meta:          meta,
		DetectedInput: format,
		Body:          body,
	}, nil
}

// livestreamBody resolves the transcript: a declared files.transcript is
// read (and must exist); otherwise youtube_url goes through the
// TranscriptFetcher.
func (a *Analyzer) livestreamBody(ctx context.Context, itemDir string, meta map[string]any) (string, error) {
	if metaFile(meta, "transcript") != "" {
		return a.fileFromBody(itemDir, meta, "transcript")
	}
	ytURL := metaString(meta, "youtube_url")
	if ytURL == "" {
		return "", fmt.Errorf("analyzer: source %q: livestream has no files.transcript and no youtube_url in meta.yaml", itemDir)
	}
	return a.youTubeTranscript(ctx, ytURL)
}

// podcastBody treats the podcast like article text: a declared
// files.transcript or files.post is read (and must exist); otherwise the
// url goes through the URLFetcher.
func (a *Analyzer) podcastBody(ctx context.Context, itemDir string, meta map[string]any) (string, error) {
	for _, key := range []string{"transcript", "post"} {
		if metaFile(meta, key) != "" {
			return a.fileFromBody(itemDir, meta, key)
		}
	}
	epURL := metaString(meta, "url")
	if epURL == "" {
		return "", fmt.Errorf("analyzer: source %q: podcast has no files.transcript, files.post, or url in meta.yaml", itemDir)
	}
	if a.FetchURL == nil {
		return "", fmt.Errorf("analyzer: fetching podcast url %q is not wired in Phase 1; add files.transcript to the source folder or inject a URLFetcher", epURL)
	}
	return a.FetchURL(ctx, epURL)
}

func (a *Analyzer) youTubeTranscript(ctx context.Context, ytURL string) (string, error) {
	if a.Transcripts == nil {
		return "", fmt.Errorf("analyzer: YouTube transcript fetch for %q is not wired in Phase 1 (design §10.5); add files.transcript to the source folder or inject a TranscriptFetcher", ytURL)
	}
	return a.Transcripts(ctx, ytURL)
}

// fileFromBody reads meta.files.<key> and returns that file's text. A
// declared file that cannot be read fails closed.
func (a *Analyzer) fileFromBody(itemDir string, meta map[string]any, key string) (string, error) {
	name := metaFile(meta, key)
	if name == "" {
		return "", fmt.Errorf("analyzer: source %q: meta.yaml has no files.%s", itemDir, key)
	}
	body, err := a.readText(path.Join(itemDir, name))
	if err != nil {
		return "", fmt.Errorf("analyzer: source %q: reading files.%s %q: %w", itemDir, key, name, err)
	}
	return body, nil
}

func (a *Analyzer) readText(name string) (string, error) {
	b, err := a.FS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("reading %q: %w", name, err)
	}
	return string(b), nil
}

// ---- helpers ----

// concreteFormat turns the route inputFormat into the concrete format:
// explicit route inputs override detection; "auto" (or empty) takes the
// detected format.
func concreteFormat(input, detected string) string {
	if input != FormatAuto && input != "" {
		return input
	}
	return detected
}

// detectInput implements the documented "auto" detection rule for
// repo-path sources: content/<type>/ directory first, then meta shape.
func detectInput(srcPath string, meta map[string]any) string {
	byDir := map[string]string{
		"videos":      FormatTeleprompter,
		"livestreams": FormatLivestream,
		"blog":        FormatBlog,
		"podcasts":    FormatPodcast,
	}
	segs := strings.Split(strings.Trim(srcPath, "/"), "/")
	for i, seg := range segs {
		if seg == "content" && i+1 < len(segs) {
			if f, ok := byDir[segs[i+1]]; ok {
				return f
			}
		}
	}
	if f, ok := byDir[segs[0]]; ok {
		return f
	}
	// Meta-shape fallback for sources outside the known content dirs.
	if metaFile(meta, "teleprompter") != "" {
		return FormatTeleprompter
	}
	if metaFile(meta, "post") != "" {
		return FormatBlog
	}
	if metaString(meta, "youtube_url") != "" {
		return FormatLivestream
	}
	if metaString(meta, "url") != "" {
		return FormatPodcast
	}
	return ""
}

func isURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func isYouTubeURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "youtu.be" || host == "youtube.com" || strings.HasSuffix(host, ".youtube.com")
}

// normalizeRepoPath canonicalizes a repo-relative source path: forward
// slashes, no leading "./" or "/", no trailing slash, no ".." segments
// (returns "" for paths that would escape the repo root).
func normalizeRepoPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	segs := make([]string, 0, 8)
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return ""
		default:
			segs = append(segs, seg)
		}
	}
	return strings.Join(segs, "/")
}

func fileExists(fs SourceFS, name string) bool {
	_, err := fs.ReadFile(name)
	return err == nil
}

// parseMeta parses a meta.yaml document into a generic map.
func parseMeta(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// metaFile returns meta.files.<key> as a non-empty file name, or "".
func metaFile(meta map[string]any, key string) string {
	files, ok := meta["files"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := files[key].(string)
	return strings.TrimSpace(name)
}

// metaString returns meta.<key> as a non-empty string, or "".
func metaString(meta map[string]any, key string) string {
	s, _ := meta[key].(string)
	return strings.TrimSpace(s)
}

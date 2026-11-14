package scrape

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// talkRecordingScraper resolves a recorded-talk reference to transcript text.
//
// v1 scope (plan §10 decision 2, board-approved): accept a URL that already has
// captions/a transcript and REUSE the YouTube transcript path; raw-audio ASR
// (Whisper-class) is deferred to a fast-follow. Concretely:
//
//   - A YouTube URL delegates to the youtube scraper (caption track → transcript).
//   - Any other URL is fetched and parsed as a caption/transcript document:
//     WebVTT, SubRip (SRT), YouTube timedtext XML, or plain text.
//
// This keeps raw media out of scope while covering the common "my talk is on
// YouTube" and "here is the .vtt/.srt/transcript" cases DevRels actually have.
type talkRecordingScraper struct {
	fetch   *fetcher
	robots  *robotsCache
	youtube *youtubeScraper
}

var _ kindScraper = (*talkRecordingScraper)(nil)

func (t *talkRecordingScraper) scrape(ctx context.Context, ref v1alpha1.PersonaReference) ([]rag.RawDoc, error) {
	// Reuse the YouTube transcript path when the recording lives on YouTube
	// (youTubeVideoID returns an error for non-YouTube URLs).
	if _, err := youTubeVideoID(ref.URL); err == nil {
		return t.youtube.scrape(ctx, ref)
	}

	if err := t.robots.allowed(ctx, ref.URL, t.fetch.userAgent); err != nil {
		return nil, err
	}
	body, _, err := t.fetch.get(ctx, ref.URL)
	if err != nil {
		return nil, err
	}
	text, err := parseTranscriptDocument(body)
	if err != nil {
		return nil, fmt.Errorf("scrape: talk-recording %q: %w", ref.URL, err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("scrape: talk-recording %q: transcript was empty (no captions in document)", ref.URL)
	}
	return []rag.RawDoc{{
		SourceURL: ref.URL,
		Title:     transcriptTitle(ref.URL),
		Text:      text,
		Meta: map[string]any{
			"transcriptSource":  "captions-url",
			"originalReference": ref.URL,
		},
	}}, nil
}

// tagRe strips caption-markup tags (<c>, <i>, <00:00:01.000> karaoke timings,
// <v Speaker>) that appear inside WebVTT/SRT cue bodies.
var tagRe = regexp.MustCompile(`<[^>]*>`)

// cueTimingRe matches a WebVTT/SRT cue-timing line (contains `-->`).
var cueTimingRe = regexp.MustCompile(`-->`)

// parseTranscriptDocument normalizes a caption/transcript document into plain
// transcript text. It auto-detects WebVTT, SubRip (SRT), YouTube timedtext XML,
// and falls back to treating the body as plain text.
func parseTranscriptDocument(body []byte) (string, error) {
	trimmed := strings.TrimSpace(string(body))
	switch {
	case strings.HasPrefix(trimmed, "WEBVTT"):
		return parseCueText(trimmed), nil
	case looksLikeTimedText(trimmed):
		// Reuse the YouTube timedtext decoder (<transcript><text>…).
		return transcriptText(body)
	case looksLikeSRT(trimmed):
		return parseCueText(trimmed), nil
	default:
		// Plain-text transcript: keep paragraph structure, collapse whitespace.
		return normalizeBlocks(trimmed), nil
	}
}

func looksLikeTimedText(s string) bool {
	return strings.Contains(s, "<transcript") || strings.Contains(s, "<text ") || strings.Contains(s, "<tt ")
}

// looksLikeSRT detects the SubRip shape: an integer cue index line followed by a
// `hh:mm:ss,mmm --> hh:mm:ss,mmm` timing line.
var srtHeadRe = regexp.MustCompile(`(?m)^\s*\d+\s*$\n\s*\d{2}:\d{2}:\d{2}[,.]\d{3}\s*-->`)

func looksLikeSRT(s string) bool { return srtHeadRe.MatchString(s) }

// parseCueText linearizes a cue-based caption document (WebVTT or SRT): it drops
// the format header, cue-timing lines, numeric cue indices, NOTE/STYLE/REGION
// blocks and markup tags, de-duplicates consecutive repeated lines (common in
// rolling auto-captions), and joins the remaining cue text into one transcript.
func parseCueText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	var last string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || ln == "WEBVTT" {
			continue
		}
		if cueTimingRe.MatchString(ln) {
			continue
		}
		// Skip block headers and pure numeric cue indices.
		if strings.HasPrefix(ln, "NOTE") || strings.HasPrefix(ln, "STYLE") || strings.HasPrefix(ln, "REGION") {
			continue
		}
		if isAllDigits(ln) {
			continue
		}
		ln = collapseSpaces(tagRe.ReplaceAllString(ln, ""))
		if ln == "" || ln == last {
			continue
		}
		last = ln
		out = append(out, ln)
	}
	return collapseSpaces(strings.Join(out, " "))
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// transcriptTitle derives a readable title from a transcript URL's filename.
func transcriptTitle(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	base = strings.TrimSuffix(base, path.Ext(base))
	base = strings.NewReplacer("-", " ", "_", " ").Replace(base)
	return collapseSpaces(base)
}

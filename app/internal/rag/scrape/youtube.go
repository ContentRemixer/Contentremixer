package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// youtubeScraper fetches a YouTube video's caption track and normalizes it to
// transcript text (design §3.1: "captions only; no ASR; caption-less videos
// are skipped"). It fetches the public watch page, extracts the caption-track
// list from the embedded ytInitialPlayerResponse, picks the best track
// (preferring a non-auto-generated English track), fetches that track's
// timedtext XML, and joins the cue text.
type youtubeScraper struct {
	fetch *fetcher
}

var _ kindScraper = (*youtubeScraper)(nil)

// playerResponseRe pulls the ytInitialPlayerResponse JSON object out of the
// watch-page HTML. YouTube embeds it as `var ytInitialPlayerResponse = {...};`.
var playerResponseRe = regexp.MustCompile(`ytInitialPlayerResponse\s*=\s*(\{.*?\})\s*;`)

func (y *youtubeScraper) scrape(ctx context.Context, ref v1alpha1.PersonaReference) ([]rag.RawDoc, error) {
	videoID, err := youTubeVideoID(ref.URL)
	if err != nil {
		return nil, err
	}
	watchURL := "https://www.youtube.com/watch?v=" + videoID

	page, _, err := y.fetch.get(ctx, watchURL)
	if err != nil {
		return nil, err
	}
	pr, err := parsePlayerResponse(page)
	if err != nil {
		return nil, fmt.Errorf("scrape: youtube %q: %w", ref.URL, err)
	}

	track, ok := pickCaptionTrack(pr.Captions.Renderer.CaptionTracks)
	if !ok {
		// Caption-less videos are skipped (design §3.1) — surface as an error
		// the controller records per-reference without failing the persona.
		return nil, fmt.Errorf("scrape: youtube %q: no caption track available (video %s)", ref.URL, videoID)
	}

	// The caption-track baseUrl extracted from the player response no longer
	// serves a body when fetched bare — YouTube now returns an empty 200 unless
	// the request names an explicit caption format, which surfaced downstream as
	// `decode caption xml: EOF` (ISI-7460). Request the stable json3 format; it
	// reliably returns a non-empty body and is less brittle than the legacy XML.
	capBody, _, err := y.fetch.get(ctx, appendCaptionFormat(track.BaseURL))
	if err != nil {
		return nil, err
	}
	text, err := transcriptText(capBody)
	if err != nil {
		return nil, fmt.Errorf("scrape: youtube %q: %w", ref.URL, err)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("scrape: youtube %q: caption track was empty (video %s)", ref.URL, videoID)
	}

	title := strings.TrimSpace(pr.VideoDetails.Title)
	doc := rag.RawDoc{
		SourceURL: watchURL,
		Title:     title,
		Text:      text,
		Meta: map[string]any{
			"videoId":           videoID,
			"captionLanguage":   track.LanguageCode,
			"captionAutoGen":    track.Kind == "asr",
			"author":            strings.TrimSpace(pr.VideoDetails.Author),
			"lengthSeconds":     pr.VideoDetails.LengthSeconds,
			"originalReference": ref.URL,
		},
	}
	return []rag.RawDoc{doc}, nil
}

// --- player-response + caption parsing ---

type playerResponse struct {
	Captions struct {
		Renderer struct {
			CaptionTracks []captionTrack `json:"captionTracks"`
		} `json:"playerCaptionsTracklistRenderer"`
	} `json:"captions"`
	VideoDetails struct {
		Title         string `json:"title"`
		Author        string `json:"author"`
		LengthSeconds string `json:"lengthSeconds"`
	} `json:"videoDetails"`
}

type captionTrack struct {
	BaseURL      string `json:"baseUrl"`
	LanguageCode string `json:"languageCode"`
	// Kind is "asr" for auto-generated tracks, empty for human/uploaded ones.
	Kind string `json:"kind"`
}

func parsePlayerResponse(page []byte) (*playerResponse, error) {
	m := playerResponseRe.FindSubmatch(page)
	if m == nil {
		return nil, fmt.Errorf("could not locate ytInitialPlayerResponse (watch page shape changed or video unavailable)")
	}
	var pr playerResponse
	if err := json.Unmarshal(m[1], &pr); err != nil {
		return nil, fmt.Errorf("decode player response: %w", err)
	}
	return &pr, nil
}

// pickCaptionTrack prefers a human-authored English track, then any human
// track, then an auto-generated English track, then the first track.
func pickCaptionTrack(tracks []captionTrack) (captionTrack, bool) {
	if len(tracks) == 0 {
		return captionTrack{}, false
	}
	isEN := func(t captionTrack) bool {
		return strings.HasPrefix(strings.ToLower(t.LanguageCode), "en")
	}
	human := func(t captionTrack) bool { return t.Kind != "asr" }

	var firstHuman, firstEN, firstENHuman *captionTrack
	for i := range tracks {
		t := &tracks[i]
		if t.BaseURL == "" {
			continue
		}
		if human(*t) && isEN(*t) && firstENHuman == nil {
			firstENHuman = t
		}
		if human(*t) && firstHuman == nil {
			firstHuman = t
		}
		if isEN(*t) && firstEN == nil {
			firstEN = t
		}
	}
	switch {
	case firstENHuman != nil:
		return *firstENHuman, true
	case firstHuman != nil:
		return *firstHuman, true
	case firstEN != nil:
		return *firstEN, true
	}
	for i := range tracks {
		if tracks[i].BaseURL != "" {
			return tracks[i], true
		}
	}
	return captionTrack{}, false
}

// appendCaptionFormat forces an explicit caption format on the track baseUrl.
// A bare baseUrl (no fmt) now yields an empty body (ISI-7460); json3 is the
// format yt-dlp and the ecosystem settled on as the stable, non-empty one. If
// the URL already carries a fmt (some tracks do), we leave it untouched. We
// append the raw param rather than re-encoding via url.Values so signed query
// params (signature/expire/sparams) keep their original order.
func appendCaptionFormat(baseURL string) string {
	if baseURL == "" {
		return baseURL
	}
	if u, err := url.Parse(baseURL); err == nil && u.Query().Get("fmt") != "" {
		return baseURL
	}
	sep := "&"
	if !strings.Contains(baseURL, "?") {
		sep = "?"
	}
	return baseURL + sep + "fmt=json3"
}

// json3Transcript is the YouTube json3 caption shape: a list of timed events,
// each with text segments. Window/append events carry no segs and are skipped.
// Segment text is already Unicode-decoded (unlike the legacy XML), so it is NOT
// HTML-unescaped — only intra-cue newlines are flattened.
type json3Transcript struct {
	Events []struct {
		Segs []struct {
			Utf8 string `json:"utf8"`
		} `json:"segs"`
	} `json:"events"`
}

// timedtext is the legacy YouTube caption XML shape:
// <transcript><text ...>cue</text>…. Retained as a fallback for any track that
// still serves XML.
type timedtext struct {
	Texts []struct {
		Body string `xml:",chardata"`
	} `xml:"text"`
}

// transcriptText decodes a caption body (json3 or legacy XML) into a single
// normalized line, cues joined by spaces. The format is detected by the first
// non-space byte: '{' → json3, else XML. An empty body is reported explicitly
// rather than as an opaque decode EOF (ISI-7460).
func transcriptText(capBody []byte) (string, error) {
	trimmed := bytes.TrimSpace(capBody)
	if len(trimmed) == 0 {
		return "", fmt.Errorf("caption track returned an empty body (video unavailable or format unsupported)")
	}
	if trimmed[0] == '{' {
		return transcriptTextJSON3(trimmed)
	}
	return transcriptTextXML(capBody)
}

func transcriptTextJSON3(capJSON []byte) (string, error) {
	var tt json3Transcript
	if err := json.Unmarshal(capJSON, &tt); err != nil {
		return "", fmt.Errorf("decode caption json: %w", err)
	}
	parts := make([]string, 0, len(tt.Events))
	for _, e := range tt.Events {
		for _, seg := range e.Segs {
			s := strings.TrimSpace(strings.ReplaceAll(seg.Utf8, "\n", " "))
			if s != "" {
				parts = append(parts, s)
			}
		}
	}
	return collapseSpaces(strings.Join(parts, " ")), nil
}

func transcriptTextXML(capXML []byte) (string, error) {
	var tt timedtext
	if err := xml.Unmarshal(capXML, &tt); err != nil {
		return "", fmt.Errorf("decode caption xml: %w", err)
	}
	parts := make([]string, 0, len(tt.Texts))
	for _, t := range tt.Texts {
		s := html.UnescapeString(t.Body)
		s = strings.ReplaceAll(s, "\n", " ")
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	return collapseSpaces(strings.Join(parts, " ")), nil
}

// youTubeVideoID extracts the 11-char video id from the common YouTube URL
// shapes: watch?v=, youtu.be/<id>, /embed/<id>, /shorts/<id>, /live/<id>.
func youTubeVideoID(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("scrape: youtube: parse url %q: %w", raw, err)
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	switch {
	case host == "youtu.be":
		if id := strings.Trim(u.Path, "/"); id != "" {
			return firstPathSeg(id), nil
		}
	case host == "youtube.com" || host == "m.youtube.com" || strings.HasSuffix(host, ".youtube.com"):
		if v := u.Query().Get("v"); v != "" {
			return v, nil
		}
		for _, prefix := range []string{"/embed/", "/shorts/", "/live/", "/v/"} {
			if strings.HasPrefix(u.Path, prefix) {
				return firstPathSeg(strings.TrimPrefix(u.Path, prefix)), nil
			}
		}
	}
	return "", fmt.Errorf("scrape: youtube: could not extract video id from %q", raw)
}

func firstPathSeg(s string) string {
	if i := strings.IndexAny(s, "/?&"); i >= 0 {
		return s[:i]
	}
	return s
}

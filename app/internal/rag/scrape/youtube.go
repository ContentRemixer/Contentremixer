package scrape

import (
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

	capXML, _, err := y.fetch.get(ctx, track.BaseURL)
	if err != nil {
		return nil, err
	}
	text, err := transcriptText(capXML)
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

// timedtext is the YouTube caption XML shape: <transcript><text ...>cue</text>…
type timedtext struct {
	Texts []struct {
		Body string `xml:",chardata"`
	} `xml:"text"`
}

// transcriptText decodes the timedtext XML into a single normalized paragraph
// per cue, joined by spaces. Caption cues arrive HTML-escaped (and may contain
// &amp;#39; style double-escaping); we unescape once, then collapse whitespace.
func transcriptText(capXML []byte) (string, error) {
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

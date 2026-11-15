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
// are skipped").
//
// It resolves the video's player response (which lists the caption tracks) from
// one of several sources, tried in order (ISI-7467):
//
//	innertube-android → POST youtubei/v1/player with an ANDROID client context
//	innertube-ios     → the same with an IOS client context
//	watch-page        → GET the watch page, extract ytInitialPlayerResponse
//
// The InnerTube mobile clients come first because YouTube made the WEB
// watch-page caption baseUrls proof-of-origin (PoToken) gated in mid-2025: a
// bare watch-page baseUrl now returns HTTP 200 with an empty body, which
// surfaced as `decode caption xml: EOF` (ISI-7460) and then, once json3 was
// requested, as "caption track returned an empty body" (ISI-7425). The ANDROID/
// IOS InnerTube clients' signed caption baseUrls still serve a body server-side
// without a PoToken, so they are the reliable path. The watch-page source is
// kept as a last resort. From the chosen source we pick the best track
// (preferring a non-auto-generated English track), fetch that track's json3
// (falling back to legacy XML) body, and join the cue text. The first source
// that yields a non-empty transcript wins.
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

	// Try each player-response source in order; the first that yields a non-empty
	// transcript wins. We fall through on a fetch error, a missing track, or an
	// empty caption body (the PoToken symptom on the watch-page source). lastErr
	// carries the final source's failure: for a genuinely caption-less video the
	// last source leaves the distinct "no caption track" error the controller
	// skips per design §3.1, not an opaque transport error.
	var (
		chosen  *playerResponse
		track   captionTrack
		text    string
		lastErr error
	)
	for _, src := range y.playerSources() {
		pr, err := src.fetch(ctx, videoID)
		if err != nil {
			lastErr = fmt.Errorf("scrape: youtube %q (%s): %w", ref.URL, src.name, err)
			continue
		}
		t, ok := pickCaptionTrack(pr.Captions.Renderer.CaptionTracks)
		if !ok {
			lastErr = fmt.Errorf("scrape: youtube %q: no caption track available (video %s)", ref.URL, videoID)
			continue
		}
		// Request the stable json3 format: a bare baseUrl returns an empty body
		// (ISI-7460), and transcriptText falls back to legacy XML if a track
		// still serves it. An empty body here means this source's baseUrl is
		// PoToken-gated, so we fall through to the next source.
		capBody, _, err := y.fetch.get(ctx, appendCaptionFormat(t.BaseURL))
		if err != nil {
			lastErr = fmt.Errorf("scrape: youtube %q (%s): %w", ref.URL, src.name, err)
			continue
		}
		txt, err := transcriptText(capBody)
		if err != nil {
			lastErr = fmt.Errorf("scrape: youtube %q (%s): %w", ref.URL, src.name, err)
			continue
		}
		if strings.TrimSpace(txt) == "" {
			lastErr = fmt.Errorf("scrape: youtube %q (%s): caption track was empty (video %s)", ref.URL, src.name, videoID)
			continue
		}
		chosen, track, text = pr, t, txt
		break
	}
	if text == "" {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("scrape: youtube %q: no player source returned captions (video %s)", ref.URL, videoID)
	}

	title := strings.TrimSpace(chosen.VideoDetails.Title)
	doc := rag.RawDoc{
		SourceURL: watchURL,
		Title:     title,
		Text:      text,
		Meta: map[string]any{
			"videoId":           videoID,
			"captionLanguage":   track.LanguageCode,
			"captionAutoGen":    track.Kind == "asr",
			"author":            strings.TrimSpace(chosen.VideoDetails.Author),
			"lengthSeconds":     chosen.VideoDetails.LengthSeconds,
			"originalReference": ref.URL,
		},
	}
	return []rag.RawDoc{doc}, nil
}

// --- player-response sources (ISI-7467) ---

// innertubePlayerURL is the InnerTube v1 player endpoint. We POST a mobile
// client context here; the ANDROID/IOS clients' signed caption baseUrls still
// serve a body without a PoToken, unlike the WEB watch-page baseUrls.
const innertubePlayerURL = "https://www.youtube.com/youtubei/v1/player?prettyPrint=false"

// Mobile InnerTube client identities. The versions mirror a recent official app
// build; they only need to name a client YouTube still serves captions to
// server-side. The matching app User-Agent is sent so the request reads as the
// mobile app, not a generic bot.
const (
	androidUserAgent = "com.google.android.youtube/20.10.38 (Linux; U; Android 11) gzip"
	iosUserAgent     = "com.google.ios.youtube/20.10.4 (iPhone16,2; U; CPU iOS 18_0 like Mac OS X)"
)

// playerSource is one way to obtain a video's player response (caption-track
// list + video details). Sources are tried in order so a single blocked/gated
// endpoint does not take the whole scrape down.
type playerSource struct {
	name  string
	fetch func(ctx context.Context, videoID string) (*playerResponse, error)
}

func (y *youtubeScraper) playerSources() []playerSource {
	return []playerSource{
		{"innertube-android", func(ctx context.Context, id string) (*playerResponse, error) {
			return y.innertube(ctx, id, androidUserAgent, innertubeClient{
				ClientName: "ANDROID", ClientVersion: "20.10.38", AndroidSDKVersion: 30, HL: "en", GL: "US",
			})
		}},
		{"innertube-ios", func(ctx context.Context, id string) (*playerResponse, error) {
			return y.innertube(ctx, id, iosUserAgent, innertubeClient{
				ClientName: "IOS", ClientVersion: "20.10.4", DeviceModel: "iPhone16,2", HL: "en", GL: "US",
			})
		}},
		{"watch-page", y.watchPagePlayer},
	}
}

// innertubeClient is the InnerTube client identity sent in the player request
// context.client. androidSdkVersion/deviceModel are client-specific and omitted
// when zero so each client sends only the fields it needs.
type innertubeClient struct {
	ClientName        string `json:"clientName"`
	ClientVersion     string `json:"clientVersion"`
	AndroidSDKVersion int    `json:"androidSdkVersion,omitempty"`
	DeviceModel       string `json:"deviceModel,omitempty"`
	HL                string `json:"hl"`
	GL                string `json:"gl"`
}

type innertubeRequest struct {
	Context struct {
		Client innertubeClient `json:"client"`
	} `json:"context"`
	VideoID string `json:"videoId"`
}

// innertube POSTs the player request for one client and decodes the response.
// The videoID is marshalled as JSON (not concatenated) so an odd id cannot break
// or inject into the body.
func (y *youtubeScraper) innertube(ctx context.Context, videoID, userAgent string, client innertubeClient) (*playerResponse, error) {
	var reqBody innertubeRequest
	reqBody.Context.Client = client
	reqBody.VideoID = videoID
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("encode innertube request: %w", err)
	}
	raw, _, err := y.fetch.postJSON(ctx, innertubePlayerURL, body, userAgent)
	if err != nil {
		return nil, err
	}
	var pr playerResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, fmt.Errorf("decode innertube player response: %w", err)
	}
	return &pr, nil
}

// watchPagePlayer fetches the public watch page and extracts the embedded
// ytInitialPlayerResponse. Last-resort source: its caption baseUrls are now
// PoToken-gated and usually return an empty body (ISI-7467).
func (y *youtubeScraper) watchPagePlayer(ctx context.Context, videoID string) (*playerResponse, error) {
	page, _, err := y.fetch.get(ctx, "https://www.youtube.com/watch?v="+videoID)
	if err != nil {
		return nil, err
	}
	return parsePlayerResponse(page)
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

// timedtext covers both YouTube caption XML shapes in one decode:
//
//	legacy srv1: <transcript><text start dur>cue</text>…  → Texts
//	srv3:        <timedtext format="3"><body><p><s>word</s>…</p>…  → Paras
//
// srv3 is what the InnerTube ANDROID/IOS player's signed baseUrls actually
// return (ISI-7467): that endpoint ignores the requested fmt (json3/srv1/srv3
// all come back as srv3), so a parser that only read <text> would extract an
// empty transcript from every live caption body. A srv3 <p> carries its words
// in <s> segments (leading spaces included) and some cues carry their text as
// <p> chardata directly, so we handle both.
type timedtext struct {
	Texts []struct {
		Body string `xml:",chardata"`
	} `xml:"text"`
	Paras []struct {
		Chardata string `xml:",chardata"`
		Segs     []struct {
			Text string `xml:",chardata"`
		} `xml:"s"`
	} `xml:"body>p"`
}

// transcriptText decodes a caption body (json3 or XML) into a single normalized
// line, cues joined by spaces. The format is detected by the first non-space
// byte: '{' → json3, else XML (legacy srv1 or srv3). An empty body is reported
// explicitly rather than as an opaque decode EOF (ISI-7460).
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
	parts := make([]string, 0, len(tt.Texts)+len(tt.Paras))
	// Legacy srv1 <text> cues.
	for _, t := range tt.Texts {
		if s := cleanCue(t.Body); s != "" {
			parts = append(parts, s)
		}
	}
	// srv3 <p> cues: join the word-level <s> segments (spaces are carried in the
	// segment text, so no separator), falling back to the <p> chardata when a cue
	// has no segments.
	for _, p := range tt.Paras {
		cue := p.Chardata
		if len(p.Segs) > 0 {
			var b strings.Builder
			for _, sg := range p.Segs {
				b.WriteString(sg.Text)
			}
			cue = b.String()
		}
		if s := cleanCue(cue); s != "" {
			parts = append(parts, s)
		}
	}
	return collapseSpaces(strings.Join(parts, " ")), nil
}

// cleanCue normalizes one caption cue: resolve HTML entities (YouTube XML is
// often double-escaped, e.g. &amp;#39; → '), flatten intra-cue newlines, trim.
func cleanCue(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
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

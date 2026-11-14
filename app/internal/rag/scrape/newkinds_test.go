package scrape

import (
	"context"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// --- teleprompter-script: direct inline ingest, no fetch ---

func TestScrapeTeleprompterInline(t *testing.T) {
	fixedClock(t)
	script := "Opening Hook: why observability matters\n\n\nToday we ship fast but debug slow.  Let's fix that.\n"
	// A Doer that fails any call proves the no-fetch contract: teleprompter must
	// never touch the network.
	doer := &stubDoer{routes: map[string]stubResp{}}
	s := New(&Options{HTTP: doer, SourceText: script})

	docs, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{
		URL: "teleprompter://devrel-talk-2026", Kind: KindTeleprompterScript,
	})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("want 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if len(doer.calls) != 0 {
		t.Errorf("teleprompter must not fetch; made %d calls: %v", len(doer.calls), doer.calls)
	}
	if d.SourceKind != KindTeleprompterScript {
		t.Errorf("SourceKind = %q", d.SourceKind)
	}
	if d.Title != "Opening Hook: why observability matters" {
		t.Errorf("Title = %q", d.Title)
	}
	if !strings.Contains(d.Text, "debug slow") {
		t.Errorf("body missing: %q", d.Text)
	}
	// normalizeBlocks collapses the triple blank line to a single separator.
	if strings.Contains(d.Text, "\n\n\n") {
		t.Errorf("blank lines not normalized: %q", d.Text)
	}
}

func TestScrapeTeleprompterNoText(t *testing.T) {
	s := New(&Options{HTTP: &stubDoer{routes: map[string]stubResp{}}}) // no SourceText
	_, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{
		URL: "teleprompter://x", Kind: KindTeleprompterScript,
	})
	if err == nil || !strings.Contains(err.Error(), "no inline text") {
		t.Fatalf("want no-inline-text error, got %v", err)
	}
}

// --- talk-recording: reuse youtube path + caption-document parsing ---

func TestScrapeTalkRecordingYouTubeReuse(t *testing.T) {
	fixedClock(t)
	watchURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	capURL := "https://www.youtube.com/api/timedtext?v=dQw4w9WgXcQ&lang=en"
	doer := &stubDoer{routes: map[string]stubResp{
		watchURL: {body: fixture(t, "youtube_watch.html")},
		capURL:   {body: fixture(t, "youtube_captions.xml")},
	}}
	s := New(&Options{HTTP: doer})

	docs, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{
		URL: "https://youtu.be/dQw4w9WgXcQ", Kind: KindTalkRecording,
	})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(docs) != 1 || docs[0].SourceKind != KindTalkRecording {
		t.Fatalf("want 1 talk-recording doc, got %+v", docs)
	}
	if !strings.Contains(docs[0].Text, "we're talking about observability") {
		t.Errorf("youtube transcript path not reused: %q", docs[0].Text)
	}
}

func TestScrapeTalkRecordingVTT(t *testing.T) {
	fixedClock(t)
	url := "https://talks.example.com/keynote.vtt"
	vtt := "WEBVTT\n\nNOTE recorded at conf\n\n1\n00:00:01.000 --> 00:00:04.000\n<v Speaker>Welcome to the <c>keynote</c>.\n\n2\n00:00:04.000 --> 00:00:07.000\nToday we talk about developer trust.\n"
	doer := &stubDoer{routes: map[string]stubResp{
		url:                                    {body: []byte(vtt)},
		"https://talks.example.com/robots.txt": {status: 404},
	}}
	s := New(&Options{HTTP: doer})

	docs, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{URL: url, Kind: KindTalkRecording})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	d := docs[0]
	if strings.Contains(d.Text, "-->") || strings.Contains(d.Text, "WEBVTT") || strings.Contains(d.Text, "<c>") {
		t.Errorf("cue markup leaked: %q", d.Text)
	}
	for _, want := range []string{"Welcome to the keynote.", "developer trust"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("transcript missing %q in %q", want, d.Text)
		}
	}
	if d.Title != "keynote" {
		t.Errorf("Title = %q", d.Title)
	}
}

func TestScrapeTalkRecordingSRT(t *testing.T) {
	fixedClock(t)
	url := "https://talks.example.com/talk.srt"
	srt := "1\n00:00:01,000 --> 00:00:03,000\nFirst line of the talk.\n\n2\n00:00:03,000 --> 00:00:05,000\nSecond line of the talk.\n"
	doer := &stubDoer{routes: map[string]stubResp{
		url:                                    {body: []byte(srt)},
		"https://talks.example.com/robots.txt": {status: 404},
	}}
	s := New(&Options{HTTP: doer})

	docs, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{URL: url, Kind: KindTalkRecording})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if !strings.Contains(docs[0].Text, "First line of the talk. Second line of the talk.") {
		t.Errorf("srt not linearized: %q", docs[0].Text)
	}
}

// --- github-repo: markdown-only fan-out ---

func TestScrapeGitHubRepoMarkdownOnly(t *testing.T) {
	fixedClock(t)
	// Repo metadata (default branch) + recursive tree + raw markdown bodies.
	meta := `{"default_branch":"main"}`
	tree := `{"truncated":false,"tree":[
		{"path":"README.md","type":"blob"},
		{"path":"docs/getting-started.md","type":"blob"},
		{"path":"docs/img/logo.png","type":"blob"},
		{"path":"main.go","type":"blob"},
		{"path":"internal/util.md","type":"blob"},
		{"path":"CHANGELOG.md","type":"blob"}
	]}`
	readme := "# Awesome Tool\n\nThis is the **intro** paragraph with a [link](https://x).\n\n```go\nfmt.Println(\"skip me\")\n```\n\nSecond paragraph of prose.\n"
	gs := "# Getting Started\n\nInstall and run.\n"
	doer := &stubDoer{routes: map[string]stubResp{
		"https://api.github.com/repos/acme/tool":                                   {body: []byte(meta)},
		"https://api.github.com/repos/acme/tool/git/trees/main?recursive=1":        {body: []byte(tree)},
		"https://raw.githubusercontent.com/acme/tool/main/README.md":               {body: []byte(readme)},
		"https://raw.githubusercontent.com/acme/tool/main/docs/getting-started.md": {body: []byte(gs)},
	}}
	s := New(&Options{HTTP: doer})

	docs, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{
		URL: "https://github.com/acme/tool", Kind: KindGitHubRepo,
	})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	// README + docs/getting-started only: CHANGELOG, internal/util.md, main.go,
	// and the png are all excluded.
	if len(docs) != 2 {
		t.Fatalf("want 2 markdown docs, got %d: %+v", len(docs), docURLs(docs))
	}
	var readmeDoc bool
	for _, d := range docs {
		if d.SourceKind != KindGitHubRepo {
			t.Errorf("SourceKind = %q", d.SourceKind)
		}
		if strings.Contains(d.Text, "skip me") || strings.Contains(d.Text, "```") {
			t.Errorf("code fence not stripped: %q", d.Text)
		}
		if strings.Contains(d.Text, "**") || strings.Contains(d.Text, "](") {
			t.Errorf("markdown syntax leaked: %q", d.Text)
		}
		if d.Meta["path"] == "README.md" {
			readmeDoc = true
			if d.Title != "Awesome Tool" {
				t.Errorf("README title = %q", d.Title)
			}
			if !strings.Contains(d.Text, "intro paragraph with a link") {
				t.Errorf("README prose: %q", d.Text)
			}
		}
	}
	if !readmeDoc {
		t.Errorf("README not among docs: %v", docURLs(docs))
	}
	// Never fetched a non-markdown blob.
	for _, c := range doer.calls {
		if strings.HasSuffix(c, ".png") || strings.HasSuffix(c, "main.go") || strings.HasSuffix(c, "CHANGELOG.md") {
			t.Errorf("fetched a non-wanted file: %s", c)
		}
	}
}

func TestScrapeGitHubRepoBranchInURL(t *testing.T) {
	fixedClock(t)
	tree := `{"truncated":false,"tree":[{"path":"docs/intro.md","type":"blob"},{"path":"docs/other.md","type":"blob"}]}`
	doer := &stubDoer{routes: map[string]stubResp{
		"https://api.github.com/repos/acme/tool/git/trees/dev?recursive=1": {body: []byte(tree)},
		"https://raw.githubusercontent.com/acme/tool/dev/docs/intro.md":    {body: []byte("# Intro\n\nHello.\n")},
	}}
	s := New(&Options{HTTP: doer})

	// /tree/dev/docs/intro.md narrows to that subpath; no repo-metadata call
	// should be made because the branch is explicit.
	docs, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{
		URL: "https://github.com/acme/tool/tree/dev/docs/intro.md", Kind: KindGitHubRepo,
	})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(docs) != 1 || docs[0].Meta["path"] != "docs/intro.md" {
		t.Fatalf("subpath filter failed: %v", docURLs(docs))
	}
	for _, c := range doer.calls {
		if strings.HasSuffix(c, "/repos/acme/tool") {
			t.Errorf("made a default-branch call despite explicit branch: %s", c)
		}
	}
}

func TestParseGitHubRepo(t *testing.T) {
	cases := []struct {
		in                           string
		owner, name, branch, subpath string
	}{
		{"https://github.com/acme/tool", "acme", "tool", "", ""},
		{"https://github.com/acme/tool.git", "acme", "tool", "", ""},
		{"https://github.com/acme/tool/tree/main", "acme", "tool", "main", ""},
		{"https://github.com/acme/tool/tree/v2/docs/guide", "acme", "tool", "v2", "docs/guide"},
	}
	for _, c := range cases {
		got, err := parseGitHubRepo(c.in)
		if err != nil {
			t.Errorf("parseGitHubRepo(%q): %v", c.in, err)
			continue
		}
		if got.owner != c.owner || got.name != c.name || got.branch != c.branch || got.subpath != c.subpath {
			t.Errorf("parseGitHubRepo(%q) = %+v, want %s/%s @%s :%s", c.in, got, c.owner, c.name, c.branch, c.subpath)
		}
	}
	if _, err := parseGitHubRepo("https://gitlab.com/acme/tool"); err == nil {
		t.Errorf("want error for non-github host")
	}
}

func docURLs(docs []rag.RawDoc) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.SourceURL
	}
	return out
}

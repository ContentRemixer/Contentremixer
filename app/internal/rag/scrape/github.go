package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// githubRepoScraper fetches a GitHub repository's prose as style exemplars,
// MARKDOWN ONLY (board decision, plan §10 decision 3 — v1 ingests documentation,
// not source code or comments). It selects README, everything under any docs/
// directory, and tutorial/guide markdown, fetches each file's raw content, and
// strips markdown to readable prose. Fan-out is bounded by MaxDocs so a large
// repo cannot trigger an unbounded crawl (the Scraper also re-enforces the cap).
//
// It talks to the GitHub REST API (git/trees for the file list, raw.github-
// usercontent.com for bodies). robots.txt is not consulted: these are documented
// programmatic API/raw endpoints, not arbitrary crawled pages, and the budget +
// markdown-only filter already bound the work. An optional token lifts the 60-
// req/hr anonymous rate limit.
type githubRepoScraper struct {
	fetch   *fetcher
	maxDocs int
	token   string
}

var _ kindScraper = (*githubRepoScraper)(nil)

// apiBase / rawBase are overridable in tests so the fixture Doer can route the
// GitHub API and raw hosts without touching the network.
var (
	apiBase = "https://api.github.com"
	rawBase = "https://raw.githubusercontent.com"
)

func (g *githubRepoScraper) scrape(ctx context.Context, ref v1alpha1.PersonaReference) ([]rag.RawDoc, error) {
	repo, err := parseGitHubRepo(ref.URL)
	if err != nil {
		return nil, err
	}
	branch := repo.branch
	if branch == "" {
		branch, err = g.defaultBranch(ctx, repo)
		if err != nil {
			return nil, err
		}
	}

	paths, truncated, err := g.listMarkdown(ctx, repo, branch)
	if err != nil {
		return nil, err
	}
	// Keep only wanted markdown under the optional subpath, bounded by the cap.
	wanted := make([]string, 0, len(paths))
	for _, p := range paths {
		if repo.subpath != "" && !strings.HasPrefix(p, repo.subpath+"/") && p != repo.subpath {
			continue
		}
		if isWantedMarkdown(p) {
			wanted = append(wanted, p)
		}
	}
	if len(wanted) == 0 {
		msg := "no README/docs/tutorial markdown found"
		if truncated {
			msg += " (repo tree was truncated by the GitHub API)"
		}
		return nil, fmt.Errorf("scrape: github-repo %q: %s", ref.URL, msg)
	}
	if g.maxDocs > 0 && len(wanted) > g.maxDocs {
		logDropped(ctx, KindGitHubRepo, ref.URL, g.maxDocs, len(wanted)-g.maxDocs)
		wanted = wanted[:g.maxDocs]
	}

	docs := make([]rag.RawDoc, 0, len(wanted))
	for _, p := range wanted {
		raw, _, err := g.get(ctx, fmt.Sprintf("%s/%s/%s/%s/%s", rawBase, repo.owner, repo.name, branch, p))
		if err != nil {
			// One unreadable file must not sink the whole repo scrape; skip it.
			continue
		}
		text := markdownToText(string(raw))
		if strings.TrimSpace(text) == "" {
			continue
		}
		blobURL := fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s", repo.owner, repo.name, branch, p)
		docs = append(docs, rag.RawDoc{
			SourceURL: blobURL,
			Title:     markdownTitle(string(raw), p),
			Text:      text,
			Meta: map[string]any{
				"repo":              fmt.Sprintf("%s/%s", repo.owner, repo.name),
				"branch":            branch,
				"path":              p,
				"originalReference": ref.URL,
			},
		})
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("scrape: github-repo %q: no readable markdown content extracted", ref.URL)
	}
	return docs, nil
}

// get issues a GET with the optional bearer token (and the GitHub API version
// header for api.github.com requests).
func (g *githubRepoScraper) get(ctx context.Context, rawURL string) ([]byte, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("scrape: github-repo: build request for %q: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", g.fetch.userAgent)
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	if strings.HasPrefix(rawURL, apiBase) {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := g.fetch.do.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("scrape: github-repo: fetch %q: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp, fmt.Errorf("scrape: github-repo: fetch %q: unexpected status %d", rawURL, resp.StatusCode)
	}
	body, err := readCapped(resp.Body)
	if err != nil {
		return nil, resp, fmt.Errorf("scrape: github-repo: read body of %q: %w", rawURL, err)
	}
	return body, resp, nil
}

func (g *githubRepoScraper) defaultBranch(ctx context.Context, repo githubRepo) (string, error) {
	body, _, err := g.get(ctx, fmt.Sprintf("%s/repos/%s/%s", apiBase, repo.owner, repo.name))
	if err != nil {
		return "", err
	}
	var meta struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", fmt.Errorf("scrape: github-repo: decode repo metadata: %w", err)
	}
	if meta.DefaultBranch == "" {
		return "", fmt.Errorf("scrape: github-repo %s/%s: no default branch reported", repo.owner, repo.name)
	}
	return meta.DefaultBranch, nil
}

// listMarkdown returns every markdown blob path in the repo tree (recursive),
// plus whether the API truncated the listing (very large repos).
func (g *githubRepoScraper) listMarkdown(ctx context.Context, repo githubRepo, branch string) ([]string, bool, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", apiBase, repo.owner, repo.name, url.PathEscape(branch))
	body, _, err := g.get(ctx, u)
	if err != nil {
		return nil, false, err
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, false, fmt.Errorf("scrape: github-repo: decode tree: %w", err)
	}
	paths := make([]string, 0, len(tree.Tree))
	for _, e := range tree.Tree {
		if e.Type == "blob" && isMarkdownPath(e.Path) {
			paths = append(paths, e.Path)
		}
	}
	return paths, tree.Truncated, nil
}

// githubRepo is a parsed repository reference.
type githubRepo struct {
	owner   string
	name    string
	branch  string // optional, from /tree/<branch>
	subpath string // optional, from /tree/<branch>/<subpath>
}

// parseGitHubRepo accepts github.com repo URLs in the common shapes:
//
//	https://github.com/owner/repo
//	https://github.com/owner/repo.git
//	https://github.com/owner/repo/tree/<branch>[/<subpath>...]
func parseGitHubRepo(raw string) (githubRepo, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return githubRepo{}, fmt.Errorf("scrape: github-repo: parse url %q: %w", raw, err)
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	if host != "github.com" {
		return githubRepo{}, fmt.Errorf("scrape: github-repo: %q is not a github.com URL", raw)
	}
	segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	if len(segs) < 2 {
		return githubRepo{}, fmt.Errorf("scrape: github-repo: %q is not an owner/repo URL", raw)
	}
	repo := githubRepo{owner: segs[0], name: strings.TrimSuffix(segs[1], ".git")}
	// /tree/<branch>/<subpath...>
	if len(segs) >= 4 && segs[2] == "tree" {
		repo.branch = segs[3]
		if len(segs) > 4 {
			repo.subpath = strings.Join(segs[4:], "/")
		}
	}
	return repo, nil
}

var markdownExtRe = regexp.MustCompile(`(?i)\.(md|markdown|mdx)$`)

func isMarkdownPath(p string) bool { return markdownExtRe.MatchString(p) }

// isWantedMarkdown keeps the v1 selection: README (anywhere), anything under a
// docs/ directory (any depth), and tutorial/guide/getting-started markdown.
func isWantedMarkdown(p string) bool {
	if !isMarkdownPath(p) {
		return false
	}
	lower := strings.ToLower(p)
	base := strings.ToLower(path.Base(p))
	if strings.HasPrefix(base, "readme.") {
		return true
	}
	if lower == "docs" || strings.HasPrefix(lower, "docs/") || strings.Contains(lower, "/docs/") {
		return true
	}
	for _, kw := range []string{"tutorial", "guide", "getting-started", "getting_started", "howto", "how-to", "walkthrough"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

var (
	fencedCodeRe  = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")
	htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	atxHeadingRe  = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s*`)
	imageRe       = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	linkRe        = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	inlineCodeRe  = regexp.MustCompile("`+")
	emphasisRe    = regexp.MustCompile(`[*_]{1,3}`)
	listBulletRe  = regexp.MustCompile(`(?m)^\s{0,3}([-*+]|\d+\.)\s+`)
	blockquoteRe  = regexp.MustCompile(`(?m)^\s{0,3}>\s?`)
	htmlTagRe     = regexp.MustCompile(`<[^>]+>`)
)

// markdownToText strips markdown syntax to readable prose for style capture:
// fenced code blocks and HTML comments are dropped entirely (not style signal),
// headings/list/blockquote markers are removed, links/images unwrap to their
// text, and inline code/emphasis markers are stripped. Block structure is kept
// so the chunker splits on paragraphs.
func markdownToText(md string) string {
	s := fencedCodeRe.ReplaceAllString(md, "\n")
	s = htmlCommentRe.ReplaceAllString(s, "")
	s = imageRe.ReplaceAllString(s, "")
	s = linkRe.ReplaceAllString(s, "$1")
	s = atxHeadingRe.ReplaceAllString(s, "")
	s = blockquoteRe.ReplaceAllString(s, "")
	s = listBulletRe.ReplaceAllString(s, "")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = inlineCodeRe.ReplaceAllString(s, "")
	s = emphasisRe.ReplaceAllString(s, "")
	return normalizeBlocks(s)
}

var firstHeadingRe = regexp.MustCompile(`(?m)^\s{0,3}#\s+(.+?)\s*#*\s*$`)

// markdownTitle prefers the first H1 heading, else the filename (de-slugged).
func markdownTitle(md, p string) string {
	if m := firstHeadingRe.FindStringSubmatch(md); m != nil {
		return collapseSpaces(m[1])
	}
	base := strings.TrimSuffix(path.Base(p), path.Ext(p))
	base = strings.NewReplacer("-", " ", "_", " ").Replace(base)
	return collapseSpaces(base)
}

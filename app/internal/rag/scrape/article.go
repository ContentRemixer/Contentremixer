package scrape

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// articleScraper fetches an article/blog page and extracts its readable body
// (design §3.1: "readability-style extraction, strip nav/boilerplate"). It is
// a dependency-light extractor built on golang.org/x/net/html (already in the
// module graph) rather than a third-party readability module: it drops
// non-content elements (script/style/nav/header/footer/aside/form), then picks
// the subtree with the most text as the main content and linearizes it to
// block-separated plain text.
type articleScraper struct {
	fetch  *fetcher
	robots *robotsCache
}

var _ kindScraper = (*articleScraper)(nil)

func (a *articleScraper) scrape(ctx context.Context, ref v1alpha1.PersonaReference) ([]rag.RawDoc, error) {
	if err := a.robots.allowed(ctx, ref.URL, a.fetch.userAgent); err != nil {
		return nil, err
	}

	body, _, err := a.fetch.get(ctx, ref.URL)
	if err != nil {
		return nil, err
	}
	root, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("scrape: article %q: parse html: %w", ref.URL, err)
	}

	title := extractTitle(root)
	text := extractReadable(root)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("scrape: article %q: no readable content extracted", ref.URL)
	}

	return []rag.RawDoc{{
		SourceURL: ref.URL,
		Title:     title,
		Text:      text,
		Meta: map[string]any{
			"originalReference": ref.URL,
		},
	}}, nil
}

// boilerplateTags are structural/non-content elements dropped before scoring.
var boilerplateTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true,
	atom.Nav: true, atom.Header: true, atom.Footer: true,
	atom.Aside: true, atom.Form: true, atom.Button: true,
	atom.Svg: true, atom.Iframe: true, atom.Template: true,
}

// blockTags introduce a block boundary (newline) when linearized.
var blockTags = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Br: true, atom.Li: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true,
	atom.H5: true, atom.H6: true, atom.Section: true, atom.Article: true,
	atom.Blockquote: true, atom.Pre: true, atom.Tr: true, atom.Ul: true, atom.Ol: true,
}

// extractTitle prefers <meta property="og:title">, then <title>.
func extractTitle(root *html.Node) string {
	var og, titleTag string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Meta:
				if attr(n, "property") == "og:title" || attr(n, "name") == "og:title" {
					if c := attr(n, "content"); c != "" && og == "" {
						og = c
					}
				}
			case atom.Title:
				if titleTag == "" {
					titleTag = textOf(n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if strings.TrimSpace(og) != "" {
		return collapseSpaces(og)
	}
	return collapseSpaces(titleTag)
}

// extractReadable removes boilerplate subtrees, finds the densest content
// container (the candidate node whose descendant text is longest, biased to
// <article>/<main> and content-ish class/id names), and linearizes it.
func extractReadable(root *html.Node) string {
	body := findBody(root)
	if body == nil {
		body = root
	}
	stripBoilerplate(body)

	best := pickMainContent(body)
	if best == nil {
		best = body
	}
	return linearize(best)
}

func findBody(root *html.Node) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Body {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// stripBoilerplate detaches boilerplate element subtrees in place.
func stripBoilerplate(n *html.Node) {
	for child := n.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == html.ElementNode && boilerplateTags[child.DataAtom] {
			n.RemoveChild(child)
		} else {
			stripBoilerplate(child)
		}
		child = next
	}
}

// pickMainContent returns the element with the highest content score: the
// length of its descendant text, boosted for semantic/content containers and
// penalized for link-heavy (navigation-like) subtrees.
func pickMainContent(root *html.Node) *html.Node {
	var best *html.Node
	var bestScore float64
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if s := scoreNode(n); s > bestScore {
				bestScore, best = s, n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	return best
}

func scoreNode(n *html.Node) float64 {
	textLen := len(strings.TrimSpace(textOf(n)))
	if textLen == 0 {
		return 0
	}
	score := float64(textLen)
	switch n.DataAtom {
	case atom.Article, atom.Main:
		score *= 3
	case atom.Section:
		score *= 1.5
	}
	idClass := strings.ToLower(attr(n, "id") + " " + attr(n, "class") + " " + attr(n, "role"))
	for _, good := range []string{"article", "content", "post", "entry", "story", "main", "body"} {
		if strings.Contains(idClass, good) {
			score *= 1.5
			break
		}
	}
	for _, bad := range []string{"comment", "sidebar", "nav", "menu", "footer", "promo", "share", "related"} {
		if strings.Contains(idClass, bad) {
			score *= 0.3
			break
		}
	}
	// Penalize link farms: subtrees where most text sits inside anchors.
	if linkLen := anchorTextLen(n); textLen > 0 && float64(linkLen)/float64(textLen) > 0.5 {
		score *= 0.2
	}
	return score
}

// linearize renders a node's text with block-level newlines, collapsing
// whitespace within each block and separating blocks by a single blank line.
func linearize(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
		case html.ElementNode:
			block := blockTags[n.DataAtom]
			if block {
				b.WriteString("\n")
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			if block {
				b.WriteString("\n")
			}
		}
	}
	walk(n)
	return normalizeBlocks(b.String())
}

func anchorTextLen(n *html.Node) int {
	total := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.A {
			total += len(strings.TrimSpace(textOf(n)))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return total
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

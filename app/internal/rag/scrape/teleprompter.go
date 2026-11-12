package scrape

import (
	"context"
	"fmt"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// teleprompterScraper ingests a DevRel talk/teleprompter script directly from
// the inline body the DevRel pasted at onboarding (plan §5: "direct text ingest
// — no fetch; store + chunk + embed"). There is no network call: the body is
// already in hand (stored by Booth as a Postgres raw-text blob, plan §4), so the
// scraper only normalizes it into a RawDoc for the chunk/embed stages. The body
// arrives on Options.SourceText because the frozen PersonaReference contract
// (URL + Kind only) carries no text field.
type teleprompterScraper struct {
	text string
}

var _ kindScraper = (*teleprompterScraper)(nil)

func (t *teleprompterScraper) scrape(_ context.Context, ref v1alpha1.PersonaReference) ([]rag.RawDoc, error) {
	if strings.TrimSpace(t.text) == "" {
		// Fail closed with an actionable message: the launcher must pass the
		// pasted/stored script body (SCRAPE_SOURCE_TEXT) for this kind.
		return nil, fmt.Errorf("scrape: teleprompter-script reference %q has no inline text (expected the pasted/stored script body)", ref.URL)
	}
	text := normalizeBlocks(t.text)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("scrape: teleprompter-script reference %q yielded no usable text", ref.URL)
	}
	return []rag.RawDoc{{
		SourceURL: ref.URL,
		Title:     firstLineTitle(text),
		Text:      text,
		Meta: map[string]any{
			"ingest":            "inline",
			"chars":             len(text),
			"originalReference": ref.URL,
		},
	}}, nil
}

// firstLineTitle derives a short title from the first non-empty line of a
// document, trimmed to a sane length. Teleprompter scripts rarely carry a formal
// title, so the opening line is the best cheap signal for the review queue.
func firstLineTitle(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		ln = collapseSpaces(ln)
		if ln == "" {
			continue
		}
		const max = 120
		if len(ln) > max {
			ln = strings.TrimSpace(ln[:max]) + "…"
		}
		return ln
	}
	return ""
}

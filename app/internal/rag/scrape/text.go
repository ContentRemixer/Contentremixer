package scrape

import (
	"regexp"
	"strings"
)

var (
	wsRe        = regexp.MustCompile(`[ \t\f\v]+`)
	blankLineRe = regexp.MustCompile(`\n{3,}`)
)

// collapseSpaces trims and collapses all runs of whitespace (including the
// non-breaking space) to single spaces. Used for single-line fields (titles)
// and transcript bodies. strings.Fields already treats U+00A0 as whitespace.
func collapseSpaces(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// normalizeBlocks collapses intra-line whitespace, trims each line, reduces
// runs of blank lines to a single blank-line separator, and trims the whole
// document. Produces the stable block-separated text the chunker splits.
func normalizeBlocks(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, " ", " ") // nbsp → space
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = wsRe.ReplaceAllString(ln, " ")
		out = append(out, strings.TrimSpace(ln))
	}
	joined := strings.Join(out, "\n")
	joined = blankLineRe.ReplaceAllString(joined, "\n\n")
	return strings.TrimSpace(joined)
}

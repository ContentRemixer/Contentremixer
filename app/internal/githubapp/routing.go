// Package githubapp is subtask D (ISI-7079): the GitHub App webhook receiver
// (the "Probot-equivalent" in Go, design §1 decision) plus the single,
// config-driven label→format resolution used by the whole pipeline.
//
// Routing is driven entirely by ReferenceRepo.spec.labelRoutes — the label
// string is never parsed for its format (design §3 rule 2). Resolve is the one
// shared resolution function; the webhook (this package), the analyzer and the
// writer all read the same routes.
package githubapp

import (
	"fmt"
	"strings"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// recyclePrefix is the only label namespace that participates in routing.
const recyclePrefix = "recycle:"

// Route is one resolved label→format transformation (design §3/§4).
type Route struct {
	// Label is the exact GitHub label that matched (echoed in the ack).
	Label string
	// InputFormat is the route's resolved analyzer input (may be "auto").
	InputFormat string
	// OutputFormat is the route's resolved writer style (styles/<fmt>.md).
	OutputFormat string
}

// Resolve maps an issue's labels to pipeline routes using the repo CR's
// labelRoutes as the single source of truth (design §3).
//
// It returns one Route per recognised recycle:* label and the list of
// recycle:* labels that had no matching route (echoed in the ack as
// "ignored"). Non-recycle:* labels are irrelevant to routing and are dropped.
// Matching is by exact label string — no format is parsed out of the name.
func Resolve(repoCR *contentremixerv1alpha1.ReferenceRepo, issueLabels []string) (matched []Route, unmapped []string) {
	byLabel := make(map[string]contentremixerv1alpha1.LabelRoute, len(repoCR.Spec.LabelRoutes))
	for _, rt := range repoCR.Spec.LabelRoutes {
		byLabel[rt.Label] = rt
	}

	seen := make(map[string]bool, len(issueLabels))
	for _, label := range issueLabels {
		if !strings.HasPrefix(label, recyclePrefix) {
			continue // rule 1: only recycle:* labels route.
		}
		if seen[label] {
			continue // the same label can arrive twice across events; route once.
		}
		seen[label] = true

		if rt, ok := byLabel[label]; ok {
			input := rt.InputFormat
			if input == "" {
				input = "auto" // mirrors the CRD server-side default.
			}
			matched = append(matched, Route{Label: label, InputFormat: input, OutputFormat: rt.OutputFormat})
		} else {
			unmapped = append(unmapped, label)
		}
	}
	return matched, unmapped
}

// ackComment renders the acknowledgement comment body posted back to the issue
// (design §3, consumer 1). It always names what was resolved and what was
// ignored so the acceptance "documented ignored ack" is self-explanatory.
//
// Precondition: at least one recycle:* label was present (len(matched)+
// len(unmapped) > 0); the caller does not ack when nothing recycle-related was
// seen.
func ackComment(matched []Route, unmapped []string) string {
	var b strings.Builder
	b.WriteString("🎛️ **ContentRemixer** picked up this issue.\n")

	if len(matched) > 0 {
		b.WriteString("\n**Resolved routes** (from the `ReferenceRepo` `spec.labelRoutes`):\n")
		for _, r := range matched {
			_, _ = fmt.Fprintf(&b, "- `%s` → input `%s` → output `%s`\n", r.Label, r.InputFormat, r.OutputFormat)
		}
	} else {
		b.WriteString("\nNo recycle label maps to a route in the `ReferenceRepo` `spec.labelRoutes`, so **nothing will run**.\n")
	}

	if len(unmapped) > 0 {
		b.WriteString("\n**Ignored** (no matching route — add one to `spec.labelRoutes` to enable):\n")
		for _, label := range unmapped {
			_, _ = fmt.Fprintf(&b, "- `%s`\n", label)
		}
	}
	return b.String()
}

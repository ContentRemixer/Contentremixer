// Package contentvalidate is the central Content CI validation logic (ISI-7118,
// subtask C7; ISI-7093 plan §3). It runs the Content CI checks against changed
// content folders of a reference repo and emits GitHub PR annotations plus a
// markdown summary table.
//
// It is the single source of truth the thin composite GitHub Action
// (.github/actions/content-validate) and the ReferenceRepo install (subtask C8,
// ISI-7120) both drive — zero duplication: the Action and C8 call the same
// `contentremixer content validate` CLI, which calls this package.
//
// Checks (plan §3), each a Rule in the extensible registry:
//
//	required-assets   — every asset declared by content/<type>/meta.template.yaml
//	                    (files.*) is present on disk                      (error)
//	asset-structure   — meta.yaml parses; template-declared fields are
//	                    present; title is non-empty                      (error/warn)
//	content-quality   — reuse ISI-7082 fail-closed checker/Mastering over
//	                    the body + meta (secrets/IP/hostname → block)     (error/warn)
//	seo               — single H1, title length, non-empty tags          (warn)
//	pr-template       — required PR-body sections present (pairs with C9) (error)
//
// FAIL-CLOSED: a single error-severity Finding fails validation (exit 1); warn
// findings are advisory. The engine is extensible — new checks register a Rule
// with no change to the CLI or the Action.
package contentvalidate

import (
	"context"
	"sort"
)

// Severity classifies a Finding. Error fails validation (fail-closed); Warning
// is advisory. The string values map 1:1 to GitHub workflow-command levels
// (::error / ::warning), so report.go emits them verbatim.
type Severity string

const (
	// SeverityError fails validation closed: any Finding at this level sets
	// Report.OK() = false and the CLI exits non-zero.
	SeverityError Severity = "error"
	// SeverityWarning is advisory and never fails validation by default
	// (the CLI's --fail-on=warning promotes it).
	SeverityWarning Severity = "warning"
)

// Finding is one rule outcome against one content target. File/Line anchor the
// GitHub PR annotation; Line 0 means a file-level (no line) annotation.
type Finding struct {
	// Rule is the emitting rule's ID (stable, kebab-case).
	Rule string
	// Severity is SeverityError or SeverityWarning.
	Severity Severity
	// Message is the human-readable finding, safe to surface on a public PR
	// (rules that scan for secrets must redact — content-quality delegates
	// that to the ISI-7082 checker, which already redacts evidence).
	Message string
	// Target is the content folder the finding is about (repo-relative),
	// or "" for repo-level findings (e.g. pr-template).
	Target string
	// File is the repo-relative file the annotation anchors to (for the
	// GitHub ::error file=... command). May equal Target's meta.yaml.
	File string
	// Line is the 1-based line for the annotation, or 0 for file-level.
	Line int
}

// Target is one changed item folder to validate: a directory directly
// containing a meta.yaml, under content/<type>/ (a Crate item) or
// recycled/<slug>/ (a recycled draft a remix PR adds, ISI-7076 §4).
type Target struct {
	// Type is the content type: the segment after content/ for a Crate item
	// (blog, videos, podcasts, livestreams, social, newsletter), or — for a
	// recycled/<slug>/ draft — meta.output_format mapped via C9 pr.ContentType.
	// "" when the folder resolves to no known type (type-specific rules skip).
	Type string
	// Dir is the repo-relative content folder path.
	Dir string
	// MetaPath is the repo-relative path of the folder's meta.yaml.
	MetaPath string
	// Meta is the parsed meta.yaml (nil if it was unreadable/unparsable —
	// asset-structure reports that as an error).
	Meta map[string]any
	// MetaErr is set when meta.yaml could not be read or parsed.
	MetaErr error
	// Template is the parsed content/<type>/meta.template.yaml, or nil when
	// the type has no template (required-assets then reports it).
	Template *Template
	// Body is the resolved primary body text (post/teleprompter/transcript),
	// via the reused ISI-7080 analyzer; "" when none resolves.
	Body string
	// BodyFile is the repo-relative path body came from (for annotations).
	BodyFile string
	// DetectedInput is the analyzer's resolved input format, when available.
	DetectedInput string
}

// ValidateContext is the shared input every Rule sees. A rule iterates whatever
// subset it needs (most iterate Targets; pr-template reads PRBody).
type ValidateContext struct {
	// RepoRoot is the reference-repo checkout root.
	RepoRoot string
	// Targets are the discovered changed content folders.
	Targets []Target
	// PRBody is the pull-request body text, or "" when not supplied
	// (pr-template then no-ops — it only runs when a PR body is present).
	PRBody string
	// PRBodyFile is the repo-relative path PRBody came from, for annotations.
	PRBodyFile string
	// PRContentType optionally forces the C9 content-PR-template type to
	// validate against (blog|social|newsletter|default). Empty lets the
	// pr-template rule detect it from the PR body's template marker.
	PRContentType string
}

// Rule is one extensible Content CI check. Implementations are pure and
// deterministic: same inputs → same Findings in a stable order.
type Rule interface {
	// ID is the stable kebab-case rule identifier (appears in Finding.Rule
	// and the summary table).
	ID() string
	// Description is a one-line human summary (shown by `content validate
	// --list-rules`).
	Description() string
	// Check runs the rule over the context and returns its findings.
	Check(ctx context.Context, vc *ValidateContext) []Finding
}

// Engine is the rule registry. The zero value is empty; use DefaultEngine for
// the plan-§3 built-ins. Register is additive so downstream (C8) can extend.
type Engine struct {
	rules []Rule
}

// Register adds a rule to the engine. Duplicate IDs are allowed but
// discouraged; rules run in registration order.
func (e *Engine) Register(r Rule) { e.rules = append(e.rules, r) }

// Rules returns the registered rules in order.
func (e *Engine) Rules() []Rule { return e.rules }

// Run executes every registered rule and returns the aggregated Report. Each
// rule's findings keep their rule-local order; across rules they are stable by
// registration order, then sorted into a deterministic report order.
func (e *Engine) Run(ctx context.Context, vc *ValidateContext) Report {
	var all []Finding
	for _, r := range e.rules {
		all = append(all, r.Check(ctx, vc)...)
	}
	sortFindings(all)
	return Report{Findings: all}
}

// Report is the aggregated outcome of an engine Run.
type Report struct {
	Findings []Finding
}

// Errors returns the number of error-severity findings.
func (r Report) Errors() int { return r.count(SeverityError) }

// Warnings returns the number of warn-severity findings.
func (r Report) Warnings() int { return r.count(SeverityWarning) }

func (r Report) count(s Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == s {
			n++
		}
	}
	return n
}

// OK reports whether validation passed under the given fail-on severity.
// failOnWarning=false (the default) passes when there are zero errors;
// failOnWarning=true also fails on any warning.
func (r Report) OK(failOnWarning bool) bool {
	if r.Errors() > 0 {
		return false
	}
	if failOnWarning && r.Warnings() > 0 {
		return false
	}
	return true
}

// sortFindings orders findings deterministically: by target, then file, then
// line, then rule, then message. Errors are NOT floated above warnings — the
// summary table groups by severity in report.go — so the order here is the
// stable "by location" order a reviewer scans.
func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
}

// DefaultEngine returns an Engine with the plan-§3 built-in rules registered in
// a stable order.
func DefaultEngine() *Engine {
	e := &Engine{}
	e.Register(RequiredAssetsRule{})
	e.Register(AssetStructureRule{})
	e.Register(ContentQualityRule{})
	e.Register(SEORule{})
	e.Register(PRTemplateRule{})
	return e
}

// Package pipeline holds the shared run-context and stage hand-off contracts
// for the recycle pipeline (design §4: analyzer → writer → checker).
//
// The types here are the authoritative Phase-1 data contracts; each stage
// lives in its own subpackage and implements against these:
//
//	pipeline/analyzer  — subtask E (ISI-7080): Sample + input-format parsers
//	pipeline/writer    — subtask F (ISI-7081): Mix + styles/<format>.md
//	pipeline/checker   — subtask G (ISI-7082): CheckResult + scrub rules
//
// Keeping the hand-off types in one leaf package means the webhook (subtask D,
// which builds RunContext) and the stages never import each other.
package pipeline

import (
	"context"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// RunContext is the shared run context, built by the webhook from the recycle
// issue and the resolved label route (design §4).
type RunContext struct {
	// Repo is the GitHub "owner/name" of the reference repository.
	Repo string
	// IssueNumber is the recycle issue the run originates from.
	IssueNumber int
	// SourcePath is the source location from the issue form: a content/...
	// path inside the reference repo OR an external URL.
	SourcePath string
	// InputFormat is the resolved route input format: one of
	// teleprompter | livestream | blog | podcast | auto ("auto" detects from
	// the source meta.yaml — see the analyzer package).
	InputFormat string
	// OutputFormat is the resolved route output format selecting
	// styles/<outputFormat>.md for the writer.
	OutputFormat string
	// Notes is the free-text angle/audience guidance from the issue form.
	Notes string
	// ModelName is the LiteLLM alias to use (system default unless overridden).
	ModelName string
	// Owner is propagated for tenancy tagging (design §6); platform-owned
	// (empty subject) in Phase 1.
	Owner contentremixerv1alpha1.OwnerRef
}

// Sample is the structured source brief the analyzer emits for the writer
// (design §4). It is the normalized, format-agnostic view of one source.
type Sample struct {
	// SourcePath is the normalized path (or URL) that was read.
	SourcePath string
	// SourceCommit is the git SHA of the reference repo checkout at read
	// time; mandatory for the PR Sample-clearance block (design §5).
	SourceCommit string
	// Meta is the parsed source meta.yaml (title, author, tags, files, ...).
	// Empty (not nil) for URL sources that have no meta.yaml.
	Meta map[string]any
	// DetectedInput is the concrete input format after resolving "auto".
	DetectedInput string
	// Body is the extracted source text: script, transcript, or article.
	Body string
}

// Analyzer is the pipeline stage contract implemented by subtask E
// (internal/pipeline/analyzer).
type Analyzer interface {
	// Analyze resolves the source (content/ path or URL), picks the parser
	// by InputFormat, parses meta.yaml, and emits the normalized brief.
	Analyze(ctx context.Context, rc RunContext) (Sample, error)
}

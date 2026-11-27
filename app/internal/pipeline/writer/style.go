// Style resolution seam (ISI-7965 W2): the writer resolves the output-format
// style guide through this interface before falling back to the repo file.
package writer

import (
	"context"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// StyleGuide is one resolved output-format style guide (ISI-7965 W2).
type StyleGuide struct {
	// Guide is the markdown style-guide body the writer binds into the
	// system prompt. Non-empty when ResolveStyleGuide returns nil.
	Guide string
	// Source is the provenance label echoed in the system-prompt style
	// block and recorded in Mix.Meta["style_source"], e.g.
	// "contentstyle:contentremixer/blog-style". Implementations must set
	// it; an empty label degrades to the generic "contentstyle".
	Source string
}

// StyleResolver resolves the style guide for one run (ISI-7965 W2: the
// ContentStyle CR source becomes the primary style-guide source, with the
// repo styles/<format>.md file kept as the transitional fallback until W5).
//
// The contract mirrors the optional Conditioner seam: optional on the
// Writer (nil = repo-file path, byte-for-byte unchanged), invoked per run
// with the RunContext threading the output format, and DEGRADE-NEVER-GATE
// on the CR side — an error or a missing CR is the repo-fallback signal,
// never a failed run. The writer fails closed only when NEITHER source
// yields a usable guide.
type StyleResolver interface {
	// ResolveStyleGuide returns the guide for the run's output format
	// (format is the trimmed rc.OutputFormat). A resolver that yields no
	// guide returns a non-nil error; the writer then tries the repo file.
	ResolveStyleGuide(ctx context.Context, rc pipeline.RunContext, format string) (StyleGuide, error)
}

// StyleResolverFunc adapts a function to StyleResolver (tests inject stubs).
type StyleResolverFunc func(ctx context.Context, rc pipeline.RunContext, format string) (StyleGuide, error)

// ResolveStyleGuide implements StyleResolver.
func (f StyleResolverFunc) ResolveStyleGuide(ctx context.Context, rc pipeline.RunContext, format string) (StyleGuide, error) {
	return f(ctx, rc, format)
}

package dispatch

import (
	"context"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/pr"
)

// ContentPipeline is the inline run worker the content strategy drives
// (analyzer → writer → checker → PR assembly). *pr.Runner satisfies it; the
// interface keeps ContentStrategy unit-testable with a fake.
type ContentPipeline interface {
	Run(ctx context.Context, rc pipeline.RunContext, target contentremixerv1alpha1.PRTargetSpec, token string) (pr.RunOutcome, error)
}

// ContentStrategy runs a WorkOrder on the in-process pipeline — the
// "content" actor template (ADR-1: lean text skills stay inline). It is the
// Phase-1 path behind the Dispatcher; recycle:blog must run byte-for-byte as
// before, so this maps the WorkOrder to the exact RunContext the pre-seam
// recycleRunner built (ModelName left empty => gateway default resolves it).
type ContentStrategy struct {
	// Pipeline is the inline run worker. Required.
	Pipeline ContentPipeline
}

// Runtime reports the inline backend.
func (s ContentStrategy) Runtime() Runtime { return RuntimeInline }

// Run executes the order on the inline pipeline.
func (s ContentStrategy) Run(ctx context.Context, wo WorkOrder) error {
	_, err := s.Pipeline.Run(ctx, runContext(wo), wo.Inputs.PRTarget, wo.Inputs.Token)
	return err
}

// runContext maps a WorkOrder to the pipeline RunContext. This is the
// regression-critical seam: the field mapping must stay identical to the
// pre-dispatch recycleRunner so recycle:blog is byte-for-byte unchanged.
func runContext(wo WorkOrder) pipeline.RunContext {
	return pipeline.RunContext{
		Repo:         wo.Repo,
		IssueNumber:  wo.IssueNumber,
		SourcePath:   wo.Inputs.SourcePath,
		InputFormat:  wo.Inputs.InputFormat,
		OutputFormat: wo.Inputs.OutputFormat,
		Notes:        wo.Inputs.Notes,
		Owner:        wo.MemoryRef.Owner,
		PersonaName:  wo.MemoryRef.Persona,
		// ModelName intentionally empty: the LiteLLM gateway default resolves
		// it (unchanged from the pre-seam path).
	}
}

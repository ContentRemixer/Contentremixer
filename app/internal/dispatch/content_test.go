package dispatch

import (
	"context"
	"testing"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/pr"
)

// fakePipeline captures the exact RunContext/PR target/token the content
// strategy passed, so the test can assert the mapping is byte-for-byte what
// the pre-seam recycleRunner built.
type fakePipeline struct {
	gotRC     pipeline.RunContext
	gotTarget contentremixerv1alpha1.PRTargetSpec
	gotToken  string
}

func (f *fakePipeline) Run(_ context.Context, rc pipeline.RunContext, target contentremixerv1alpha1.PRTargetSpec, token string) (pr.RunOutcome, error) {
	f.gotRC = rc
	f.gotTarget = target
	f.gotToken = token
	return pr.RunOutcome{}, nil
}

func TestContentStrategy_Runtime(t *testing.T) {
	if got := (ContentStrategy{}).Runtime(); got != RuntimeInline {
		t.Fatalf("Runtime() = %q, want %q", got, RuntimeInline)
	}
}

// TestContentStrategy_MapsWorkOrderToRunContext is the regression guard for
// recycle:blog: it pins the WorkOrder -> RunContext field mapping to exactly
// what the pre-dispatch recycleRunner produced.
func TestContentStrategy_MapsWorkOrderToRunContext(t *testing.T) {
	fp := &fakePipeline{}
	s := ContentStrategy{Pipeline: fp}

	target := contentremixerv1alpha1.PRTargetSpec{BaseBranch: "main", BranchPrefix: "recycle/", Draft: true}
	wo := WorkOrder{
		Repo:        "ContentRemixer/Contentremixer",
		IssueNumber: 42,
		Inputs: Inputs{
			SourcePath:   "content/kubecon-talk",
			Notes:        "punchy, for platform engineers",
			InputFormat:  "teleprompter",
			OutputFormat: "blog",
			Token:        "ghp_secret",
			PRTarget:     target,
		},
		Skill:         "content-writer",
		ActorTemplate: contentremixerv1alpha1.ActorTemplateContent,
	}

	if err := s.Run(context.Background(), wo); err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	want := pipeline.RunContext{
		Repo:         "ContentRemixer/Contentremixer",
		IssueNumber:  42,
		SourcePath:   "content/kubecon-talk",
		InputFormat:  "teleprompter",
		OutputFormat: "blog",
		Notes:        "punchy, for platform engineers",
		// ModelName, Owner, PersonaName stay zero — identical to the pre-seam
		// path, so recycle:blog is byte-for-byte unchanged.
	}
	if fp.gotRC != want {
		t.Fatalf("RunContext mismatch:\n got  %+v\n want %+v", fp.gotRC, want)
	}
	if fp.gotTarget != target {
		t.Fatalf("PRTarget mismatch: got %+v want %+v", fp.gotTarget, target)
	}
	if fp.gotToken != "ghp_secret" {
		t.Fatalf("token = %q, want %q", fp.gotToken, "ghp_secret")
	}
}

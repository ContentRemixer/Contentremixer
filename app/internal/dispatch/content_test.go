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
		Skill:        "content-writer",
		// ModelName, Owner, PersonaName stay zero. Skill now threads the route's
		// EffectiveSkill() (ISI-7421); for blog it is "content-writer", exactly
		// what the writer previously derived from the format, so recycle:blog is
		// byte-for-byte unchanged at the writer output.
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

// TestContentStrategy_ThreadsExplicitSkill proves ISI-7421: an explicit
// (non-default) route skill reaches the writer's RunContext.Skill regardless of
// output format, so route.EffectiveSkill() actually drives skill-pack selection
// instead of the writer always falling back to the format default. Here a short
// run is pinned to "human-voice-review" and must arrive as-is.
func TestContentStrategy_ThreadsExplicitSkill(t *testing.T) {
	fp := &fakePipeline{}
	s := ContentStrategy{Pipeline: fp}

	wo := WorkOrder{
		Repo:        "ContentRemixer/Contentremixer",
		IssueNumber: 7,
		Inputs: Inputs{
			SourcePath:   "content/snippet",
			InputFormat:  "auto",
			OutputFormat: "short",
			Token:        "ghp_secret",
		},
		Skill:         "human-voice-review",
		ActorTemplate: contentremixerv1alpha1.ActorTemplateContent,
	}

	if err := s.Run(context.Background(), wo); err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if fp.gotRC.Skill != "human-voice-review" {
		t.Fatalf("explicit route skill did not thread to RunContext: got %q, want %q",
			fp.gotRC.Skill, "human-voice-review")
	}
}

// TestContentStrategy_ShortDefaultStaysStyleOnly guards the regression hazard
// ISI-7421 calls out: a default short route carries the empty skill
// (DefaultSkill("short") == ""), so threading it leaves RunContext.Skill empty
// and the writer stays style-only — it must NOT become "content-writer".
func TestContentStrategy_ShortDefaultStaysStyleOnly(t *testing.T) {
	fp := &fakePipeline{}
	s := ContentStrategy{Pipeline: fp}

	wo := WorkOrder{
		Repo:        "ContentRemixer/Contentremixer",
		IssueNumber: 8,
		Inputs: Inputs{
			SourcePath:   "content/snippet",
			InputFormat:  "auto",
			OutputFormat: "short",
			Token:        "ghp_secret",
		},
		Skill:         contentremixerv1alpha1.DefaultSkill("short"), // "" — the route default
		ActorTemplate: contentremixerv1alpha1.ActorTemplateContent,
	}

	if err := s.Run(context.Background(), wo); err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if fp.gotRC.Skill != "" {
		t.Fatalf("default short route must stay style-only, got RunContext.Skill = %q", fp.gotRC.Skill)
	}
}

package contenttest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
)

// capturing generator records the request and returns a scripted response.
type stubGen struct {
	req  writer.GenerateRequest
	resp writer.GenerateResponse
	err  error
}

func (s *stubGen) Generate(_ context.Context, req writer.GenerateRequest) (writer.GenerateResponse, error) {
	s.req = req
	return s.resp, s.err
}

func TestRun_WeavesTraitsSelfDescriptionTopicAndMedium(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "A tidy sample.", Model: "gpt-4o"}}
	r := NewRunnerWithGenerator(gen, "alias-x")
	out, err := r.Run(context.Background(), Request{
		PersonaName:        "ada",
		Topic:              "zero-downtime deploys",
		PrimaryContentKind: "blog",
		StyleTraits:        "Dry wit, short sentences.",
		SelfDescription:    "I explain infra like a friend.",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Sample != "A tidy sample." {
		t.Fatalf("sample = %q", out.Sample)
	}
	if out.ModelUsed != "gpt-4o" {
		t.Fatalf("modelUsed = %q, want echoed gpt-4o", out.ModelUsed)
	}
	if gen.req.Model != "alias-x" {
		t.Fatalf("requested model = %q, want alias-x", gen.req.Model)
	}
	for _, want := range []string{"Dry wit, short sentences.", "I explain infra like a friend.", "STYLE TEST", "blog post"} {
		if !strings.Contains(gen.req.System, want) {
			t.Errorf("system prompt missing %q:\n%s", want, gen.req.System)
		}
	}
	for _, want := range []string{"zero-downtime deploys", "Target medium: blog"} {
		if !strings.Contains(gen.req.Prompt, want) {
			t.Errorf("user prompt missing %q:\n%s", want, gen.req.Prompt)
		}
	}
}

func TestRun_EmptyTopicFailsClosed(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "x"}}
	r := NewRunnerWithGenerator(gen, "")
	if _, err := r.Run(context.Background(), Request{PersonaName: "ada", Topic: "   "}); err == nil {
		t.Fatal("expected error for empty topic")
	}
}

func TestRun_EmptyCompletionFailsClosed(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "   "}}
	r := NewRunnerWithGenerator(gen, "")
	if _, err := r.Run(context.Background(), Request{PersonaName: "ada", Topic: "k8s"}); err == nil {
		t.Fatal("expected error for empty completion")
	}
}

func TestRun_GeneratorErrorPropagates(t *testing.T) {
	gen := &stubGen{err: errors.New("gateway down")}
	r := NewRunnerWithGenerator(gen, "")
	_, err := r.Run(context.Background(), Request{PersonaName: "ada", Topic: "k8s"})
	if err == nil || !strings.Contains(err.Error(), "gateway down") {
		t.Fatalf("err = %v, want wrapped gateway error", err)
	}
}

func TestRun_ModelFallsBackToDefault(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "sample"}} // no echoed model
	r := NewRunnerWithGenerator(gen, "")                              // no requested model
	out, err := r.Run(context.Background(), Request{PersonaName: "ada", Topic: "k8s"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ModelUsed != DefaultModelUsed {
		t.Fatalf("modelUsed = %q, want %q", out.ModelUsed, DefaultModelUsed)
	}
}

func TestRun_MediumAgnosticWhenKindUnset(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "sample"}}
	r := NewRunnerWithGenerator(gen, "")
	if _, err := r.Run(context.Background(), Request{PersonaName: "ada", Topic: "k8s"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(gen.req.Prompt, "Target medium:") {
		t.Errorf("unset kind should not emit a target medium line:\n%s", gen.req.Prompt)
	}
	if !strings.Contains(gen.req.System, "a few short paragraphs") {
		t.Errorf("unset kind should use the generic brief:\n%s", gen.req.System)
	}
}

func TestRun_NilGenerator(t *testing.T) {
	var r *Runner
	if _, err := r.Run(context.Background(), Request{Topic: "k8s"}); err == nil {
		t.Fatal("expected error from nil runner")
	}
}

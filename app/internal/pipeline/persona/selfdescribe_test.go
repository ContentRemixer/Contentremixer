package persona

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/retrieve"
)

func corpusRetriever(matches []rag.Match, embErr, qErr error) *retrieve.Retriever {
	return retrieve.New(&stubEmbedder{vec: []float32{1, 2, 3}, err: embErr}, &stubStore{ret: matches, err: qErr})
}

func sampleMatches() []rag.Match {
	return []rag.Match{
		{Chunk: rag.Chunk{Text: "rotate like you floss", SourceURL: "https://ex/1"}, Score: 0.9},
		{Chunk: rag.Chunk{Text: "the best abstraction is invisible", SourceURL: "https://ex/2"}, Score: 0.85},
	}
}

func TestDraft_HappyPath(t *testing.T) {
	var gotReq writer.GenerateRequest
	gen := writer.GeneratorFunc(func(_ context.Context, req writer.GenerateRequest) (writer.GenerateResponse, error) {
		gotReq = req
		return writer.GenerateResponse{Content: "  Writes with dry, example-first rigour.  "}, nil
	})
	d := &SelfDescriber{Retriever: corpusRetriever(sampleMatches(), nil, nil), Generate: gen}

	desc, err := d.Draft(context.Background(), "henrik", "henrik-otel", "old description")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc != "Writes with dry, example-first rigour." {
		t.Fatalf("desc not trimmed: %q", desc)
	}
	// The prompt must carry the current description (refine) + the samples.
	if !strings.Contains(gotReq.Prompt, "old description") {
		t.Fatalf("current description not seeded into refine prompt:\n%s", gotReq.Prompt)
	}
	for _, want := range []string{"rotate like you floss", "the best abstraction is invisible"} {
		if !strings.Contains(gotReq.Prompt, want) {
			t.Fatalf("sample %q missing from prompt:\n%s", want, gotReq.Prompt)
		}
	}
	if !strings.Contains(gotReq.System, "voice") {
		t.Fatalf("system prompt should instruct voice analysis:\n%s", gotReq.System)
	}
}

func TestDraft_EmptyCorpusErrors(t *testing.T) {
	gen := writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		t.Fatalf("generator must not be called with an empty corpus")
		return writer.GenerateResponse{}, nil
	})
	d := &SelfDescriber{Retriever: corpusRetriever(nil, nil, nil), Generate: gen}
	if _, err := d.Draft(context.Background(), "henrik", "henrik-otel", ""); err == nil {
		t.Fatalf("expected an error for an empty corpus")
	}
}

func TestDraft_RetrievalErrorPropagates(t *testing.T) {
	gen := writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		return writer.GenerateResponse{Content: "x"}, nil
	})
	d := &SelfDescriber{Retriever: corpusRetriever(nil, errors.New("embed 503"), nil), Generate: gen}
	if _, err := d.Draft(context.Background(), "henrik", "henrik-otel", ""); err == nil {
		t.Fatalf("expected the retrieval error to propagate")
	}
}

func TestRefine_PatchesSelfDescription(t *testing.T) {
	cr := newPersonaCR("henrik-otel", "henrik", "stale description", true)
	cl := fake.NewClientBuilder().WithScheme(personaScheme(t)).WithObjects(cr).Build()
	gen := writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		return writer.GenerateResponse{Content: "Writes with dry, example-first rigour."}, nil
	})
	d := &SelfDescriber{
		Retriever: corpusRetriever(sampleMatches(), nil, nil),
		Generate:  gen,
		Client:    cl,
		Namespace: "contentremixer",
	}

	applied, err := d.Refine(context.Background(), "henrik", "henrik-otel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied != "Writes with dry, example-first rigour." {
		t.Fatalf("applied = %q", applied)
	}
	var got contentremixerv1alpha1.DevRelPersona
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "contentremixer", Name: "henrik-otel"}, &got); err != nil {
		t.Fatalf("get after patch: %v", err)
	}
	if got.Spec.SelfDescription != "Writes with dry, example-first rigour." {
		t.Fatalf("CR self-description not patched: %q", got.Spec.SelfDescription)
	}
}

func TestRefine_OwnerMismatchRefused(t *testing.T) {
	cr := newPersonaCR("henrik-otel", "henrik", "stale", true)
	cl := fake.NewClientBuilder().WithScheme(personaScheme(t)).WithObjects(cr).Build()
	gen := writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		return writer.GenerateResponse{Content: "x"}, nil
	})
	d := &SelfDescriber{Retriever: corpusRetriever(sampleMatches(), nil, nil), Generate: gen, Client: cl, Namespace: "contentremixer"}
	if _, err := d.Refine(context.Background(), "intruder", "henrik-otel"); err == nil {
		t.Fatalf("expected cross-owner refusal")
	}
}

func TestRefine_NoOpWhenIdentical(t *testing.T) {
	const same = "Writes with dry, example-first rigour."
	cr := newPersonaCR("henrik-otel", "henrik", same, true)
	cl := fake.NewClientBuilder().WithScheme(personaScheme(t)).WithObjects(cr).Build()
	gen := writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		return writer.GenerateResponse{Content: same}, nil
	})
	d := &SelfDescriber{Retriever: corpusRetriever(sampleMatches(), nil, nil), Generate: gen, Client: cl, Namespace: "contentremixer"}
	applied, err := d.Refine(context.Background(), "henrik", "henrik-otel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied != same {
		t.Fatalf("applied = %q, want unchanged", applied)
	}
}

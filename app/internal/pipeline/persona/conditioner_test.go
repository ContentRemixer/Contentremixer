package persona

import (
	"context"
	"errors"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/retrieve"
)

// --- stubs ---

type stubProvider struct {
	profile PersonaProfile
	err     error
	calls   int
}

func (p *stubProvider) Lookup(_ context.Context, _, _ string) (PersonaProfile, error) {
	p.calls++
	return p.profile, p.err
}

type stubEmbedder struct {
	vec []float32
	err error
}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	return [][]float32{s.vec}, nil
}

type stubStore struct {
	rag.VectorStore
	got rag.Query
	ret []rag.Match
	err error
}

func (s *stubStore) Query(_ context.Context, q rag.Query) ([]rag.Match, error) {
	s.got = q
	return s.ret, s.err
}

func sample() pipeline.Sample {
	return pipeline.Sample{Body: "how secrets end up in git and the boring fix"}
}

func runCtx() pipeline.RunContext {
	rc := pipeline.RunContext{Notes: "angle: prevention", PersonaName: "henrik-otel"}
	rc.Owner.Subject = "henrik"
	return rc
}

// --- tests ---

func TestCondition_NoPersonaIsStyleOnly(t *testing.T) {
	prov := &stubProvider{}
	c := &Conditioner{Provider: prov, Retriever: retrieve.New(&stubEmbedder{}, &stubStore{})}
	rc := runCtx()
	rc.PersonaName = "" // no attribution
	overlay, err := c.Condition(context.Background(), rc, sample())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if overlay.SelfDescription != "" || len(overlay.Exemplars) != 0 || overlay.Degraded {
		t.Fatalf("expected empty style-only overlay, got %+v", overlay)
	}
	if prov.calls != 0 {
		t.Fatalf("provider called for a persona-less run")
	}
}

func TestCondition_ProviderErrorDegrades(t *testing.T) {
	c := &Conditioner{Provider: &stubProvider{err: errors.New("get devrelpersona: not found")}, Retriever: retrieve.New(&stubEmbedder{}, &stubStore{})}
	overlay, err := c.Condition(context.Background(), runCtx(), sample())
	if err == nil {
		t.Fatalf("expected error surfaced for logging")
	}
	if !overlay.Degraded {
		t.Fatalf("expected Degraded overlay, got %+v", overlay)
	}
}

func TestCondition_NotReadyIsSelfDescriptionOnly(t *testing.T) {
	store := &stubStore{ret: []rag.Match{{Score: 0.9}}} // would return a match if queried
	c := &Conditioner{
		Provider:  &stubProvider{profile: PersonaProfile{SelfDescription: "dry prose", Ready: false}},
		Retriever: retrieve.New(&stubEmbedder{vec: []float32{1}}, store),
	}
	overlay, err := c.Condition(context.Background(), runCtx(), sample())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if overlay.SelfDescription != "dry prose" || !overlay.Degraded || len(overlay.Exemplars) != 0 {
		t.Fatalf("not-Ready persona should be self-description-only+degraded, got %+v", overlay)
	}
	if store.got.OwnerSubject != "" {
		t.Fatalf("retrieval ran against a not-Ready persona index")
	}
}

func TestCondition_FullOverlayAndScope(t *testing.T) {
	store := &stubStore{ret: []rag.Match{
		{Chunk: rag.Chunk{Text: "rotate like you floss", SourceURL: "https://ex/1"}, Score: 0.91},
		{Chunk: rag.Chunk{Text: "", SourceURL: "https://ex/empty"}, Score: 0.5}, // dropped
		{Chunk: rag.Chunk{Text: "best abstraction is invisible", SourceURL: "https://ex/2"}, Score: 0.88},
	}}
	c := &Conditioner{
		Provider:  &stubProvider{profile: PersonaProfile{SelfDescription: "dry prose", TopK: 7, MinScore: 0.3, Ready: true}},
		Retriever: retrieve.New(&stubEmbedder{vec: []float32{1, 2, 3}}, store),
	}
	overlay, err := c.Condition(context.Background(), runCtx(), sample())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if overlay.SelfDescription != "dry prose" || overlay.Degraded {
		t.Fatalf("expected full overlay, got %+v", overlay)
	}
	if len(overlay.Exemplars) != 2 {
		t.Fatalf("expected 2 non-empty exemplars, got %d: %+v", len(overlay.Exemplars), overlay.Exemplars)
	}
	if overlay.Exemplars[0].Text != "rotate like you floss" || overlay.Exemplars[0].Score != 0.91 {
		t.Fatalf("exemplar not mapped: %+v", overlay.Exemplars[0])
	}
	// Scope + tuning passed straight through to the store query.
	if store.got.OwnerSubject != "henrik" || store.got.PersonaName != "henrik-otel" {
		t.Fatalf("tenancy keys not scoped: %+v", store.got)
	}
	if store.got.TopK != 7 || store.got.MinScore != 0.3 {
		t.Fatalf("retrieval tuning not honoured: %+v", store.got)
	}
}

func TestCondition_DefaultTopKWhenUnset(t *testing.T) {
	store := &stubStore{ret: []rag.Match{{Chunk: rag.Chunk{Text: "x"}, Score: 0.9}}}
	c := &Conditioner{
		Provider:  &stubProvider{profile: PersonaProfile{Ready: true}}, // TopK == 0
		Retriever: retrieve.New(&stubEmbedder{vec: []float32{1}}, store),
	}
	if _, err := c.Condition(context.Background(), runCtx(), sample()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.got.TopK != DefaultTopK {
		t.Fatalf("TopK = %d, want default %d", store.got.TopK, DefaultTopK)
	}
}

func TestCondition_RetrievalErrorDegrades(t *testing.T) {
	c := &Conditioner{
		Provider:  &stubProvider{profile: PersonaProfile{SelfDescription: "dry", Ready: true}},
		Retriever: retrieve.New(&stubEmbedder{err: errors.New("embed: 503")}, &stubStore{}),
	}
	overlay, err := c.Condition(context.Background(), runCtx(), sample())
	if err != nil {
		t.Fatalf("retrieval error must not fail the writer, got: %v", err)
	}
	if overlay.SelfDescription != "dry" || !overlay.Degraded || len(overlay.Exemplars) != 0 {
		t.Fatalf("retrieval error should degrade to self-description-only, got %+v", overlay)
	}
}

func TestCondition_SparseCorpusDegrades(t *testing.T) {
	c := &Conditioner{
		Provider:  &stubProvider{profile: PersonaProfile{SelfDescription: "dry", Ready: true}},
		Retriever: retrieve.New(&stubEmbedder{vec: []float32{1}}, &stubStore{ret: nil}),
	}
	overlay, err := c.Condition(context.Background(), runCtx(), sample())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if overlay.SelfDescription != "dry" || !overlay.Degraded || len(overlay.Exemplars) != 0 {
		t.Fatalf("sparse corpus should degrade to self-description-only, got %+v", overlay)
	}
}

func TestCondition_NotWired(t *testing.T) {
	c := &Conditioner{} // no provider/retriever
	overlay, err := c.Condition(context.Background(), runCtx(), sample())
	if err == nil {
		t.Fatalf("expected a wiring error")
	}
	if !overlay.Degraded {
		t.Fatalf("expected Degraded overlay on misconfiguration, got %+v", overlay)
	}
}

func TestBuildSeedTruncatesAndJoins(t *testing.T) {
	seed := buildSeed("body text", "note text", 0) // 0 => no cap
	if seed != "body text\n\nnote text" {
		t.Fatalf("seed = %q", seed)
	}
	if got := buildSeed("abcdef", "", 3); got != "abc" {
		t.Fatalf("truncated seed = %q, want abc", got)
	}
	if got := buildSeed("", "only notes", 100); got != "only notes" {
		t.Fatalf("notes-only seed = %q", got)
	}
}

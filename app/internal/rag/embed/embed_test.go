package embed

import (
	"context"
	"errors"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// stubEmbedder returns a fixed-width vector per input unless Err/Width overrides.
type stubEmbedder struct {
	width int
	calls [][]string
	err   error
}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	s.calls = append(s.calls, texts)
	if s.err != nil {
		return nil, s.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, s.width)
		v[0] = float32(len(texts[i])) // make it text-dependent
		out[i] = v
	}
	return out, nil
}

func chunks(n int) []rag.Chunk {
	cs := make([]rag.Chunk, n)
	for i := range cs {
		cs[i] = rag.Chunk{ID: string(rune('a' + i)), Text: "body"}
	}
	return cs
}

func TestEmbed_FillsAndPreservesOrder(t *testing.T) {
	emb := &stubEmbedder{width: 4}
	st := New(emb, 4)
	out, err := st.Embed(context.Background(), chunks(3))
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d chunks", len(out))
	}
	for i, c := range out {
		if len(c.Embedding) != 4 {
			t.Errorf("chunk %d dim = %d", i, len(c.Embedding))
		}
	}
}

func TestEmbed_DoesNotMutateInput(t *testing.T) {
	in := chunks(2)
	st := New(&stubEmbedder{width: 4}, 4)
	if _, err := st.Embed(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if in[0].Embedding != nil {
		t.Error("input chunk was mutated")
	}
}

func TestEmbed_DimensionMismatchFails(t *testing.T) {
	// embedder returns width 3 but stage expects 1536 default.
	st := New(&stubEmbedder{width: 3}, DefaultDim)
	_, err := st.Embed(context.Background(), chunks(1))
	if err == nil {
		t.Fatal("expected dimension-mismatch error")
	}
}

func TestEmbed_Batching(t *testing.T) {
	emb := &stubEmbedder{width: 2}
	st := New(emb, 2)
	// 200 chunks > maxBatch(96) => 3 batches.
	if _, err := st.Embed(context.Background(), chunks(200)); err != nil {
		t.Fatal(err)
	}
	if len(emb.calls) != 3 {
		t.Errorf("expected 3 batch calls, got %d", len(emb.calls))
	}
	if len(emb.calls[0]) != maxBatch {
		t.Errorf("first batch size = %d, want %d", len(emb.calls[0]), maxBatch)
	}
}

func TestEmbed_EmptyInput(t *testing.T) {
	emb := &stubEmbedder{width: 2}
	out, err := New(emb, 2).Embed(context.Background(), nil)
	if err != nil || out != nil {
		t.Errorf("empty input: out=%v err=%v", out, err)
	}
	if len(emb.calls) != 0 {
		t.Error("embedder should not be called for empty input")
	}
}

func TestEmbed_EmbedderError(t *testing.T) {
	st := New(&stubEmbedder{width: 4, err: errors.New("boom")}, 4)
	if _, err := st.Embed(context.Background(), chunks(1)); err == nil {
		t.Fatal("expected embedder error to propagate")
	}
}

func TestEmbed_NilEmbedder(t *testing.T) {
	if _, err := New(nil, 4).Embed(context.Background(), chunks(1)); err == nil {
		t.Fatal("expected error with nil embedder")
	}
}

func TestNew_DefaultsDim(t *testing.T) {
	if got := New(&stubEmbedder{width: 1}, 0).Dim(); got != DefaultDim {
		t.Errorf("Dim() = %d, want %d", got, DefaultDim)
	}
}

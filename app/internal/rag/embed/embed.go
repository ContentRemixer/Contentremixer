// Package embed is P2-C's (ISI-7146) embed stage (design §3.3/§6): it turns the
// P2-B chunks ([]rag.Chunk with Embedding==nil) into embedded chunks by calling
// the LiteLLM gateway's embeddings surface, exactly like the writer calls it for
// completion (§6: one gateway, one secrets story). It is a pure data hand-off —
// it imports no sibling RAG stage, only the rag contract and the litellm client.
//
// Dimension is a hard contract (§6): every returned vector must have exactly
// Dim components (default 1536 = OpenAI text-embedding-3-small). A mismatch
// fails the index with a clear error — a misconfigured embedding model is
// caught here, at index time, not as silent garbage retrieval later.
package embed

import (
	"context"
	"fmt"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

const (
	// Alias is the ModelProfile litellmName the embed stage requests (design
	// §6: a ModelProfile tagged "embedding" exposes this alias). The retrieval
	// seed (§5.3) MUST request the SAME alias so stored and query vectors come
	// from one model — cosine similarity is otherwise meaningless.
	Alias = "embed"
	// DefaultDim is the embedding dimension the pgvector schema is built for
	// (§4.1/§6): 1536 = text-embedding-3-small. Changing it is a schema
	// migration + full re-index, not a hot swap (§11).
	DefaultDim = 1536
	// maxBatch bounds one gateway embeddings call so a large corpus is embedded
	// in chunks rather than one giant request.
	maxBatch = 96
)

// Embedder produces one vector per input text, in input order. Both the index
// path (this package) and the query path (internal/rag/retrieve) depend on this
// interface, so a stub embedder drives tests and the live LiteLLM-backed
// implementation (NewLiteLLM) guarantees index/query model symmetry (§6).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Stage embeds chunks in place via an Embedder, asserting each vector's
// dimension. The zero value is not usable; call New.
type Stage struct {
	emb Embedder
	dim int
}

// New returns an embed Stage over emb with the given vector dimension. A dim
// <= 0 defaults to DefaultDim.
func New(emb Embedder, dim int) *Stage {
	if dim <= 0 {
		dim = DefaultDim
	}
	return &Stage{emb: emb, dim: dim}
}

// Dim reports the dimension this stage enforces.
func (s *Stage) Dim() int { return s.dim }

// Embed fills Embedding on each chunk (in a fresh slice; the input is not
// mutated) by embedding Chunk.Text through the Embedder. It batches the gateway
// calls and asserts len(vector) == Dim on every result (§6). An empty input
// yields an empty result and no gateway call. The returned chunks preserve
// input order.
func (s *Stage) Embed(ctx context.Context, chunks []rag.Chunk) ([]rag.Chunk, error) {
	if s == nil || s.emb == nil {
		return nil, fmt.Errorf("embed: stage has no Embedder")
	}
	if len(chunks) == 0 {
		return nil, nil
	}

	out := make([]rag.Chunk, len(chunks))
	copy(out, chunks)

	for start := 0; start < len(out); start += maxBatch {
		end := start + maxBatch
		if end > len(out) {
			end = len(out)
		}
		batch := out[start:end]

		texts := make([]string, len(batch))
		for i := range batch {
			texts[i] = batch[i].Text
		}

		vecs, err := s.emb.Embed(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("embed: embedding chunks [%d,%d): %w", start, end, err)
		}
		if len(vecs) != len(batch) {
			return nil, fmt.Errorf("embed: embedder returned %d vectors for %d chunks", len(vecs), len(batch))
		}
		for i := range batch {
			if len(vecs[i]) != s.dim {
				return nil, fmt.Errorf("embed: chunk %q vector dimension %d != expected %d (embedding model misconfigured?)", batch[i].ID, len(vecs[i]), s.dim)
			}
			batch[i].Embedding = vecs[i]
		}
	}
	return out, nil
}

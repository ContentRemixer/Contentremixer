// Package retrieve is P2-C's (ISI-7146) query-time stage (design §4/§5): it
// embeds a retrieval seed with the SAME embedding model/alias the indexer used
// (§6 symmetry) and runs the owner-scoped top-k query against a rag.VectorStore.
// It is the reusable mechanics the P2-D writer-conditioner (ISI-7147) builds the
// PersonaConditioner on top of — the owner/persona tenancy keys are mandatory
// and passed straight through to the store's always-present WHERE predicate.
//
// This package wires embed → store at query time; it imports both, but neither
// imports it, keeping the one-directional stage contract (rag/types.go).
package retrieve

import (
	"context"
	"fmt"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/embed"
)

// Retriever embeds a seed and runs an owner-scoped nearest-neighbour query. The
// Embedder MUST be the same model/alias used to index (embed.NewLiteLLM with
// embed.Alias) or scores are meaningless (§6).
type Retriever struct {
	emb   embed.Embedder
	store rag.VectorStore
}

// New returns a Retriever over emb and store.
func New(emb embed.Embedder, store rag.VectorStore) *Retriever {
	return &Retriever{emb: emb, store: store}
}

// Params scope one retrieval. OwnerSubject and PersonaName are mandatory tenancy
// keys; an empty OwnerSubject is rejected before any embedding or query runs
// (fail closed, §8).
type Params struct {
	OwnerSubject string
	PersonaName  string
	// Seed is the text to embed and search by (the writer's Sample body +
	// notes, §5.3).
	Seed string
	// TopK / MinScore tune the query (the persona's RetrievalSpec, §5).
	TopK     int
	MinScore float64
}

// Retrieve embeds p.Seed and returns the owner-scoped top-k matches. A blank
// seed yields no matches and no embedding call (nothing to search by).
func (r *Retriever) Retrieve(ctx context.Context, p Params) ([]rag.Match, error) {
	if r == nil || r.emb == nil || r.store == nil {
		return nil, fmt.Errorf("retrieve: retriever not fully initialised")
	}
	if strings.TrimSpace(p.OwnerSubject) == "" {
		return nil, fmt.Errorf("retrieve: empty OwnerSubject (refusing cross-tenant retrieval)")
	}
	if strings.TrimSpace(p.PersonaName) == "" {
		return nil, fmt.Errorf("retrieve: empty PersonaName")
	}
	if strings.TrimSpace(p.Seed) == "" {
		return nil, nil
	}

	vecs, err := r.emb.Embed(ctx, []string{p.Seed})
	if err != nil {
		return nil, fmt.Errorf("retrieve: embedding seed: %w", err)
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("retrieve: embedder returned no seed vector")
	}

	return r.store.Query(ctx, rag.Query{
		OwnerSubject: p.OwnerSubject,
		PersonaName:  p.PersonaName,
		Embedding:    vecs[0],
		TopK:         p.TopK,
		MinScore:     p.MinScore,
	})
}

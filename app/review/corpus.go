package review

import (
	"context"
	"fmt"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/embed"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/store"
)

// pgvectorCorpus is the live Corpus: it embeds accepted chunks through the same
// LiteLLM alias used everywhere else (embed.Alias) and upserts them into the
// persona pgvector store (ISI-7146), reusing the frozen replace-by-source
// semantics so an accept is idempotent per source.
type pgvectorCorpus struct {
	stage *embed.Stage
	vs    *store.Store
}

// NewCorpus builds the live accept-time corpus sink. It lives here (app module)
// because it must import the app/internal embed + pgvector stages, which the
// Booth module cannot name directly; Booth calls this public factory and holds
// the result behind the Corpus interface. dsn is the pgvector DSN (the same live
// Postgres, ISI-7148); litellmBaseURL is the gateway origin. The caller owns
// Close via the returned io.Closer-style Close method.
func NewCorpus(ctx context.Context, dsn, litellmBaseURL, apiKey string) (*CorpusHandle, error) {
	vs, err := store.Open(ctx, dsn, store.DefaultDim)
	if err != nil {
		return nil, fmt.Errorf("review: open corpus vector store: %w", err)
	}
	client := &litellm.Client{BaseURL: litellmBaseURL, APIKey: apiKey}
	stage := embed.New(embed.NewLiteLLM(client, embed.Alias), store.DefaultDim)
	return &CorpusHandle{c: &pgvectorCorpus{stage: stage, vs: vs}, vs: vs}, nil
}

// CorpusHandle owns the live corpus resources. It satisfies Corpus and adds
// Close so the Booth binary can release the pool on shutdown.
type CorpusHandle struct {
	c  Corpus
	vs *store.Store
}

// Ingest delegates to the underlying pgvector corpus.
func (h *CorpusHandle) Ingest(ctx context.Context, src ScrapedSource, chunks []ScrapedChunk) error {
	return h.c.Ingest(ctx, src, chunks)
}

// Close releases the underlying vector-store pool.
func (h *CorpusHandle) Close() { h.vs.Close() }

func (p *pgvectorCorpus) Ingest(ctx context.Context, src ScrapedSource, chunks []ScrapedChunk) error {
	if src.OwnerSubject == "" || src.PersonaName == "" {
		return fmt.Errorf("review: corpus ingest needs owner+persona (got owner=%q persona=%q)", src.OwnerSubject, src.PersonaName)
	}
	if len(chunks) == 0 {
		return nil
	}
	ragChunks := make([]rag.Chunk, len(chunks))
	for i, c := range chunks {
		ragChunks[i] = rag.Chunk{
			ID:           c.ID,
			PersonaOwner: src.OwnerSubject,
			PersonaName:  src.PersonaName,
			SourceURL:    src.SourceURL,
			SourceKind:   src.SourceKind,
			Ordinal:      c.Ordinal,
			Text:         c.Text,
			Metadata:     c.Metadata,
		}
	}
	embedded, err := p.stage.Embed(ctx, ragChunks)
	if err != nil {
		return fmt.Errorf("review: embed accepted chunks: %w", err)
	}
	if err := p.vs.Upsert(ctx, embedded); err != nil {
		return fmt.Errorf("review: upsert accepted chunks: %w", err)
	}
	return nil
}

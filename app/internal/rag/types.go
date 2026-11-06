// Package rag holds the cross-stage data contract for the Phase-2 persona
// RAG pipeline (design §3): scrape → chunk → embed → store → retrieve. The
// stages live in sibling subpackages (internal/rag/scrape, internal/rag/chunk,
// and — P2-C — internal/rag/embed, internal/rag/store, internal/rag/retrieve)
// and are pure data hand-offs: a stage NEVER imports another stage. They share
// only the plain-data types and the stage interfaces defined here, so the
// DevRelPersona controller (§2.3) is the sole orchestrator wiring them together.
//
// This package is P2-B's (ISI-7145) half of that contract: it defines RawDoc
// (scrape output), Chunk (chunk output / embed+store input), and the Scraper
// and Chunker interfaces. The embed/store/retrieve types (Match, VectorStore)
// are added by P2-C (ISI-7146) alongside the Embedding field, which P2-B leaves
// nil.
package rag

import (
	"context"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// RawDoc is one fetched source document before chunking (design §3.1). A
// single article/video resolves to one RawDoc; a channel/feed reference fans
// out to many (one per video/post).
type RawDoc struct {
	// SourceURL is the resolved concrete URL of this document. A channel or
	// feed reference expands to many RawDocs each with their own SourceURL.
	SourceURL string
	// SourceKind echoes the PersonaReference.Kind that produced this doc
	// (youtube | article) so downstream stages and stored chunks can trace
	// provenance without re-deriving it from the URL.
	SourceKind string
	// Title is a best-effort human title (video title / <title> / og:title).
	Title string
	// Text is the extracted plain text: the transcript body for youtube, the
	// readable article body (nav/boilerplate stripped) for article.
	Text string
	// FetchedAt is when the fetch completed (metadata, design task line).
	FetchedAt time.Time
	// Meta carries kind-specific details (e.g. video id, publish date,
	// language). Always non-nil on a successful scrape.
	Meta map[string]any
}

// Chunk is one retrievable/embeddable unit (design §3.2). PersonaOwner and
// PersonaName are the tenancy keys (§4/§8); Embedding is left nil by P2-B and
// filled by the P2-C embed stage (§6).
type Chunk struct {
	// ID is deterministic — sha256(owner|persona|source_url|ordinal) hex — so
	// re-indexing the same source yields stable IDs and the §2.3 replace-by-
	// source upsert stays idempotent.
	ID string
	// PersonaOwner is owner.subject, the tenancy key.
	PersonaOwner string
	// PersonaName is the CR metadata.name.
	PersonaName string
	// SourceURL and SourceKind trace the chunk back to its RawDoc.
	SourceURL  string
	SourceKind string
	// Ordinal is the 0-based position of this chunk within its source doc.
	Ordinal int
	// Text is the chunk body.
	Text string
	// Embedding is nil until the P2-C embed stage fills it.
	Embedding []float32
	// Metadata carries forward the RawDoc.Meta plus chunk-level fields (title,
	// fetchedAt) so a stored chunk is self-describing for retrieval/citation.
	Metadata map[string]any
}

// Scraper resolves one PersonaReference to zero-or-more RawDocs (design §3.1).
// Network/parse failure returns an error the controller isolates per-reference
// (§2.3 step 3): one dead link records a ReferenceFailed condition and does not
// sink the persona.
type Scraper interface {
	Scrape(ctx context.Context, ref v1alpha1.PersonaReference) ([]RawDoc, error)
}

// Chunker splits one RawDoc into ordered chunks (design §3.2). It is
// deterministic and side-effect-free: the same RawDoc + owner + persona always
// yields the same chunks (and therefore the same IDs).
type Chunker interface {
	Chunk(doc RawDoc, owner, persona string) []Chunk
}

// Query is an owner-scoped nearest-neighbour search (design §4). OwnerSubject is
// MANDATORY and non-empty — a VectorStore implementation MUST reject an empty
// OwnerSubject rather than scan across tenants (§8 tenancy invariant).
// PersonaName scopes further to one persona.
type Query struct {
	// OwnerSubject is the tenancy scope — REQUIRED, non-empty. An empty value
	// fails closed (error), never a cross-tenant read.
	OwnerSubject string
	// PersonaName is the persona scope — REQUIRED.
	PersonaName string
	// Embedding is the query vector (the writer's retrieval seed, §5), produced
	// by the SAME embedding model/alias as the stored chunks (§6 symmetry).
	Embedding []float32
	// TopK is the maximum number of matches to return.
	TopK int
	// MinScore drops matches below this cosine similarity (0 = keep all top-k).
	MinScore float64
}

// Match is one retrieved chunk with its cosine similarity score in 0..1 (design
// §4): 1 is identical, 0 is orthogonal.
type Match struct {
	Chunk
	Score float64
}

// VectorStore is the storage seam for persona voice chunks (design §4, decision
// #6: pgvector). Owner scoping is enforced in EVERY method — there is no
// unscoped read or delete. A future Qdrant adapter replaces only the
// implementation; this interface is the frozen contract.
type VectorStore interface {
	// Upsert replaces-by-source: for each distinct (PersonaOwner, PersonaName,
	// SourceURL) in chunks, existing rows are deleted then the new chunks
	// inserted (idempotent re-index, §2.3 step 4). Every chunk must carry a
	// non-empty PersonaOwner; an empty one is a programming error and is
	// rejected (fail closed). Each embedding's length must equal the store's
	// configured dimension (§6) or the upsert fails — a misconfigured embedding
	// model is caught at index time, not as silent garbage retrieval.
	Upsert(ctx context.Context, chunks []Chunk) error

	// Query returns the TopK nearest chunks WITHIN (OwnerSubject, PersonaName)
	// only. The owner predicate is applied in SQL, never in Go after a broad
	// scan. Empty OwnerSubject => error (fail closed), never a cross-tenant
	// read.
	Query(ctx context.Context, q Query) ([]Match, error)

	// DeletePersona purges all chunks for one persona (finalizer path, §2.3
	// step 6), scoped to (ownerSubject, personaName). Empty ownerSubject =>
	// error (fail closed).
	DeletePersona(ctx context.Context, ownerSubject, personaName string) error

	// CountPersona returns the stored chunk count for status.chunkCount (§2.2),
	// scoped to (ownerSubject, personaName). Empty ownerSubject => error.
	CountPersona(ctx context.Context, ownerSubject, personaName string) (int, error)
}

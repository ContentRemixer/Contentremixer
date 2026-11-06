package store

import (
	"context"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

// The fail-closed guards return before any pool use, so a nil-pool Store
// exercises them without a database. These assert the §8 tenancy invariant:
// no read or delete is ever issued without a non-empty owner_subject.

func nilStore() *Store { return &Store{dim: DefaultDim} }

func TestQuery_EmptyOwnerFailsClosed(t *testing.T) {
	_, err := nilStore().Query(context.Background(), rag.Query{
		OwnerSubject: "", PersonaName: "p", Embedding: make([]float32, DefaultDim), TopK: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "OwnerSubject") {
		t.Fatalf("expected empty-owner rejection, got %v", err)
	}
}

func TestQuery_EmptyPersonaFailsClosed(t *testing.T) {
	_, err := nilStore().Query(context.Background(), rag.Query{
		OwnerSubject: "o", PersonaName: "", Embedding: make([]float32, DefaultDim),
	})
	if err == nil {
		t.Fatal("expected empty-persona rejection")
	}
}

func TestQuery_DimensionMismatch(t *testing.T) {
	_, err := nilStore().Query(context.Background(), rag.Query{
		OwnerSubject: "o", PersonaName: "p", Embedding: make([]float32, 3),
	})
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("expected dimension rejection, got %v", err)
	}
}

func TestDeletePersona_EmptyOwnerFailsClosed(t *testing.T) {
	if err := nilStore().DeletePersona(context.Background(), "", "p"); err == nil {
		t.Fatal("expected empty-owner rejection")
	}
	if err := nilStore().DeletePersona(context.Background(), "o", ""); err == nil {
		t.Fatal("expected empty-persona rejection")
	}
}

func TestCountPersona_EmptyOwnerFailsClosed(t *testing.T) {
	if _, err := nilStore().CountPersona(context.Background(), "", "p"); err == nil {
		t.Fatal("expected empty-owner rejection")
	}
}

func TestUpsert_RejectsEmptyOwner(t *testing.T) {
	err := nilStore().Upsert(context.Background(), []rag.Chunk{
		{ID: "x", PersonaOwner: "", PersonaName: "p", Embedding: make([]float32, DefaultDim)},
	})
	if err == nil || !strings.Contains(err.Error(), "PersonaOwner") {
		t.Fatalf("expected empty-owner rejection, got %v", err)
	}
}

func TestUpsert_RejectsDimensionMismatch(t *testing.T) {
	err := nilStore().Upsert(context.Background(), []rag.Chunk{
		{ID: "x", PersonaOwner: "o", PersonaName: "p", Embedding: make([]float32, 7)},
	})
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("expected dimension rejection, got %v", err)
	}
}

func TestUpsert_EmptyIsNoop(t *testing.T) {
	if err := nilStore().Upsert(context.Background(), nil); err != nil {
		t.Fatalf("empty upsert should be a no-op, got %v", err)
	}
}

func TestSchemaDDL_HasDimensionAndIndexes(t *testing.T) {
	ddl := schemaDDL(1536)
	for _, want := range []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		"vector(1536)",
		"persona_chunks_embedding_idx",
		"hnsw (embedding vector_cosine_ops)",
		"owner_subject  TEXT         NOT NULL",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("schema DDL missing %q", want)
		}
	}
	if !strings.Contains(schemaDDL(768), "vector(768)") {
		t.Error("schema DDL did not honour custom dimension")
	}
}

func TestNewWithPool_DefaultsDim(t *testing.T) {
	if got := NewWithPool(nil, 0).Dim(); got != DefaultDim {
		t.Errorf("Dim() = %d, want %d", got, DefaultDim)
	}
}

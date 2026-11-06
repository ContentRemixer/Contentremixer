package retrieve

import (
	"context"
	"errors"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

type stubEmbedder struct {
	vec  []float32
	err  error
	seen []string
}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	s.seen = append(s.seen, texts...)
	if s.err != nil {
		return nil, s.err
	}
	return [][]float32{s.vec}, nil
}

type stubStore struct {
	rag.VectorStore
	gotQuery rag.Query
	ret      []rag.Match
	err      error
}

func (s *stubStore) Query(_ context.Context, q rag.Query) ([]rag.Match, error) {
	s.gotQuery = q
	return s.ret, s.err
}

func TestRetrieve_HappyPathPassesScope(t *testing.T) {
	emb := &stubEmbedder{vec: []float32{1, 2, 3}}
	st := &stubStore{ret: []rag.Match{{Score: 0.9}}}
	r := New(emb, st)
	got, err := r.Retrieve(context.Background(), Params{
		OwnerSubject: "alice", PersonaName: "devrel", Seed: "hello", TopK: 7, MinScore: 0.4,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d matches", len(got))
	}
	if st.gotQuery.OwnerSubject != "alice" || st.gotQuery.PersonaName != "devrel" {
		t.Errorf("scope not passed through: %+v", st.gotQuery)
	}
	if st.gotQuery.TopK != 7 || st.gotQuery.MinScore != 0.4 {
		t.Errorf("tuning not passed through: %+v", st.gotQuery)
	}
	if len(st.gotQuery.Embedding) != 3 {
		t.Errorf("seed embedding not forwarded")
	}
}

func TestRetrieve_EmptyOwnerFailsClosed(t *testing.T) {
	r := New(&stubEmbedder{vec: []float32{1}}, &stubStore{})
	_, err := r.Retrieve(context.Background(), Params{OwnerSubject: "", PersonaName: "p", Seed: "x"})
	if err == nil {
		t.Fatal("expected empty-owner rejection")
	}
}

func TestRetrieve_BlankSeedNoCall(t *testing.T) {
	emb := &stubEmbedder{vec: []float32{1}}
	st := &stubStore{}
	got, err := New(emb, st).Retrieve(context.Background(), Params{OwnerSubject: "o", PersonaName: "p", Seed: "   "})
	if err != nil || got != nil {
		t.Errorf("blank seed: got=%v err=%v", got, err)
	}
	if len(emb.seen) != 0 {
		t.Error("embedder should not be called for blank seed")
	}
}

func TestRetrieve_EmbedErrorPropagates(t *testing.T) {
	r := New(&stubEmbedder{err: errors.New("boom")}, &stubStore{})
	if _, err := r.Retrieve(context.Background(), Params{OwnerSubject: "o", PersonaName: "p", Seed: "x"}); err == nil {
		t.Fatal("expected embed error to propagate")
	}
}

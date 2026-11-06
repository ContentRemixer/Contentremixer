package litellm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddings_RequestAndOrder(t *testing.T) {
	var gotModel string
	var gotInput []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EmbeddingsPath {
			t.Errorf("path = %q, want %q", r.URL.Path, EmbeddingsPath)
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		gotInput = body.Input
		// Respond with rows deliberately out of order to exercise the sort.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"text-embedding-3-small","data":[
			{"index":1,"embedding":[0.3,0.4]},
			{"index":0,"embedding":[0.1,0.2]}
		]}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, APIKey: "k"}
	vecs, served, err := c.Embeddings(context.Background(), "embed", "first", "second")
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}
	if gotModel != "embed" {
		t.Errorf("sent model = %q, want embed", gotModel)
	}
	if len(gotInput) != 2 || gotInput[0] != "first" {
		t.Errorf("sent input = %v", gotInput)
	}
	if served != "text-embedding-3-small" {
		t.Errorf("served = %q", served)
	}
	// index 0 => first input, index 1 => second — restored to input order.
	if len(vecs) != 2 || vecs[0][0] != 0.1 || vecs[1][0] != 0.3 {
		t.Errorf("vectors not in input order: %v", vecs)
	}
}

func TestEmbeddings_CountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"model":"m","data":[{"index":0,"embedding":[0.1]}]}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	if _, _, err := c.Embeddings(context.Background(), "embed", "a", "b"); err == nil {
		t.Fatal("expected error on count mismatch")
	}
}

func TestEmbeddings_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad model"}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	_, _, err := c.Embeddings(context.Background(), "embed", "a")
	if err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestEmbeddings_Guards(t *testing.T) {
	if _, _, err := (&Client{}).Embeddings(context.Background(), "m", "a"); err == nil {
		t.Error("expected error with no BaseURL")
	}
	if _, _, err := (&Client{BaseURL: "http://x"}).Embeddings(context.Background(), "m"); err == nil {
		t.Error("expected error with no input")
	}
}

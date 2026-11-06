package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
)

func TestNewLiteLLM_EmbedsViaGateway(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != litellm.EmbeddingsPath {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"model":"text-embedding-3-small","data":[{"index":0,"embedding":[0.1,0.2,0.3]}]}`))
		_ = gotModel
	}))
	defer srv.Close()

	e := NewLiteLLM(&litellm.Client{BaseURL: srv.URL}, "") // empty alias => default Alias
	vecs, err := e.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("unexpected vectors: %v", vecs)
	}
}

func TestNewLiteLLM_NilClient(t *testing.T) {
	e := NewLiteLLM(nil, Alias)
	if _, err := e.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected error with nil client")
	}
}

func TestNewLiteLLM_DefaultsAlias(t *testing.T) {
	e := NewLiteLLM(&litellm.Client{BaseURL: "http://x"}, "").(*litellmEmbedder)
	if e.alias != Alias {
		t.Errorf("alias = %q, want %q", e.alias, Alias)
	}
}

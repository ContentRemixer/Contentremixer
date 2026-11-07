package embed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
)

// TestLiteLLMEmbedder_Embed drives the gateway-backed embedder over httptest so
// the §4.4 instrumentation lines (duration + gateway-error class) run on both a
// 200 and a 429 path. The default no-op telemetry provider makes the recording
// calls inert; this exercises them for coverage and asserts data pass-through.
func TestLiteLLMEmbedder_Embed(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"m","data":[{"index":0,"embedding":[0.1,0.2]}]}`))
		}))
		defer srv.Close()
		e := NewLiteLLM(&litellm.Client{BaseURL: srv.URL}, "embed")
		vecs, err := e.Embed(context.Background(), []string{"hi"})
		if err != nil {
			t.Fatalf("Embed: %v", err)
		}
		if len(vecs) != 1 || vecs[0][0] != 0.1 {
			t.Errorf("vecs = %v", vecs)
		}
	})
	t.Run("gateway 429", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
		}))
		defer srv.Close()
		e := NewLiteLLM(&litellm.Client{BaseURL: srv.URL}, "embed")
		if _, err := e.Embed(context.Background(), []string{"hi"}); err == nil {
			t.Fatal("expected gateway error, got nil")
		}
	})
	t.Run("nil client", func(t *testing.T) {
		e := &litellmEmbedder{}
		if _, err := e.Embed(context.Background(), []string{"x"}); err == nil {
			t.Fatal("expected error on nil client")
		}
	})
}

func TestClassifyGatewayErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want metricconv.HttpStatusClass
	}{
		{"429 from gateway", fmt.Errorf("litellm: gateway error status 429: rate limited"), metricconv.HttpStatusClassRateLimited},
		{"5xx from gateway", fmt.Errorf("litellm: gateway error status 503: upstream down"), metricconv.HttpStatusClassServerError},
		{"4xx from gateway", fmt.Errorf("litellm: gateway error status 400: bad request"), metricconv.HttpStatusClassClientError},
		{"context deadline", fmt.Errorf("litellm: calling gateway: %w", context.DeadlineExceeded), metricconv.HttpStatusClassTimeout},
		{"transport failure", errors.New("litellm: calling gateway: dial tcp: connection refused"), metricconv.HttpStatusClassTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, errType := classifyGatewayErr(c.err)
			if got != c.want {
				t.Errorf("class = %q, want %q", got, c.want)
			}
			if errType != string(c.want) {
				t.Errorf("errType = %q, want %q", errType, c.want)
			}
		})
	}
}

func TestEmbedderModel(t *testing.T) {
	// litellmEmbedder exposes its alias via Model().
	le := NewLiteLLM(nil, "my-embed-alias")
	if m, ok := le.(interface{ Model() string }); !ok || m.Model() != "my-embed-alias" {
		t.Errorf("litellmEmbedder.Model() = %v, want my-embed-alias", le)
	}
	if got := embedderModel(le); got != "my-embed-alias" {
		t.Errorf("embedderModel(litellm) = %q, want my-embed-alias", got)
	}
	// A stub embedder without Model() falls back to the default Alias.
	if got := embedderModel(&stubEmbedder{}); got != Alias {
		t.Errorf("embedderModel(stub) = %q, want %q", got, Alias)
	}
}

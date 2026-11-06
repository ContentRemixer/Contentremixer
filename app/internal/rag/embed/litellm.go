package embed

import (
	"context"
	"fmt"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
)

// litellmEmbedder adapts a *litellm.Client to the Embedder interface, always
// requesting a fixed model alias so index and query vectors come from the same
// model (§6 symmetry).
type litellmEmbedder struct {
	client *litellm.Client
	alias  string
}

// NewLiteLLM returns an Embedder backed by the LiteLLM gateway, requesting the
// given model alias (use Alias for the standard "embedding"-tagged profile). It
// is the production Embedder; tests inject a stub implementing Embedder.
func NewLiteLLM(client *litellm.Client, alias string) Embedder {
	if alias == "" {
		alias = Alias
	}
	return &litellmEmbedder{client: client, alias: alias}
}

func (e *litellmEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("embed: nil LiteLLM client")
	}
	vecs, _, err := e.client.Embeddings(ctx, e.alias, texts...)
	if err != nil {
		return nil, err
	}
	return vecs, nil
}

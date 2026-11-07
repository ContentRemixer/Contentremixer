// Embeddings surface of the LiteLLM gateway client (design §6: the embed stage
// and the retrieval seed call the SAME gateway the writer uses for completion —
// one gateway, one secrets story). The embedding model is just another
// ModelProfile tagged "embedding" with its own litellmName alias; this method
// requests that alias exactly like ChatCompletion requests a chat model.
//
// It speaks the OpenAI-compatible /v1/embeddings surface the LiteLLM proxy
// serves on ServingPort.
package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// EmbeddingsPath is the OpenAI-compatible embeddings path on the gateway.
const EmbeddingsPath = "/v1/embeddings"

// Embeddings returns one vector per input string, in input order, plus the
// model the gateway served (the response's echoed "model" field — the alias the
// indexer and the retrieval seed MUST share for cosine similarity to be
// meaningful, §6 query/index symmetry). An empty model takes the gateway
// default, but the embed stage always passes an explicit embedding alias so the
// stored and query vectors come from the same model.
func (c *Client) Embeddings(ctx context.Context, model string, inputs ...string) ([][]float32, string, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return nil, "", fmt.Errorf("litellm: no gateway BaseURL configured")
	}
	if len(inputs) == 0 {
		return nil, "", fmt.Errorf("litellm: embeddings called with no input")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}

	payload := map[string]any{"input": inputs}
	if strings.TrimSpace(model) != "" {
		payload["model"] = strings.TrimSpace(model)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("litellm: encoding embeddings request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+EmbeddingsPath, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("litellm: building embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("litellm: calling gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", fmt.Errorf("litellm: reading embeddings response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("litellm: gateway error status %d: %s", resp.StatusCode, errorDetail(raw))
	}

	var out struct {
		Model string `json:"model"`
		Data  []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", fmt.Errorf("litellm: decoding embeddings response: %w", err)
	}
	if len(out.Data) != len(inputs) {
		return nil, "", fmt.Errorf("litellm: embeddings response count %d != %d inputs", len(out.Data), len(inputs))
	}

	// The spec allows data out of order; sort by Index so vectors line up with
	// inputs positionally.
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Index < out.Data[j].Index })
	vecs := make([][]float32, len(out.Data))
	for i, d := range out.Data {
		if len(d.Embedding) == 0 {
			return nil, "", fmt.Errorf("litellm: embeddings response row %d has empty vector", i)
		}
		vecs[i] = d.Embedding
	}
	return vecs, out.Model, nil
}

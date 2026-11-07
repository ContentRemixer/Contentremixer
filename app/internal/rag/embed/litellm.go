package embed

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
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

// Model reports the embedding model alias this embedder requests. The embed
// Stage uses it (via the optional modeler interface) to label the §4.4
// dimension-mismatch metric and the rag.embed span with gen_ai.request.model.
func (e *litellmEmbedder) Model() string { return e.alias }

func (e *litellmEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e == nil || e.client == nil {
		return nil, fmt.Errorf("embed: nil LiteLLM client")
	}
	// §4.4 GenAI semconv duration + gateway-error class, recorded at the layer
	// that knows the model alias and the gateway status.
	tel := telemetry.Default()
	started := time.Now()
	vecs, _, err := e.client.Embeddings(ctx, e.alias, texts...)
	dur := time.Since(started)
	if err != nil {
		class, errType := classifyGatewayErr(err)
		tel.RecordEmbedGatewayError(ctx, e.alias, class)
		tel.RecordGenAIEmbedding(ctx, e.alias, dur, errType)
		return nil, err
	}
	tel.RecordGenAIEmbedding(ctx, e.alias, dur, "")
	return vecs, nil
}

// gatewayStatusRe pulls the HTTP status out of the litellm client's error
// message ("litellm: gateway error status %d: ...").
var gatewayStatusRe = regexp.MustCompile(`gateway error status (\d{3})`)

// classifyGatewayErr maps a LiteLLM embeddings error to the §4.4 status-class
// enum and the GenAI error.type label. An HTTP status in the message classifies
// by code; a context deadline or a transport failure (no response) is a
// client-side timeout.
func classifyGatewayErr(err error) (metricconv.HttpStatusClass, string) {
	if m := gatewayStatusRe.FindStringSubmatch(err.Error()); m != nil {
		if code, perr := strconv.Atoi(m[1]); perr == nil {
			if class, ok := telemetry.ClassifyHTTPStatus(code); ok {
				return class, string(class)
			}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return metricconv.HttpStatusClassTimeout, string(metricconv.HttpStatusClassTimeout)
	}
	// Transport failure / no response — the enum's "timeout" bucket (§4.4).
	return metricconv.HttpStatusClassTimeout, string(metricconv.HttpStatusClassTimeout)
}

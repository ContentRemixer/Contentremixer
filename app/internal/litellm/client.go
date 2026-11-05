// LiteLLM gateway chat client (design §7: the pipeline stages call models
// through the LiteLLM gateway, never a provider directly). Shipped with the
// writer integration (subtask F); the config renderer is subtask C.
//
// The client speaks the OpenAI-compatible /v1/chat/completions surface the
// LiteLLM proxy serves on ServingPort. An empty model in a request is
// omitted so the gateway default (general_settings.default_model, rendered
// from the ModelProfile with spec.default=true) resolves it; the served
// model is echoed back and returned for Sample clearance (design §5).
package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatPath is the OpenAI-compatible chat-completions path on the gateway.
const ChatPath = "/v1/chat/completions"

// DefaultTimeout bounds one chat call.
const DefaultTimeout = 5 * time.Minute

// ChatMessage is one OpenAI-style chat message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client is a minimal chat-completions client for the LiteLLM gateway.
type Client struct {
	// BaseURL is the gateway origin, e.g. "http://litellm:4000".
	BaseURL string
	// APIKey is sent as the Bearer token (the gateway key, not a provider
	// key — providers authenticate via the env refs rendered by subtask C).
	// May be empty for an unauthenticated in-cluster gateway.
	APIKey string
	// HTTP is the transport; a client with DefaultTimeout is used when nil.
	HTTP *http.Client
}

// ChatCompletion sends the messages and returns the completion text plus
// the model the gateway served (the response's echoed "model" field —
// what Mix.ModelUsed records for Sample clearance).
func (c *Client) ChatCompletion(ctx context.Context, model string, messages ...ChatMessage) (string, string, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" {
		return "", "", fmt.Errorf("litellm: no gateway BaseURL configured")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}

	payload := map[string]any{"messages": messages}
	if strings.TrimSpace(model) != "" {
		payload["model"] = strings.TrimSpace(model)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("litellm: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+ChatPath, bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("litellm: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("litellm: calling gateway: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", "", fmt.Errorf("litellm: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("litellm: gateway error status %d: %s", resp.StatusCode, errorDetail(raw))
	}

	var completion struct {
		Model   string `json:"model"`
		Choices []struct {
			Message ChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &completion); err != nil {
		return "", "", fmt.Errorf("litellm: decoding response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", "", fmt.Errorf("litellm: response carries no completion: %s", truncate(raw, 512))
	}
	text := completion.Choices[0].Message.Content
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("litellm: response choice has empty content")
	}
	return text, completion.Model, nil
}

// Complete is the two-prompt convenience form: one system + one user
// message. Model may be empty to take the gateway default.
func (c *Client) Complete(ctx context.Context, model, system, user string) (string, string, error) {
	return c.ChatCompletion(ctx, model,
		ChatMessage{Role: "system", Content: system},
		ChatMessage{Role: "user", Content: user},
	)
}

// errorDetail extracts a LiteLLM/OpenAI error envelope message when
// present, else truncates the raw body.
func errorDetail(raw []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && strings.TrimSpace(envelope.Error.Message) != "" {
		return envelope.Error.Message
	}
	return truncate(raw, 512)
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

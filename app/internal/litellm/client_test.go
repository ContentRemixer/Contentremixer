package litellm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// servingGateway decodes the incoming request and replies with a canned
// completion echoing the served model, capturing the request for asserts.
type servingGateway struct {
	server *httptest.Server

	gotAuth      string
	gotModel     any // JSON "model" value (nil when omitted)
	gotMessages  []ChatMessage
	gotPath      string
	status       int
	responseBody string
}

func newServingGateway(t *testing.T, status int, body string) *servingGateway {
	t.Helper()
	g := &servingGateway{status: status, responseBody: body}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		g.gotPath = r.URL.Path
		g.gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		var decoded struct {
			Model    any           `json:"model"`
			Messages []ChatMessage `json:"messages"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Errorf("gateway: undecodable request: %v", err)
		}
		g.gotModel = decoded.Model
		g.gotMessages = decoded.Messages
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(g.status)
		_, _ = w.Write([]byte(g.responseBody))
	})
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func TestClientChatCompletion(t *testing.T) {
	g := newServingGateway(t, http.StatusOK, `{
		"model": "anthropic/claude-opus-4",
		"choices": [{"message": {"role": "assistant", "content": "# Draft\n\nBody."}}]
	}`)
	c := &Client{BaseURL: g.server.URL, APIKey: "sk-master"}

	content, model, err := c.ChatCompletion(context.Background(), "default",
		ChatMessage{Role: "system", Content: "sys"},
		ChatMessage{Role: "user", Content: "prompt"})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if content != "# Draft\n\nBody." || model != "anthropic/claude-opus-4" {
		t.Fatalf("got (%q, %q)", content, model)
	}
	if g.gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q", g.gotPath)
	}
	if g.gotAuth != "Bearer sk-master" {
		t.Fatalf("Authorization = %q", g.gotAuth)
	}
	if g.gotModel != "default" {
		t.Fatalf("model = %#v, want the alias", g.gotModel)
	}
	if len(g.gotMessages) != 2 || g.gotMessages[0].Role != "system" || g.gotMessages[1].Content != "prompt" {
		t.Fatalf("messages = %+v", g.gotMessages)
	}
}

func TestClientOmitsEmptyModelForGatewayDefault(t *testing.T) {
	g := newServingGateway(t, http.StatusOK, `{"model":"m","choices":[{"message":{"content":"x"}}]}`)
	c := &Client{BaseURL: g.server.URL}
	if _, _, err := c.Complete(context.Background(), "", "s", "p"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if g.gotModel != nil {
		t.Fatalf("empty model must be omitted so the gateway serves default_model; got %#v", g.gotModel)
	}
}

func TestClientCompleteShapesMessages(t *testing.T) {
	g := newServingGateway(t, http.StatusOK, `{"model":"m","choices":[{"message":{"content":"ok"}}]}`)
	c := &Client{BaseURL: g.server.URL}
	content, _, err := c.Complete(context.Background(), "alias", "system text", "user text")
	if err != nil || content != "ok" {
		t.Fatalf("Complete = (%q, %v)", content, err)
	}
	if len(g.gotMessages) != 2 || g.gotMessages[0] != (ChatMessage{Role: "system", Content: "system text"}) ||
		g.gotMessages[1] != (ChatMessage{Role: "user", Content: "user text"}) {
		t.Fatalf("messages = %+v", g.gotMessages)
	}
}

func TestClientErrorPaths(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		wantSub    string
	}{
		{"gateway 503 with error envelope", `{"error":{"message":"no healthy deployment"}}`, 503, "no healthy deployment"},
		{"gateway 401 bare", `unauthorized`, 401, "status 401"},
		{"empty choices", `{"model":"m","choices":[]}`, 200, "no completion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newServingGateway(t, tc.status, tc.body)
			c := &Client{BaseURL: g.server.URL}
			_, _, err := c.Complete(context.Background(), "", "s", "p")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestClientRequiresBaseURL(t *testing.T) {
	c := &Client{}
	if _, _, err := c.Complete(context.Background(), "", "s", "p"); err == nil || !strings.Contains(err.Error(), "no gateway BaseURL") {
		t.Fatalf("err = %v, want missing BaseURL failure", err)
	}
}

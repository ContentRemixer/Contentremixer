package promote

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
)

// stubGen records the request and returns a scripted response.
type stubGen struct {
	req  writer.GenerateRequest
	resp writer.GenerateResponse
	err  error
}

func (s *stubGen) Generate(_ context.Context, req writer.GenerateRequest) (writer.GenerateResponse, error) {
	s.req = req
	return s.resp, s.err
}

func TestSuggest_WeavesStyleContentAndMedium(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{
		Content: "[HOOK] Shipping is scary.\n[BODY] Here is how we made it boring.\nTwo deploys a day now.\n[CTA] Read the full post → https://x/y #devops",
		Model:   "gpt-4o",
	}}
	w := NewWriterWithGenerator(gen, "alias-x")
	out, err := w.Suggest(context.Background(), Request{
		PersonaName:        "ada",
		Content:            "A long blog about zero-downtime deploys.",
		Title:              "Zero-downtime deploys",
		URL:                "https://x/y",
		PrimaryContentKind: "blog",
		StyleTraits:        "Dry wit, short sentences.",
		SelfDescription:    "I explain infra like a friend.",
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Hook != "Shipping is scary." {
		t.Fatalf("hook = %q", out.Hook)
	}
	if !strings.Contains(out.Body, "boring") || !strings.Contains(out.Body, "Two deploys a day now.") {
		t.Fatalf("body = %q", out.Body)
	}
	if out.CTA != "Read the full post → https://x/y #devops" {
		t.Fatalf("cta = %q", out.CTA)
	}
	// Post reassembles every part, in order.
	if out.Post != out.Hook+"\n\n"+out.Body+"\n\n"+out.CTA {
		t.Fatalf("post not assembled from parts: %q", out.Post)
	}
	if out.ModelUsed != "gpt-4o" {
		t.Fatalf("modelUsed = %q, want gpt-4o", out.ModelUsed)
	}
	if gen.req.Model != "alias-x" {
		t.Fatalf("requested model = %q, want alias-x", gen.req.Model)
	}
	for _, want := range []string{"Dry wit, short sentences.", "I explain infra like a friend.", "LinkedIn", "blog post", "include the link"} {
		if !strings.Contains(gen.req.System, want) {
			t.Errorf("system prompt missing %q:\n%s", want, gen.req.System)
		}
	}
	for _, want := range []string{"A long blog about zero-downtime deploys.", "Zero-downtime deploys", "https://x/y"} {
		if !strings.Contains(gen.req.Prompt, want) {
			t.Errorf("user prompt missing %q:\n%s", want, gen.req.Prompt)
		}
	}
}

// A model that ignores the markers still yields a usable decomposition and loses
// no text.
func TestSuggest_DecomposesUnmarkedParagraphs(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "The hook line.\n\nThe middle thought.\n\nThe second middle.\n\nRead it here.", Model: "m"}}
	w := NewWriterWithGenerator(gen, "")
	out, err := w.Suggest(context.Background(), Request{PersonaName: "ada", Content: "c"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Hook != "The hook line." {
		t.Fatalf("hook = %q", out.Hook)
	}
	if out.CTA != "Read it here." {
		t.Fatalf("cta = %q", out.CTA)
	}
	if !strings.Contains(out.Body, "The middle thought.") || !strings.Contains(out.Body, "The second middle.") {
		t.Fatalf("body = %q", out.Body)
	}
}

// A single blob with no markers and no blank lines becomes the body — never dropped.
func TestSuggest_SingleBlobBecomesBody(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "just one line of promo copy", Model: "m"}}
	w := NewWriterWithGenerator(gen, "")
	out, err := w.Suggest(context.Background(), Request{PersonaName: "ada", Content: "c"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Body != "just one line of promo copy" || out.Post != "just one line of promo copy" {
		t.Fatalf("body=%q post=%q", out.Body, out.Post)
	}
}

// Marked output where only HOOK and CTA come back, plus stray text before the
// first marker, folds the stray text into the body (no loss).
func TestSuggest_StrayTextBeforeMarkersFoldsIntoBody(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "Here is your post:\n[HOOK] Big news.\n[CTA] Go read it.", Model: "m"}}
	w := NewWriterWithGenerator(gen, "")
	out, err := w.Suggest(context.Background(), Request{PersonaName: "ada", Content: "c"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Hook != "Big news." || out.CTA != "Go read it." {
		t.Fatalf("hook=%q cta=%q", out.Hook, out.CTA)
	}
	if !strings.Contains(out.Body, "Here is your post:") {
		t.Fatalf("stray preamble not preserved in body: %q", out.Body)
	}
}

func TestSuggest_EmptyContentFailsClosed(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "x"}}
	w := NewWriterWithGenerator(gen, "")
	if _, err := w.Suggest(context.Background(), Request{PersonaName: "ada", Content: "   "}); err == nil {
		t.Fatal("expected empty-content error")
	}
	if gen.req.Prompt != "" {
		t.Fatal("generator must not run on empty content")
	}
}

func TestSuggest_EmptyCompletionFailsClosed(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "   "}}
	w := NewWriterWithGenerator(gen, "")
	if _, err := w.Suggest(context.Background(), Request{PersonaName: "ada", Content: "c"}); err == nil {
		t.Fatal("expected empty-completion error")
	}
}

func TestSuggest_GeneratorErrorPropagates(t *testing.T) {
	gen := &stubGen{err: errors.New("gateway down")}
	w := NewWriterWithGenerator(gen, "")
	if _, err := w.Suggest(context.Background(), Request{PersonaName: "ada", Content: "c"}); err == nil {
		t.Fatal("expected generator error")
	}
}

func TestSuggest_NilGeneratorFailsClosed(t *testing.T) {
	var w *Writer
	if _, err := w.Suggest(context.Background(), Request{Content: "c"}); err == nil {
		t.Fatal("expected no-generator error")
	}
}

// Model unreported → request alias used; neither set → DefaultModelUsed.
func TestSuggest_ModelFallback(t *testing.T) {
	gen := &stubGen{resp: writer.GenerateResponse{Content: "[BODY] x"}}
	if out, _ := NewWriterWithGenerator(gen, "alias-z").Suggest(context.Background(), Request{Content: "c"}); out.ModelUsed != "alias-z" {
		t.Fatalf("modelUsed = %q, want alias-z", out.ModelUsed)
	}
	gen2 := &stubGen{resp: writer.GenerateResponse{Content: "[BODY] x"}}
	if out, _ := NewWriterWithGenerator(gen2, "").Suggest(context.Background(), Request{Content: "c"}); out.ModelUsed != DefaultModelUsed {
		t.Fatalf("modelUsed = %q, want %q", out.ModelUsed, DefaultModelUsed)
	}
}

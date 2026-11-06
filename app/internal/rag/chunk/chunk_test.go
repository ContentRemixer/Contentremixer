package chunk

import (
	"strings"
	"testing"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
)

func doc(text string) rag.RawDoc {
	return rag.RawDoc{
		SourceURL:  "https://example.com/post",
		SourceKind: "article",
		Title:      "A Post",
		Text:       text,
		FetchedAt:  time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC),
		Meta:       map[string]any{"author": "DevRel"},
	}
}

func TestChunkDeterministicIDs(t *testing.T) {
	c := New()
	body := strings.Repeat("This is a sentence about developer relations and metrics. ", 60)
	d := doc(body)

	a := c.Chunk(d, "sub-123", "persona-x")
	b := c.Chunk(d, "sub-123", "persona-x")
	if len(a) != len(b) {
		t.Fatalf("non-deterministic chunk count: %d vs %d", len(a), len(b))
	}
	if len(a) < 2 {
		t.Fatalf("expected multiple chunks for long doc, got %d", len(a))
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Errorf("chunk %d ID differs across runs: %s vs %s", i, a[i].ID, b[i].ID)
		}
		if a[i].Ordinal != i {
			t.Errorf("chunk %d has ordinal %d", i, a[i].Ordinal)
		}
		if a[i].Embedding != nil {
			t.Errorf("chunk %d has non-nil embedding (P2-B must leave it nil)", i)
		}
	}
	// Different persona/owner → different IDs (tenancy in the id).
	other := c.Chunk(d, "sub-999", "persona-x")
	if other[0].ID == a[0].ID {
		t.Errorf("chunk ID not scoped by owner")
	}
}

func TestChunkTenancyAndMetadata(t *testing.T) {
	c := New()
	chunks := c.Chunk(doc("One sentence here. Two sentence here."), "owner-1", "persona-1")
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	ch := chunks[0]
	if ch.PersonaOwner != "owner-1" || ch.PersonaName != "persona-1" {
		t.Errorf("tenancy keys wrong: %+v", ch)
	}
	if ch.SourceURL != "https://example.com/post" || ch.SourceKind != "article" {
		t.Errorf("source provenance wrong: %+v", ch)
	}
	if ch.Metadata["title"] != "A Post" {
		t.Errorf("title not carried: %v", ch.Metadata["title"])
	}
	if ch.Metadata["author"] != "DevRel" {
		t.Errorf("RawDoc.Meta not carried: %v", ch.Metadata["author"])
	}
	if ch.Metadata["fetchedAt"] != "2026-11-06T12:00:00Z" {
		t.Errorf("fetchedAt not carried: %v", ch.Metadata["fetchedAt"])
	}
}

func TestChunkNeverSplitsMidSentence(t *testing.T) {
	c := &Chunker{TargetTokens: 20, OverlapTokens: 4}
	body := "Alpha beta gamma delta epsilon zeta eta theta. " +
		"Iota kappa lambda mu nu xi omicron pi. " +
		"Rho sigma tau upsilon phi chi psi omega."
	chunks := c.Chunk(doc(body), "o", "p")
	if len(chunks) < 2 {
		t.Fatalf("expected split, got %d chunks", len(chunks))
	}
	for _, ch := range chunks {
		// Every chunk must end at a sentence terminator (no mid-sentence cut).
		trimmed := strings.TrimSpace(ch.Text)
		last := trimmed[len(trimmed)-1]
		if last != '.' && last != '!' && last != '?' {
			t.Errorf("chunk does not end on sentence boundary: %q", ch.Text)
		}
	}
}

func TestChunkOverlap(t *testing.T) {
	c := &Chunker{TargetTokens: 16, OverlapTokens: 8}
	body := "Sentence one about metrics. Sentence two about traces. " +
		"Sentence three about logs. Sentence four about spans. Sentence five about dashboards."
	chunks := c.Chunk(doc(body), "o", "p")
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// Consecutive chunks should share a trailing/leading sentence (overlap).
	shared := false
	for i := 1; i < len(chunks); i++ {
		prevSents := strings.Split(chunks[i-1].Text, ". ")
		if len(prevSents) > 0 {
			tail := strings.TrimSpace(prevSents[len(prevSents)-1])
			if tail != "" && strings.Contains(chunks[i].Text, tail) {
				shared = true
			}
		}
	}
	if !shared {
		t.Errorf("no overlap detected between consecutive chunks: %+v", chunkTexts(chunks))
	}
}

func TestChunkOversizedSentence(t *testing.T) {
	c := &Chunker{TargetTokens: 5, OverlapTokens: 1}
	long := "This single sentence is deliberately much longer than the tiny token budget allotted."
	chunks := c.Chunk(doc("Short one. "+long+" Short two."), "o", "p")
	// The oversized sentence survives intact as its own chunk.
	found := false
	for _, ch := range chunks {
		if strings.Contains(ch.Text, "deliberately much longer") {
			found = true
			if !strings.HasPrefix(strings.TrimSpace(ch.Text), "This single sentence") {
				t.Errorf("oversized sentence was split: %q", ch.Text)
			}
		}
	}
	if !found {
		t.Errorf("oversized sentence missing from output")
	}
}

func TestChunkEmpty(t *testing.T) {
	c := New()
	if got := c.Chunk(doc("   \n\n  "), "o", "p"); got != nil {
		t.Errorf("empty doc should yield no chunks, got %d", len(got))
	}
}

func TestChunkParagraphBoundaries(t *testing.T) {
	// A newline inside a paragraph joins; a blank line separates sentences.
	c := New()
	chunks := c.Chunk(doc("Line one\ncontinues here. Next sentence.\n\nNew paragraph entirely."), "o", "p")
	if len(chunks) != 1 {
		t.Fatalf("short doc should be one chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "Line one continues here.") {
		t.Errorf("intra-paragraph newline not joined: %q", chunks[0].Text)
	}
}

func chunkTexts(chunks []rag.Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Text
	}
	return out
}

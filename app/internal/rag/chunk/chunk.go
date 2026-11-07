// Package chunk is P2-B's (ISI-7145) chunking stage (design §3.2): it splits
// one rag.RawDoc into ordered, deterministic rag.Chunks ready for the P2-C
// embed stage. It fills no embeddings (text only) and produces stable IDs
// (sha256(owner|persona|source_url|ordinal)) so the §2.3 replace-by-source
// upsert stays idempotent across re-indexes.
//
// Splitting snaps to sentence/paragraph boundaries and never cuts mid-sentence:
// sentences are accumulated until the chunk reaches a target token budget, then
// emitted; the next chunk re-includes a trailing overlap of the previous one so
// retrieval context is not lost at a boundary. Token counts are an estimate
// (word-based), per the design's "~512/64 is a tunable heuristic, not load-
// bearing; the contract is stable ordered chunks with deterministic IDs."
package chunk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
)

// Tunable chunk budget (design §3.2 "~512 tokens with ~64-token overlap").
const (
	DefaultTargetTokens  = 512
	DefaultOverlapTokens = 64
	// tokensPerWord estimates BPE tokens from whitespace-delimited words
	// (~1.33 tokens/word for English prose) so we need no tokenizer dependency.
	tokensPerWord = 4.0 / 3.0
)

// Chunker is the rag.Chunker implementation.
type Chunker struct {
	// TargetTokens is the soft maximum token budget per chunk. Defaults to
	// DefaultTargetTokens when <= 0.
	TargetTokens int
	// OverlapTokens is how much trailing context each chunk re-includes from
	// the previous one. Defaults to DefaultOverlapTokens when < 0; clamped
	// below TargetTokens.
	OverlapTokens int
}

var _ rag.Chunker = (*Chunker)(nil)

// New returns a Chunker with the default budget.
func New() *Chunker {
	return &Chunker{TargetTokens: DefaultTargetTokens, OverlapTokens: DefaultOverlapTokens}
}

func (c *Chunker) target() int {
	if c.TargetTokens <= 0 {
		return DefaultTargetTokens
	}
	return c.TargetTokens
}

func (c *Chunker) overlap() int {
	ov := c.OverlapTokens
	if ov < 0 {
		ov = DefaultOverlapTokens
	}
	if ov >= c.target() {
		ov = c.target() / 2 // never overlap more than half a chunk
	}
	return ov
}

// Chunk splits doc into ordered chunks for (owner, persona). Deterministic: the
// same inputs always yield the same chunks and IDs. An empty/whitespace doc
// yields no chunks.
//
// It is a sentence-granular sliding window: each chunk accumulates whole
// sentences until adding the next would exceed the target token budget (a
// single sentence over budget becomes its own chunk — never split
// mid-sentence), then the window advances, pulling back a trailing overlap of
// whole sentences so retrieval context survives the boundary. The window
// always advances by at least one sentence, so termination is guaranteed.
func (c *Chunker) Chunk(doc rag.RawDoc, owner, persona string) []rag.Chunk {
	sentences := splitSentences(doc.Text)
	if len(sentences) == 0 {
		// §4.3: a doc yielding 0 chunks is itself a signal — record it. The
		// Chunker contract carries no context (deterministic, side-effect-free),
		// so metrics use Background and the rag.chunk span is orchestrator-owned.
		telemetry.Default().RecordChunking(context.Background(), metricconv.ReferenceKind(doc.SourceKind), 0, nil)
		return nil
	}
	toks := make([]int, len(sentences))
	for i, s := range sentences {
		toks[i] = estimateTokens(s)
	}
	target := c.target()
	overlap := c.overlap()

	var chunks []rag.Chunk
	ordinal := 0
	for start := 0; start < len(sentences); {
		// Grow the window; always include at least the first sentence.
		end, sum := start, 0
		for end < len(sentences) {
			if end > start && sum+toks[end] > target {
				break
			}
			sum += toks[end]
			end++
		}
		text := strings.Join(sentences[start:end], " ")
		chunks = append(chunks, c.newChunk(doc, owner, persona, ordinal, text))
		ordinal++
		if end >= len(sentences) {
			break
		}
		// Advance, carrying a trailing overlap of whole sentences. The i>start
		// loop guard guarantees nextStart > start, so the window progresses.
		nextStart, ov := end, 0
		for i := end - 1; i > start; i-- {
			if ov+toks[i] > overlap {
				break
			}
			ov += toks[i]
			nextStart = i
		}
		start = nextStart
	}
	// §4.3 chunk.count (chunks/doc) + chunk.size_tokens distribution.
	sizes := make([]int, len(chunks))
	for i := range chunks {
		sizes[i] = estimateTokens(chunks[i].Text)
	}
	telemetry.Default().RecordChunking(context.Background(), metricconv.ReferenceKind(doc.SourceKind), len(chunks), sizes)
	return chunks
}

func (c *Chunker) newChunk(doc rag.RawDoc, owner, persona string, ordinal int, text string) rag.Chunk {
	text = strings.TrimSpace(text)
	return rag.Chunk{
		ID:           chunkID(owner, persona, doc.SourceURL, ordinal),
		PersonaOwner: owner,
		PersonaName:  persona,
		SourceURL:    doc.SourceURL,
		SourceKind:   doc.SourceKind,
		Ordinal:      ordinal,
		Text:         text,
		Embedding:    nil, // filled by P2-C embed stage
		Metadata:     chunkMetadata(doc),
	}
}

// chunkMetadata carries forward the doc title + fetchedAt + RawDoc.Meta so each
// stored chunk is self-describing for retrieval/citation (P2-C/§4).
func chunkMetadata(doc rag.RawDoc) map[string]any {
	m := map[string]any{}
	for k, v := range doc.Meta {
		m[k] = v
	}
	if doc.Title != "" {
		m["title"] = doc.Title
	}
	if !doc.FetchedAt.IsZero() {
		m["fetchedAt"] = doc.FetchedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return m
}

// chunkID is the deterministic id: sha256(owner|persona|source_url|ordinal).
func chunkID(owner, persona, sourceURL string, ordinal int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", owner, persona, sourceURL, ordinal)))
	return hex.EncodeToString(h[:])
}

// estimateTokens approximates BPE token count from word count.
func estimateTokens(s string) int {
	words := len(strings.Fields(s))
	if words == 0 {
		return 0
	}
	return int(float64(words)*tokensPerWord + 0.5)
}

// sentenceBoundary matches a sentence terminator (. ! ? plus optional closing
// quotes/brackets) followed by whitespace. Splitting keeps the terminator with
// the preceding sentence.
var sentenceBoundary = regexp.MustCompile(`([.!?]+["')\]]*)\s+`)

// splitSentences splits text into trimmed sentences, respecting paragraph
// breaks (a blank line always ends a sentence). Deterministic.
func splitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []string
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		para = strings.ReplaceAll(para, "\n", " ")
		marked := sentenceBoundary.ReplaceAllString(para, "$1\x00")
		for _, s := range strings.Split(marked, "\x00") {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

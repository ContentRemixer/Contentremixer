// Package review is the Phase-4 scraper-review domain (ISI-7207, plan §3.2):
// the job-triggered reference scraper writes its output here as ScrapedSource
// rows pending human review, and an owner accepts/rescrapes/discards each one.
// Accepted sources flow their chunks into the persona pgvector corpus (ISI-7146).
//
// It is a PUBLIC app package on purpose. Two callers in two modules share it:
//
//   - app/cmd/scrape-job — the Kubernetes Job entrypoint, which runs the
//     ISI-7145 scraper + chunker (both app/internal, reachable only inside the
//     app module) and writes rows here.
//   - booth/internal/scrapejobs — the Booth API's §6.3 gating store, which is a
//     separate module and therefore cannot import app/internal; it reaches the
//     scraper's results only through this public contract.
//
// This package performs NO authorization. The §6.3 predicate is applied by the
// Booth gating store that wraps it (the same split the CRD store uses); the Job
// writes as trusted infrastructure under an owner already authorized at job
// creation.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// DeterministicID is a stable id for a composite key (e.g. job id + resolved
// url), so re-running the same scrape yields the same source row rather than a
// duplicate. It mirrors the rag chunk-id discipline (sha256 of the joined parts).
func DeterministicID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// JobStatus is the lifecycle of a scrape Job (plan §3.2 state machine, job half).
type JobStatus string

const (
	// JobQueued — row inserted, the K8s Job not yet observed running.
	JobQueued JobStatus = "queued"
	// JobRunning — the scrape-job pod has started and claimed the row.
	JobRunning JobStatus = "running"
	// JobSucceeded — the scraper wrote its ScrapedSource rows and exited 0.
	JobSucceeded JobStatus = "succeeded"
	// JobFailed — the scrape failed; Error carries the reason.
	JobFailed JobStatus = "failed"
)

// SourceStatus is the review-queue state of one scraped source (plan §3.2).
type SourceStatus string

const (
	// SourcePending — scraped, awaiting a human accept/rescrape/discard.
	SourcePending SourceStatus = "pending"
	// SourceAccepted — chunks ingested into the persona pgvector corpus.
	SourceAccepted SourceStatus = "accepted"
	// SourceRescraping — the owner asked to re-fetch; a fresh Job is enqueued.
	SourceRescraping SourceStatus = "rescraping"
	// SourceDiscarded — rejected; chunks never reach the corpus.
	SourceDiscarded SourceStatus = "discarded"
)

// ScrapeJob is one enqueued scrape request (plan §3.2: POST /v1/scrape-jobs).
// Owner is the tenancy key the Booth gating store evaluates §6.3 against.
type ScrapeJob struct {
	ID            string    `json:"id"`
	OwnerSubject  string    `json:"owner"`
	ReferenceRepo string    `json:"referenceRepo"`
	PersonaName   string    `json:"personaName"`
	SourceURL     string    `json:"sourceUrl"`
	SourceKind    string    `json:"sourceKind"`
	Status        JobStatus `json:"status"`
	K8sJobName    string    `json:"k8sJobName,omitempty"`
	Error         string    `json:"error,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ScrapedSource is one reviewable source the scraper produced for a job.
type ScrapedSource struct {
	ID            string       `json:"id"`
	JobID         string       `json:"jobId"`
	OwnerSubject  string       `json:"owner"`
	ReferenceRepo string       `json:"referenceRepo"`
	PersonaName   string       `json:"personaName"`
	SourceURL     string       `json:"sourceUrl"`
	SourceKind    string       `json:"sourceKind"`
	Title         string       `json:"title"`
	Status        SourceStatus `json:"status"`
	ChunkCount    int          `json:"chunkCount"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

// ScrapedChunk is one text-only preview chunk of a ScrapedSource. Embeddings are
// NOT stored here — they are computed lazily on accept so the review queue holds
// no vectors and a discarded source costs no embedding calls. ID is the
// deterministic rag chunk id, so accept→corpus upsert is idempotent.
type ScrapedChunk struct {
	ID       string         `json:"id"`
	SourceID string         `json:"sourceId"`
	Ordinal  int            `json:"ordinal"`
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// StyleProfile is the synthesized, DevRel-reviewable writing-style profile for a
// persona (plan §10 decision 1 = synthesize). Style synthesis (D3, ISI-7358)
// produces it from the persona's accepted + embedded exemplars; the owning
// DevRel reviews it, runs a content test against it (D4, ISI-7359), and
// VALIDATES it (D7, ISI-7362).
//
// It lives as a Postgres row — NOT in DevRelPersona CRD status — for the same
// reason the scraped corpus does: it is expensive-to-regenerate synthesized
// content (an LLM call) and large free text, which etcd/status is the wrong
// store for. The CRD carries only the Validated status condition (a cheap
// declarative gate the writer-conditioner reads); THIS row is the content source
// of truth, and the Booth validate action writes both in the same operation.
// One profile per (owner, persona). See docs/design/isi-7356-devrel-model-contract.md.
type StyleProfile struct {
	PersonaName   string `json:"personaName"`
	OwnerSubject  string `json:"owner"`
	ReferenceRepo string `json:"referenceRepo,omitempty"`
	// Traits is the synthesized voice/tone description the DevRel reviews
	// (markdown). It is human-readable on purpose: the onboarding "review your
	// style" surface renders it verbatim.
	Traits string `json:"traits"`
	// Sample is a short generated passage demonstrating the synthesized style,
	// shown alongside Traits so the DevRel judges the profile on real output.
	Sample string `json:"sample"`
	// Validated is the owner's accept decision. It mirrors the DevRelPersona
	// `Validated` status condition; the Booth validate action writes both.
	Validated   bool       `json:"validated"`
	ValidatedAt *time.Time `json:"validatedAt,omitempty"`
	// SourceCount is how many accepted exemplars the synthesis drew on — shown
	// for reviewer confidence and used to detect a profile gone stale after
	// re-ingestion (SourceCount drift vs the persona's accepted sources).
	SourceCount   int       `json:"sourceCount"`
	SynthesizedAt time.Time `json:"synthesizedAt"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// validSourceTransitions is the review state machine: a source only ever leaves
// pending, and each terminal/transient target is reached exactly once.
var validSourceTransitions = map[SourceStatus]map[SourceStatus]bool{
	SourcePending: {
		SourceAccepted:   true,
		SourceRescraping: true,
		SourceDiscarded:  true,
	},
}

// CanTransitionSource reports whether a source may move from→to under the review
// state machine. Only pending sources are actionable; accepted/rescraping/
// discarded are terminal from the API's perspective (a rescrape produces a NEW
// source row via a new job, it does not reopen the old one).
func CanTransitionSource(from, to SourceStatus) bool {
	return validSourceTransitions[from][to]
}

// ErrInvalidTransition is returned when a review action targets a source that is
// not in a state that admits it (e.g. accepting an already-discarded source).
type ErrInvalidTransition struct {
	From, To SourceStatus
}

func (e ErrInvalidTransition) Error() string {
	return fmt.Sprintf("review: invalid source transition %s -> %s", e.From, e.To)
}

// KnownSourceKinds mirrors the scraper's supported reference kinds. The Booth
// API validates job requests against this so a bad kind fails at enqueue time,
// not inside a pod that can only fail the whole Job.
//
// It is string-duplicated from v1alpha1.KnownReferenceKinds on purpose: this
// public package stays free of the k8s API types so the Booth module can import
// it without dragging in controller-runtime. The lock-step is enforced
// mechanically by TestKnownSourceKindsMatchCRD (app/review), not by a comment.
var KnownSourceKinds = []string{
	"youtube",
	"article",
	"github-repo",
	"teleprompter-script",
	"talk-recording",
}

// ValidSourceKind reports whether kind is a scraper-supported reference kind.
func ValidSourceKind(kind string) bool {
	for _, k := range KnownSourceKinds {
		if k == kind {
			return true
		}
	}
	return false
}

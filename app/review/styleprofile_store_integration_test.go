package review

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Style-profile lifecycle against a live Postgres (same discipline as
// store_integration_test.go: runs only when REVIEW_TEST_DSN is set; plain
// tables, no vector extension needed). Covers the D3 storage surface
// (ISI-7358): upsert-on-synthesize, re-synthesis INVALIDATING a prior
// validation, the owner-scoped read, and the accepted-exemplar fetch the
// synthesizer feeds on.

func TestStore_StyleProfileLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	// Seed two sources for (alice, p): one accepted with chunks, one
	// pending with a chunk — only accepted rows may feed synthesis.
	job := ScrapeJob{ID: "job-sp", OwnerSubject: "alice", PersonaName: "p", SourceURL: "https://x/a", SourceKind: "article"}
	if err := st.InsertJob(ctx, job); err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	acc := ScrapedSource{ID: "src-acc", JobID: "job-sp", OwnerSubject: "alice", PersonaName: "p", SourceURL: "https://x/a", SourceKind: "article", Title: "Accepted"}
	pend := ScrapedSource{ID: "src-pend", JobID: "job-sp", OwnerSubject: "alice", PersonaName: "p", SourceURL: "https://x/b", SourceKind: "youtube", Title: "Pending"}
	if err := st.InsertSource(ctx, acc, []ScrapedChunk{{ID: "c-acc-0", SourceID: "src-acc", Ordinal: 0, Text: "voice chunk zero"}}); err != nil {
		t.Fatalf("InsertSource accepted: %v", err)
	}
	if err := st.InsertSource(ctx, pend, []ScrapedChunk{{ID: "c-pend-0", SourceID: "src-pend", Ordinal: 0, Text: "never a voice exemplar"}}); err != nil {
		t.Fatalf("InsertSource pending: %v", err)
	}
	accUpd, err := st.AcceptSource(ctx, "src-acc")
	if err != nil {
		t.Fatalf("AcceptSource: %v", err)
	}
	if accUpd.Status != SourceAccepted {
		t.Fatalf("accepted status = %q", accUpd.Status)
	}

	// Before any synthesis: read is a not-found, exemplar fetch works.
	if _, err := st.GetStyleProfile(ctx, "alice", "p"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetStyleProfile before synthesis: %v; want ErrNotFound", err)
	}
	ex, err := st.ListAcceptedExemplars(ctx, "alice", "p", 10)
	if err != nil {
		t.Fatalf("ListAcceptedExemplars: %v", err)
	}
	if len(ex) != 1 || ex[0].Text != "voice chunk zero" || ex[0].SourceURL != "https://x/a" {
		t.Fatalf("exemplars = %+v; want only the accepted chunk", ex)
	}
	if n, err := st.CountAcceptedSources(ctx, "alice", "p"); err != nil || n != 1 {
		t.Fatalf("CountAcceptedSources = %d, %v; want 1", n, err)
	}
	// Owner scoping: another owner sees nothing.
	if ex, err := st.ListAcceptedExemplars(ctx, "bob", "p", 10); err != nil || len(ex) != 0 {
		t.Fatalf("cross-owner exemplars = %+v, %v; want empty", ex, err)
	}

	// First synthesis stores traits+sample, unvalidated.
	sp := StyleProfile{
		OwnerSubject: "alice", PersonaName: "p", ReferenceRepo: "refs/foo",
		Traits: "**Tone** warm and technical.\n**Rhythm** short sentences.", Sample: "Suppose you are the owner.",
		SourceCount: 1,
	}
	if err := st.UpsertStyleProfile(ctx, sp); err != nil {
		t.Fatalf("UpsertStyleProfile: %v", err)
	}
	got, err := st.GetStyleProfile(ctx, "alice", "p")
	if err != nil {
		t.Fatalf("GetStyleProfile after upsert: %v", err)
	}
	if got.Traits != sp.Traits || got.Sample != sp.Sample || got.SourceCount != 1 || got.ReferenceRepo != "refs/foo" {
		t.Fatalf("stored profile = %+v; want %+v", got, sp)
	}
	if got.Validated || got.ValidatedAt != nil || got.SynthesizedAt.IsZero() {
		t.Fatalf("fresh profile must be unvalidated with synthesized_at set: %+v", got)
	}

	// The validate action (D4's surface, exercised here for the shared row).
	validated, err := st.ValidateStyleProfile(ctx, "alice", "p")
	if err != nil {
		t.Fatalf("ValidateStyleProfile: %v", err)
	}
	if !validated.Validated || validated.ValidatedAt == nil {
		t.Fatalf("validated = %+v", validated)
	}
	first := got.SynthesizedAt

	// Re-synthesis replaces content and RESETS validation (ISI-7356 §4).
	time.Sleep(10 * time.Millisecond) // let now() advance past 'first'
	sp.Traits = "**Tone** revised."
	sp.Sample = "Revised sample."
	if err := st.UpsertStyleProfile(ctx, sp); err != nil {
		t.Fatalf("UpsertStyleProfile re-synthesis: %v", err)
	}
	resynth, err := st.GetStyleProfile(ctx, "alice", "p")
	if err != nil {
		t.Fatalf("GetStyleProfile re-synthesis: %v", err)
	}
	if resynth.Traits != "**Tone** revised." {
		t.Errorf("re-synthesis traits = %q", resynth.Traits)
	}
	if resynth.Validated || resynth.ValidatedAt != nil {
		t.Errorf("re-synthesis must reset validation: %+v", resynth)
	}
	if !resynth.SynthesizedAt.After(first) {
		t.Errorf("synthesized_at did not advance: %v -> %v", first, resynth.SynthesizedAt)
	}

	// Missing owner/persona is rejected, not stored.
	if err := st.UpsertStyleProfile(ctx, StyleProfile{OwnerSubject: "", PersonaName: "p"}); err == nil {
		t.Error("upsert with empty owner must fail")
	}

	// Unknown persona profile is a not-found.
	if _, err := st.ValidateStyleProfile(ctx, "alice", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ValidateStyleProfile unknown persona: %v; want ErrNotFound", err)
	}
}

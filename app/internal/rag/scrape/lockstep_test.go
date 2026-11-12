package scrape

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// TestByKindCoversKnownKinds enforces the dispatch lock-step: every reference
// kind the CRD admits (v1alpha1.KnownReferenceKinds) must have a registered
// scraper, so a kind that passes admission never dead-ends on the misleading
// "unsupported kind" path. New(nil) is the production wiring. D2 replaces the
// placeholder entries with real fetchers; this test keeps the key set honest.
func TestByKindCoversKnownKinds(t *testing.T) {
	s := New(nil)
	got := make([]string, 0, len(s.byKind))
	for k := range s.byKind {
		got = append(got, k)
	}
	want := append([]string(nil), v1alpha1.KnownReferenceKinds...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("byKind keys %v != v1alpha1.KnownReferenceKinds %v", got, want)
	}
}

// TestUnimplementedKindsFailClosed documents the D1→D2 contract boundary: the
// three DevRel-onboarding kinds admit against the CRD but, until D2 wires their
// fetchers, return a clear "not yet implemented" error (not "unsupported kind").
func TestUnimplementedKindsFailClosed(t *testing.T) {
	s := New(nil)
	for _, kind := range []string{KindGitHubRepo, KindTeleprompterScript, KindTalkRecording} {
		_, err := s.Scrape(context.Background(), v1alpha1.PersonaReference{URL: "https://example.com/x", Kind: kind})
		if err == nil {
			t.Fatalf("kind %q: expected a not-implemented error, got nil", kind)
		}
		if !strings.Contains(err.Error(), "not yet implemented") {
			t.Fatalf("kind %q: want 'not yet implemented' error, got %v", kind, err)
		}
	}
}

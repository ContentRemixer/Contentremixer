package scrape

import (
	"fmt"
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

// TestDevRelKindsAreWired asserts the three DevRel-onboarding kinds now dispatch
// to a real fetcher (D2, ISI-7357) rather than the retired "not yet implemented"
// placeholder: each resolves to a non-placeholder scraper type in byKind.
func TestDevRelKindsAreWired(t *testing.T) {
	s := New(nil)
	wantType := map[string]string{
		KindGitHubRepo:         "*scrape.githubRepoScraper",
		KindTeleprompterScript: "*scrape.teleprompterScraper",
		KindTalkRecording:      "*scrape.talkRecordingScraper",
	}
	for kind, want := range wantType {
		ks, ok := s.byKind[kind]
		if !ok {
			t.Fatalf("kind %q not registered in byKind", kind)
		}
		if got := fmt.Sprintf("%T", ks); got != want {
			t.Errorf("kind %q dispatches to %s, want %s", kind, got, want)
		}
	}
}

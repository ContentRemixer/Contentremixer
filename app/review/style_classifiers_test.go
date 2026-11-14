package review

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/style"
)

// These classifiers exist so the Booth route (ISI-7361 D6), which cannot import
// app/internal/pipeline/style, can still map the Synthesize error contract to
// HTTP status codes. They must see through fmt.Errorf wrapping.

func TestIsNoExemplars(t *testing.T) {
	if !IsNoExemplars(style.ErrNoExemplars) {
		t.Fatal("bare ErrNoExemplars not recognized")
	}
	if !IsNoExemplars(fmt.Errorf("synthesize: %w", style.ErrNoExemplars)) {
		t.Fatal("wrapped ErrNoExemplars not recognized")
	}
	if IsNoExemplars(errors.New("other")) {
		t.Fatal("unrelated error misclassified as no-exemplars")
	}
}

func TestSynthesisRateLimited(t *testing.T) {
	wait, ok := SynthesisRateLimited(style.ErrRateLimited{RetryAfter: 90 * time.Second})
	if !ok || wait != 90*time.Second {
		t.Fatalf("rate-limited classifier = (%v,%v), want (90s,true)", wait, ok)
	}
	if _, ok := SynthesisRateLimited(fmt.Errorf("wrap: %w", style.ErrRateLimited{RetryAfter: time.Second})); !ok {
		t.Fatal("wrapped rate-limited not recognized")
	}
	if _, ok := SynthesisRateLimited(errors.New("other")); ok {
		t.Fatal("unrelated error misclassified as rate-limited")
	}
}

func TestIsMalformed(t *testing.T) {
	if !IsMalformed(style.ErrMalformed{Detail: "missing header"}) {
		t.Fatal("bare ErrMalformed not recognized")
	}
	if !IsMalformed(fmt.Errorf("synthesize: %w", style.ErrMalformed{Detail: "x"})) {
		t.Fatal("wrapped ErrMalformed not recognized")
	}
	if IsMalformed(errors.New("other")) {
		t.Fatal("unrelated error misclassified as malformed")
	}
}

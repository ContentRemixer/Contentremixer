package contenttest

import (
	"testing"
	"time"
)

func TestLimiter_CapsPerWindowPerSubject(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	l := NewLimiter(2, time.Minute)
	l.now = func() time.Time { return now }

	if !l.Allow("a") || !l.Allow("a") {
		t.Fatal("first two calls should be allowed")
	}
	if l.Allow("a") {
		t.Fatal("third call within window should be denied")
	}
	// A different subject has its own budget.
	if !l.Allow("b") {
		t.Fatal("other subject should be allowed")
	}
	// After the window slides past, the budget refills.
	now = base.Add(61 * time.Second)
	if !l.Allow("a") {
		t.Fatal("call after window should be allowed again")
	}
}

func TestLimiter_ZeroMaxDisabled(t *testing.T) {
	l := NewLimiter(0, time.Minute)
	for i := 0; i < 100; i++ {
		if !l.Allow("a") {
			t.Fatal("max<=0 should disable the guard")
		}
	}
}

func TestLimiter_NilSafe(t *testing.T) {
	var l *Limiter
	if !l.Allow("a") {
		t.Fatal("nil limiter should allow")
	}
}

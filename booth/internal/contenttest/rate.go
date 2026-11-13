package contenttest

import (
	"sync"
	"time"
)

// Limiter is a per-subject sliding-window rate/cost guard for the content-test
// endpoint (ISI-7359): each content test is an LLM generation, so an unbounded
// endpoint is a cost-amplification surface. It caps each subject to Max calls per
// Window. It is in-memory and per-replica — a deliberately simple guard (the
// endpoint is interactive onboarding traffic, not a hot path); a shared/Redis
// limiter is the upgrade path if Booth ever scales past one replica for this.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time
}

// NewLimiter builds a Limiter of max calls per window. A max <= 0 disables the
// guard (Allow always true) — used when BOOTH_CONTENT_TEST_RATE_PER_MIN is unset.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, hits: map[string][]time.Time{}}
}

// Allow reports whether subject may run another content test now, recording the
// call when it does. It prunes timestamps older than the window first, so the
// map does not grow without bound for an active subject.
func (l *Limiter) Allow(subject string) bool {
	if l == nil || l.max <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)
	kept := l.hits[subject][:0]
	for _, t := range l.hits[subject] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[subject] = kept
		return false
	}
	l.hits[subject] = append(kept, now)
	return true
}

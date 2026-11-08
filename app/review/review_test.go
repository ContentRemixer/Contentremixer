package review

import "testing"

// The review state machine is the one piece of non-trivial logic that isn't a
// straight SQL passthrough, so it gets the runnable check: only pending sources
// are actionable, and every terminal/transient state is a dead end.
func TestCanTransitionSource(t *testing.T) {
	cases := []struct {
		from, to SourceStatus
		want     bool
	}{
		{SourcePending, SourceAccepted, true},
		{SourcePending, SourceRescraping, true},
		{SourcePending, SourceDiscarded, true},
		{SourcePending, SourcePending, false},
		// Terminal/transient states admit nothing.
		{SourceAccepted, SourceDiscarded, false},
		{SourceDiscarded, SourceAccepted, false},
		{SourceRescraping, SourceAccepted, false},
		{SourceAccepted, SourceAccepted, false},
		// Unknown source states fail closed.
		{SourceStatus("bogus"), SourceAccepted, false},
	}
	for _, c := range cases {
		if got := CanTransitionSource(c.from, c.to); got != c.want {
			t.Errorf("CanTransitionSource(%q,%q)=%v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestValidSourceKind(t *testing.T) {
	for _, k := range []string{"youtube", "article"} {
		if !ValidSourceKind(k) {
			t.Errorf("ValidSourceKind(%q)=false, want true", k)
		}
	}
	for _, k := range []string{"", "pdf", "YouTube", "rss"} {
		if ValidSourceKind(k) {
			t.Errorf("ValidSourceKind(%q)=true, want false", k)
		}
	}
}

func TestErrInvalidTransitionMessage(t *testing.T) {
	err := ErrInvalidTransition{From: SourceDiscarded, To: SourceAccepted}
	if got := err.Error(); got == "" {
		t.Fatal("ErrInvalidTransition.Error() is empty")
	}
}

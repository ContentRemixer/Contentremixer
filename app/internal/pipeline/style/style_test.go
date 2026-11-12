package style

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeCompleter records the call and replays a canned completion.
type fakeCompleter struct {
	model  string
	system string
	user   string
	reply  string
	err    error
}

func (f *fakeCompleter) Complete(_ context.Context, model, system, user string) (string, string, error) {
	f.model, f.system, f.user = model, system, user
	if f.err != nil {
		return "", "", f.err
	}
	return f.reply, "stub-model", nil
}

func req(owner, persona string, ex ...Exemplar) Request {
	return Request{OwnerSubject: owner, PersonaName: persona, Exemplars: ex}
}

func TestParseHappyPath(t *testing.T) {
	traits, sample, err := Parse("## Style traits\n- **Tone**: direct.\n\n## Sample paragraph\nObservability is truth.")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !strings.Contains(traits, "Tone") || !strings.Contains(traits, "direct") {
		t.Errorf("traits = %q", traits)
	}
	if sample != "Observability is truth." {
		t.Errorf("sample = %q", sample)
	}
}

func TestParseIgnoresPreambleAndCase(t *testing.T) {
	content := "Sure! Here is the profile.\n\n## style traits\nBold and concise.\n\n## SAMPLE PARAGRAPH\nMetrics first."
	traits, sample, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !strings.Contains(traits, "Bold and concise") {
		t.Errorf("traits = %q", traits)
	}
	if sample != "Metrics first." {
		t.Errorf("sample = %q", sample)
	}
}

func TestParseMalformed(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"missing sample", "## Style traits\nTone: dry."},
		{"missing traits", "## Sample paragraph\nText."},
		{"reversed order", "## Sample paragraph\nFirst.\n\n## Style traits\nSecond."},
		{"empty traits", "## Style traits\n\n## Sample paragraph\nSomething."},
		{"header mid-line only", "note: we saw ## Style traits and ## Sample paragraph once"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse(tc.content)
			var malformed ErrMalformed
			if !errors.As(err, &malformed) {
				t.Fatalf("Parse(%q) err = %v; want ErrMalformed", tc.content, err)
			}
		})
	}
}

func TestPromptContract(t *testing.T) {
	sys := prompt(Request{SelfDescription: "I am a developer advocate", PrimaryContentKind: "video"})
	for _, want := range []string{
		strings.ToLower(traitsHeader),
		strings.ToLower(sampleHeader),
		"tone", "vocabulary tier", "sentence rhythm", "structure",
		"developer advocate",
		"video description",
	} {
		if !strings.Contains(strings.ToLower(sys), want) {
			t.Errorf("prompt missing content %q", want)
		}
	}
}

func TestPromptKindGuidance(t *testing.T) {
	sys := prompt(Request{PrimaryContentKind: "tutorial"})
	if !strings.Contains(sys, "tutorial introduction") {
		t.Errorf("tutorial guidance missing: %q", sys)
	}
	if strings.Contains(sys, "video description") {
		t.Errorf("video guidance leaked into tutorial prompt")
	}
	// Medium-agnostic fallback names no specific medium.
	none := prompt(Request{})
	if !strings.Contains(none, "Pick the medium the excerpts suggest") {
		t.Errorf("medium-agnostic guidance missing: %q", none)
	}
}

func TestSynthesizeEndToEnd(t *testing.T) {
	fc := &fakeCompleter{reply: "## Style traits\n- **Tone**: warm.\n\n## Sample paragraph\nIn this post we dig in."}
	s := New(fc, "writer", Guard{})
	res, err := s.Synthesize(context.Background(), req("own", "pers",
		Exemplar{SourceURL: "u1", SourceKind: "article", Text: "one two"},
		Exemplar{SourceURL: "u2", SourceKind: "youtube", Text: "three"},
	))
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !strings.Contains(res.Traits, "Tone") || res.Sample != "In this post we dig in." {
		t.Errorf("result = %+v", res)
	}
	if fc.model != "writer" {
		t.Errorf("model alias = %q", fc.model)
	}
	for _, want := range []string{"u1", "u2", "one two", "three"} {
		if !strings.Contains(fc.user, want) {
			t.Errorf("user message missing %q", want)
		}
	}
	if !strings.Contains(fc.system, "vocabulary tier") {
		t.Errorf("system prompt missing named dimensions")
	}
}

func TestSynthesizeErrors(t *testing.T) {
	// Uninitialised.
	var nilSynth *Synthesizer
	if _, err := nilSynth.Synthesize(context.Background(), req("o", "p", Exemplar{Text: "x"})); err == nil {
		t.Error("nil synthesizer must fail")
	}
	// Empty owner/persona.
	s := New(&fakeCompleter{}, "", Guard{})
	if _, err := s.Synthesize(context.Background(), req("", "p", Exemplar{Text: "x"})); err == nil {
		t.Error("empty owner must fail")
	}
	// No exemplars: fail closed, no LLM call.
	fc := &fakeCompleter{}
	if _, err := s.Synthesize(context.Background(), req("o", "p")); !errors.Is(err, ErrNoExemplars) {
		t.Fatalf("err = %v; want ErrNoExemplars", err)
	}
	if fc.user != "" {
		t.Errorf("LLM must not be called on empty corpus; user=%q", fc.user)
	}
	// Malformed completion.
	bad := New(&fakeCompleter{reply: "I could not analyze this."}, "", Guard{})
	var malformed ErrMalformed
	if _, err := bad.Synthesize(context.Background(), req("o", "p", Exemplar{Text: "x"})); !errors.As(err, &malformed) {
		t.Fatalf("err = %v (%T); want ErrMalformed", err, err)
	}
	// Gateway failure propagates wrapped.
	boom := errors.New("gateway down")
	fcFail := &fakeCompleter{err: boom}
	fail := New(fcFail, "", Guard{})
	if _, err := fail.Synthesize(context.Background(), req("o", "p", Exemplar{Text: "x"})); !errors.Is(err, boom) {
		t.Fatalf("err = %v; want wrapped gateway error", err)
	}
}

func TestTruncateCaps(t *testing.T) {
	s := New(&fakeCompleter{}, "", Guard{MaxExemplars: 2, MaxChars: 25})
	ex := []Exemplar{
		{SourceURL: "u1", Text: "0123456789"}, // 10 bytes, whole
		{SourceURL: "u2", Text: "abcdefghij"}, // 10 bytes, whole
		{SourceURL: "u3", Text: "klmnopqrst"}, // dropped by MaxExemplars
	}
	capped := s.truncate(ex)
	if len(capped) != 2 {
		t.Fatalf("capped len = %d; want 2", len(capped))
	}
	if capped[0].Text != "0123456789" || capped[1].Text != "abcdefghij" {
		t.Errorf("capped = %+v", capped)
	}

	// Char cap cuts the final overflowing chunk at a rune boundary.
	s2 := New(&fakeCompleter{}, "", Guard{MaxExemplars: 5, MaxChars: 12})
	ex2 := []Exemplar{
		{Text: "hello world!!"}, // 13 bytes → 12 → "hello world!"
		{Text: "never included"},
	}
	got := s2.truncate(ex2)
	if len(got) != 1 || got[0].Text != "hello world!" {
		t.Errorf("rune/byte cut = %+v", got)
	}

	// Multi-byte rune on the boundary is not split.
	s3 := New(&fakeCompleter{}, "", Guard{MaxChars: 5})
	got3 := s3.truncate([]Exemplar{{Text: "héllo"}}) // é is 2 bytes; a 5-byte cut lands inside range but between runes
	if got3[0].Text != "héll" {
		t.Errorf("rune-boundary cut = %q", got3[0].Text)
	}
	if !utf8.ValidString(got3[0].Text) {
		t.Errorf("cut text is not valid UTF-8: %q", got3[0].Text)
	}
}

func TestGuardTooSoon(t *testing.T) {
	now := time.Now()
	g := Guard{MinInterval: time.Hour}
	if _, tooSoon := g.TooSoon(now, time.Time{}); tooSoon {
		t.Error("never synthesized (zero) must not be rate-limited")
	}
	wait, tooSoon := g.TooSoon(now, now.Add(-30*time.Minute))
	if !tooSoon || wait != 30*time.Minute {
		t.Errorf("TooSoon = %v, %v; want true, 30m", tooSoon, wait)
	}
	if _, tooSoon := g.TooSoon(now, now.Add(-time.Hour)); tooSoon {
		t.Error("elapsed interval must not be rate-limited")
	}
	// Zero guard → default interval applies.
	g0 := Guard{}
	if _, tooSoon := g0.TooSoon(now, now.Add(-2*time.Hour)); tooSoon {
		t.Error("2h elapsed with default guard must not be rate-limited")
	}
	if _, tooSoon := g0.TooSoon(now, now.Add(-10*time.Minute)); !tooSoon {
		t.Error("10m elapsed with default guard must be rate-limited")
	}
}

func TestErrRateLimitedMessage(t *testing.T) {
	e := ErrRateLimited{RetryAfter: 90 * time.Second}
	if !strings.Contains(e.Error(), "1m30s") {
		t.Errorf("message = %q", e.Error())
	}
}

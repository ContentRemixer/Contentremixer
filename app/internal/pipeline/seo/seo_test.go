package seo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// fakeCompleter returns a canned completion (or error), recording whether it
// was called and the body it received.
type fakeCompleter struct {
	reply  string
	err    error
	called bool
	gotSys string
	gotMsg string
}

func (f *fakeCompleter) Complete(_ context.Context, _, system, user string) (string, string, error) {
	f.called = true
	f.gotSys = system
	f.gotMsg = user
	if f.err != nil {
		return "", "", f.err
	}
	return f.reply, "served-model", nil
}

func blogMix() pipeline.Mix {
	return pipeline.Mix{
		OutputFormat: "blog",
		Title:        "Shipping OpenTelemetry on Kubernetes",
		Markdown:     "# Shipping OpenTelemetry on Kubernetes\n\nWe wired traces end to end.\n\n## Setup\n\nInstall the collector.\n",
		Meta:         map[string]any{"title": "Shipping OpenTelemetry on Kubernetes"},
	}
}

func TestOptimizeEnrichesMeta(t *testing.T) {
	c := &fakeCompleter{reply: `{
		"description": "Ship OpenTelemetry on Kubernetes with an end-to-end tracing pipeline: install the collector, wire your services, and verify spans land in your backend fast.",
		"canonical_url": "/blog/opentelemetry-on-kubernetes",
		"primary_keyword": "opentelemetry kubernetes",
		"tags": ["observability", "kubernetes", "opentelemetry"],
		"schema": {"@context": "https://schema.org", "@type": "Article", "headline": "Shipping OpenTelemetry on Kubernetes"}
	}`}
	o := New(c, "seo-model", Guard{})

	got, err := o.Optimize(context.Background(), pipeline.RunContext{}, blogMix())
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if !c.called {
		t.Fatal("completer was not called")
	}
	if got.Meta["canonical_url"] != "/blog/opentelemetry-on-kubernetes" {
		t.Errorf("canonical_url = %v", got.Meta["canonical_url"])
	}
	if got.Meta["primary_keyword"] != "opentelemetry kubernetes" {
		t.Errorf("primary_keyword = %v", got.Meta["primary_keyword"])
	}
	desc, _ := got.Meta["description"].(string)
	if n := utf8.RuneCountInString(desc); n < MinDescRunes || n > MaxDescRunes {
		t.Errorf("description is %d runes, want %d-%d: %q", n, MinDescRunes, MaxDescRunes, desc)
	}
	tags, ok := got.Meta["tags"].([]string)
	if !ok || len(tags) != 3 {
		t.Fatalf("tags = %#v", got.Meta["tags"])
	}
	schema, _ := got.Meta["schema"].(string)
	if !strings.Contains(schema, `"@type":"Article"`) {
		t.Errorf("schema not compacted/stored: %q", schema)
	}
	// Schema must be recognizable as JSON-LD (checker.hasJSONLD reads this key).
	if strings.Contains(schema, "\n") {
		t.Errorf("schema should be single-line compact JSON: %q", schema)
	}
}

func TestOptimizeDegradesToUnchanged(t *testing.T) {
	base := blogMix()
	cases := []struct {
		name string
		o    *Optimizer
	}{
		{"nil optimizer", (*Optimizer)(nil)},
		{"nil completer", New(nil, "m", Guard{})},
		{"completer error", New(&fakeCompleter{err: errors.New("boom")}, "m", Guard{})},
		{"unparseable output", New(&fakeCompleter{reply: "sorry, I cannot help with that"}, "m", Guard{})},
		{"empty json", New(&fakeCompleter{reply: "{}"}, "m", Guard{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.o.Optimize(context.Background(), pipeline.RunContext{}, base)
			if err != nil {
				t.Fatalf("Optimize returned error (must degrade, never fail): %v", err)
			}
			// No SEO keys added; the input meta survives verbatim.
			for _, k := range []string{"canonical_url", "primary_keyword", "description"} {
				if _, present := got.Meta[k]; present {
					t.Errorf("%s: meta[%q] should be absent on degrade", tc.name, k)
				}
			}
			if got.Meta["title"] != "Shipping OpenTelemetry on Kubernetes" {
				t.Errorf("%s: input meta not preserved: %#v", tc.name, got.Meta)
			}
		})
	}
}

func TestOptimizeEmptyBodyIsNoOp(t *testing.T) {
	c := &fakeCompleter{reply: `{"description":"x"}`}
	o := New(c, "m", Guard{})
	m := pipeline.Mix{OutputFormat: "blog", Markdown: "   \n\n  "}
	if _, err := o.Optimize(context.Background(), pipeline.RunContext{}, m); err != nil {
		t.Fatal(err)
	}
	if c.called {
		t.Error("completer must not be called for an empty body")
	}
}

func TestParseToleratesFenceAndProse(t *testing.T) {
	reply := "Here is the SEO metadata:\n```json\n{\"primary_keyword\": \"otel\", \"tags\": [\"a\"]}\n```\nHope that helps!"
	o := New(&fakeCompleter{reply: reply}, "m", Guard{})
	got, err := o.Optimize(context.Background(), pipeline.RunContext{}, blogMix())
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta["primary_keyword"] != "otel" {
		t.Errorf("primary_keyword = %v (fence/prose not tolerated)", got.Meta["primary_keyword"])
	}
}

func TestMergeTagsUnionsAndDedupes(t *testing.T) {
	got := mergeTags([]any{"Kubernetes", "observability"}, []string{"observability", "OpenTelemetry", " "})
	want := []string{"Kubernetes", "observability", "OpenTelemetry"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("mergeTags = %v, want %v", got, want)
	}
}

func TestMergeDoesNotClobberExistingTags(t *testing.T) {
	m := blogMix()
	m.Meta["tags"] = []string{"provenance-tag"}
	c := &fakeCompleter{reply: `{"tags": ["seo-tag"]}`}
	got, _ := New(c, "m", Guard{}).Optimize(context.Background(), pipeline.RunContext{}, m)
	tags, _ := got.Meta["tags"].([]string)
	if len(tags) != 2 || tags[0] != "provenance-tag" || tags[1] != "seo-tag" {
		t.Errorf("tags = %v, want [provenance-tag seo-tag]", tags)
	}
}

func TestTrimToRunesMultibyte(t *testing.T) {
	// 170 em-dashes (3 bytes each) — byte length 510, rune length 170.
	s := strings.Repeat("—", 170)
	got := trimToRunes(s, MaxDescRunes)
	if n := utf8.RuneCountInString(got); n > MaxDescRunes {
		t.Errorf("trimmed to %d runes, want <= %d", n, MaxDescRunes)
	}
	if !utf8.ValidString(got) {
		t.Error("trim produced invalid UTF-8")
	}
}

func TestMergeDoesNotMutateInputMeta(t *testing.T) {
	m := blogMix()
	orig := m.Meta
	c := &fakeCompleter{reply: `{"primary_keyword": "otel"}`}
	got, _ := New(c, "m", Guard{}).Optimize(context.Background(), pipeline.RunContext{}, m)
	if _, present := orig["primary_keyword"]; present {
		t.Error("input meta map was mutated; Optimize must copy")
	}
	if got.Meta["primary_keyword"] != "otel" {
		t.Error("enriched copy missing the keyword")
	}
}

func TestSystemPromptMatchesSchemaTypeToFormat(t *testing.T) {
	tut := blogMix()
	tut.OutputFormat = "tutorial"
	c := &fakeCompleter{reply: `{"primary_keyword":"x"}`}
	_, _ = New(c, "m", Guard{}).Optimize(context.Background(), pipeline.RunContext{}, tut)
	if !strings.Contains(c.gotSys, "HowTo") {
		t.Errorf("tutorial format should request a HowTo schema; system prompt:\n%s", c.gotSys)
	}
}

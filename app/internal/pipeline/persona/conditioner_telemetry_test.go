package persona

import (
	"context"
	"errors"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/retrieve"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
)

// Global SDK providers installed once before any test runs; telemetry.Default()
// delegates to them (OTel global-provider wiring), so the conditioner's §4.6/§6.2
// recordings land in these in-memory readers and can be asserted back.
var (
	telReader *sdkmetric.ManualReader
	telSpans  *tracetest.SpanRecorder
)

func TestMain(m *testing.M) {
	telReader = sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(telReader)))
	telSpans = tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(telSpans)))
	os.Exit(m.Run())
}

// conditionOutcomes collects the condition.total series as outcome→persona-set,
// so a test can assert "persona P recorded outcome O" without depending on the
// cumulative counts of other tests sharing the global reader.
func conditionOutcomes(t *testing.T) map[string]map[string]bool {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := telReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]map[string]bool{} // outcome -> set(persona)
	for _, sm := range rm.ScopeMetrics {
		for _, md := range sm.Metrics {
			if md.Name != metricconv.MetricPersonaConditionTotal {
				continue
			}
			for _, dp := range md.Data.(metricdata.Sum[int64]).DataPoints {
				oc, _ := dp.Attributes.Value(attribute.Key(metricconv.AttrConditionOutcome))
				pn, _ := dp.Attributes.Value(attribute.Key(metricconv.AttrPersonaName))
				if out[oc.AsString()] == nil {
					out[oc.AsString()] = map[string]bool{}
				}
				out[oc.AsString()][pn.AsString()] = true
			}
		}
	}
	return out
}

// degradedReasons collects degraded.total as reason→persona-set.
func degradedReasons(t *testing.T) map[string]map[string]bool {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := telReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, md := range sm.Metrics {
			if md.Name != metricconv.MetricPersonaConditionDegradedTotal {
				continue
			}
			for _, dp := range md.Data.(metricdata.Sum[int64]).DataPoints {
				rn, _ := dp.Attributes.Value(attribute.Key(metricconv.AttrConditionDegradedReason))
				pn, _ := dp.Attributes.Value(attribute.Key(metricconv.AttrPersonaName))
				if out[rn.AsString()] == nil {
					out[rn.AsString()] = map[string]bool{}
				}
				out[rn.AsString()][pn.AsString()] = true
			}
		}
	}
	return out
}

// condSpanFor returns the persona.condition span for a given persona name, or nil.
func condSpanFor(persona string) sdktrace.ReadOnlySpan {
	for _, s := range telSpans.Ended() {
		if s.Name() != "persona.condition" {
			continue
		}
		for _, kv := range s.Attributes() {
			if kv.Key == attribute.Key(metricconv.AttrPersonaName) && kv.Value.AsString() == persona {
				return s
			}
		}
	}
	return nil
}

func runWith(persona string, prov *stubProvider, r *retrieve.Retriever) (writer.PersonaOverlay, error) {
	rc := runCtx()
	rc.PersonaName = persona
	rc.Repo = "ContentRemixer/site"
	rc.IssueNumber = 7
	c := &Conditioner{Provider: prov, Retriever: r}
	return c.Condition(context.Background(), rc, sample())
}

// TestConditioningTelemetry_OutcomeLadder drives every branch of §5.4 and asserts
// the §4.6 condition.total outcome + condition.degraded.total reason each emits,
// using a unique persona name per branch to isolate from the shared reader.
func TestConditioningTelemetry_OutcomeLadder(t *testing.T) {
	// full: Ready + ≥1 exemplar.
	runWith("tel-full",
		&stubProvider{profile: PersonaProfile{SelfDescription: "x", Ready: true}},
		retrieve.New(&stubEmbedder{vec: []float32{1}}, &stubStore{ret: []rag.Match{{Chunk: rag.Chunk{Text: "hi"}, Score: 0.9}}}))

	// self_description_only: Ready but no matches (sparse corpus) — NOT a degrade.
	runWith("tel-sparse",
		&stubProvider{profile: PersonaProfile{SelfDescription: "x", Ready: true}},
		retrieve.New(&stubEmbedder{vec: []float32{1}}, &stubStore{ret: nil}))

	// degraded / persona_not_ready.
	runWith("tel-notready",
		&stubProvider{profile: PersonaProfile{SelfDescription: "x", Ready: false}},
		retrieve.New(&stubEmbedder{vec: []float32{1}}, &stubStore{}))

	// degraded / persona_not_ready (provider lookup failure).
	runWith("tel-proverr",
		&stubProvider{err: errors.New("get devrelpersona: boom")},
		retrieve.New(&stubEmbedder{vec: []float32{1}}, &stubStore{}))

	// degraded / embed_failed.
	runWith("tel-embed",
		&stubProvider{profile: PersonaProfile{SelfDescription: "x", Ready: true}},
		retrieve.New(&stubEmbedder{err: errors.New("503")}, &stubStore{}))

	// degraded / store_unavailable.
	runWith("tel-store",
		&stubProvider{profile: PersonaProfile{SelfDescription: "x", Ready: true}},
		retrieve.New(&stubEmbedder{vec: []float32{1}}, &stubStore{err: errors.New("conn refused")}))

	outcomes := conditionOutcomes(t)
	for persona, wantOutcome := range map[string]metricconv.ConditionOutcome{
		"tel-full":     metricconv.ConditionOutcomeFull,
		"tel-sparse":   metricconv.ConditionOutcomeSelfDescriptionOnly,
		"tel-notready": metricconv.ConditionOutcomeDegraded,
		"tel-proverr":  metricconv.ConditionOutcomeDegraded,
		"tel-embed":    metricconv.ConditionOutcomeDegraded,
		"tel-store":    metricconv.ConditionOutcomeDegraded,
	} {
		if !outcomes[string(wantOutcome)][persona] {
			t.Errorf("persona %q: condition.total{outcome=%s} not recorded; got %v", persona, wantOutcome, outcomes)
		}
	}

	// self_description_only must NOT increment degraded.total (§10 budget).
	reasons := degradedReasons(t)
	if reasons[string(metricconv.ConditionDegradedReasonEmptyRetrieval)]["tel-sparse"] {
		t.Error("sparse corpus wrongly recorded condition.degraded.total (must be self_description_only only)")
	}
	for persona, wantReason := range map[string]metricconv.ConditionDegradedReason{
		"tel-notready": metricconv.ConditionDegradedReasonPersonaNotReady,
		"tel-proverr":  metricconv.ConditionDegradedReasonPersonaNotReady,
		"tel-embed":    metricconv.ConditionDegradedReasonEmbedFailed,
		"tel-store":    metricconv.ConditionDegradedReasonStoreUnavailable,
	} {
		if !reasons[string(wantReason)][persona] {
			t.Errorf("persona %q: condition.degraded.total{reason=%s} not recorded; got %v", persona, wantReason, reasons)
		}
	}

	// §6.2 span: a degraded run's persona.condition span carries outcome=degraded,
	// degraded=true, and the §5 WARN event with the run id.
	sp := condSpanFor("tel-store")
	if sp == nil {
		t.Fatal("no persona.condition span for tel-store")
	}
	var degraded bool
	for _, kv := range sp.Attributes() {
		if kv.Key == "contentremixer.condition.degraded" {
			degraded = kv.Value.AsBool()
		}
	}
	if !degraded {
		t.Error("tel-store persona.condition span: degraded attribute not true")
	}
	var evRunID string
	for _, ev := range sp.Events() {
		if ev.Name != "persona.condition.degraded" {
			continue
		}
		for _, a := range ev.Attributes {
			if a.Key == "contentremixer.run.id" {
				evRunID = a.Value.AsString()
			}
		}
	}
	if evRunID != "ContentRemixer/site#7" {
		t.Errorf("degraded event run.id = %q, want ContentRemixer/site#7", evRunID)
	}

	// full run's span is not flagged degraded.
	full := condSpanFor("tel-full")
	if full == nil {
		t.Fatal("no persona.condition span for tel-full")
	}
	for _, kv := range full.Attributes() {
		if kv.Key == "contentremixer.condition.degraded" && kv.Value.AsBool() {
			t.Error("tel-full persona.condition span wrongly flagged degraded")
		}
	}
}

// TestConditioningTelemetry_NoPersonaCounterNoSpan asserts the Phase-1 style-only
// path records condition.total{no_persona} with an empty persona label and opens
// no persona.condition span (nothing was conditioned).
func TestConditioningTelemetry_NoPersonaCounterNoSpan(t *testing.T) {
	rc := runCtx()
	rc.PersonaName = ""
	c := &Conditioner{Provider: &stubProvider{}, Retriever: retrieve.New(&stubEmbedder{}, &stubStore{})}
	if _, err := c.Condition(context.Background(), rc, sample()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !conditionOutcomes(t)[string(metricconv.ConditionOutcomeNoPersona)][""] {
		t.Error("no_persona outcome not recorded with empty persona label")
	}
	if condSpanFor("") != nil {
		t.Error("persona.condition span opened for a persona-less run")
	}
}

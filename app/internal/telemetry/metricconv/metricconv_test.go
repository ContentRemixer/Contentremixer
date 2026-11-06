package metricconv

import "testing"

// knownAttributeKeys is the set of attribute-key constants emitted by codegen.
// It is maintained by hand so that a metric referencing an attribute the registry
// never defines is caught as a test failure (not just a silent string).
var knownAttributeKeys = map[string]bool{
	AttrConditionDegradedReason: true,
	AttrConditionOutcome:        true,
	AttrHttpStatusClass:         true,
	AttrPersonaName:             true,
	AttrPersonaPhase:            true,
	AttrRagEmbedExpectedDim:     true,
	AttrRagRetrieveEmptyReason:  true,
	AttrRagScrapeDropReason:     true,
	AttrRagStoreErrorReason:     true,
	AttrReferenceFailReason:     true,
	AttrReferenceKind:           true,
	AttrReindexTrigger:          true,
	AttrResult:                  true,
	AttrDbOperationName:         true,
	AttrGenAiRequestModel:       true,
}

func TestMetricsNonEmpty(t *testing.T) {
	if len(Metrics) == 0 {
		t.Fatal("Metrics is empty; codegen did not run")
	}
}

func TestLookupRoundTrips(t *testing.T) {
	for _, m := range Metrics {
		got, ok := Lookup(m.Name)
		if !ok {
			t.Errorf("Lookup(%q) returned ok=false", m.Name)
			continue
		}
		if got.Name != m.Name {
			t.Errorf("Lookup(%q).Name = %q", m.Name, got.Name)
		}
	}
	if _, ok := Lookup("contentremixer.persona.does_not_exist"); ok {
		t.Error("Lookup returned ok=true for an unknown metric")
	}
}

func TestNoDuplicateMetricNames(t *testing.T) {
	if len(byName) != len(Metrics) {
		t.Fatalf("duplicate metric names: %d unique vs %d defined", len(byName), len(Metrics))
	}
}

func TestMetricsWellFormed(t *testing.T) {
	validInstrument := map[InstrumentKind]bool{
		InstrumentCounter:       true,
		InstrumentUpDownCounter: true,
		InstrumentGauge:         true,
		InstrumentHistogram:     true,
	}
	for _, m := range Metrics {
		if m.Name == "" {
			t.Error("metric with empty Name")
		}
		if m.Unit == "" {
			t.Errorf("%s: empty Unit", m.Name)
		}
		if !validInstrument[m.Instrument] {
			t.Errorf("%s: invalid instrument %q", m.Name, m.Instrument)
		}
		for _, a := range m.Attributes {
			if !knownAttributeKeys[a] {
				t.Errorf("%s: references undefined attribute key %q", m.Name, a)
			}
		}
	}
}

func TestSchemaURLSet(t *testing.T) {
	if SchemaURL == "" {
		t.Error("SchemaURL is empty")
	}
}

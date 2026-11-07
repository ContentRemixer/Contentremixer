package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
)

// harness builds a Metrics over an in-memory SDK metric reader and span
// recorder so recorded telemetry can be read back and asserted.
type harness struct {
	m    *telemetry.Metrics
	read *sdkmetric.ManualReader
	spr  *tracetest.SpanRecorder
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	spr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spr))
	m, err := telemetry.New(mp, tp)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	return &harness{m: m, read: r, spr: spr}
}

func (h *harness) collect(t *testing.T) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := h.read.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, md := range sm.Metrics {
			out[md.Name] = md
		}
	}
	return out
}

func mustMetric(t *testing.T, all map[string]metricdata.Metrics, name string) metricdata.Metrics {
	t.Helper()
	md, ok := all[name]
	if !ok {
		t.Fatalf("metric %q not recorded", name)
	}
	return md
}

func attrStr(set attribute.Set, key string) string {
	v, ok := set.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return v.String()
}

func TestMetrics_CoversFullContract(t *testing.T) {
	h := newHarness(t)
	m := h.m
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	// §4.1 reconciler
	m.RecordReconcile(ctx, "demo", 150*time.Millisecond, metricconv.ResultSuccess)
	m.SetPhase(ctx, "demo", "Scraping", "Embedding") // Embedding maps to Indexing
	m.ObserveChunkCount(ctx, "demo", 42)
	m.ObserveIndexedReferences(ctx, "demo", 3)
	m.ObserveLastIndexedAge(ctx, "demo", now.Add(-90*time.Second), now)
	m.RecordReferenceFailed(ctx, "demo", metricconv.ReferenceKindYoutube, metricconv.ReferenceFailReasonNoCaptions)
	m.RecordReindex(ctx, "demo", metricconv.ReindexTriggerSpecChange)

	// §4.2 scrape
	m.RecordScrape(ctx, metricconv.ReferenceKindArticle, 2*time.Second, metricconv.ResultSuccess, 4)
	m.RecordScrapeError(ctx, metricconv.ReferenceKindYoutube, metricconv.ReferenceFailReasonNetwork)
	m.RecordScrapeDropped(ctx, metricconv.ReferenceKindArticle, metricconv.ReferenceFailReasonCapExceeded, 7)

	// §4.3 chunk
	m.RecordChunking(ctx, metricconv.ReferenceKindArticle, 3, []int{500, 480, 120})

	// §4.4 embed
	m.RecordEmbedBatch(ctx, 300*time.Millisecond, metricconv.ResultSuccess)
	m.RecordGenAIEmbedding(ctx, "embed", 310*time.Millisecond, "")
	m.RecordEmbedDimensionMismatch(ctx, "embed", 1536)
	m.RecordEmbedGatewayError(ctx, "embed", metricconv.HttpStatusClassRateLimited)

	// §4.5 store + retrieve
	m.RecordStoreOp(ctx, "query", 12*time.Millisecond)
	m.SetStoreUp(ctx, true)
	m.RecordStoreError(ctx, "upsert", telemetry.StoreErrorConn)
	m.RecordRetrieval(ctx, "demo", 5, 0.83)
	m.RecordRetrievalEmpty(ctx, "demo", telemetry.RetrieveEmptyNoRows)

	all := h.collect(t)

	// Every §4.1–4.5 instrument must be present under its registry name.
	for _, name := range []string{
		metricconv.MetricPersonaReconcileDuration, metricconv.MetricPersonaReconcileTotal,
		metricconv.MetricPersonaPhase, metricconv.MetricPersonaChunkCount,
		metricconv.MetricPersonaIndexedReferences, metricconv.MetricPersonaLastIndexedAge,
		metricconv.MetricPersonaReferenceFailedTotal, metricconv.MetricPersonaReindexTotal,
		metricconv.MetricRagScrapeDuration, metricconv.MetricRagScrapeDocs,
		metricconv.MetricRagScrapeErrorsTotal, metricconv.MetricRagScrapeDroppedTotal,
		metricconv.MetricRagChunkCount, metricconv.MetricRagChunkSizeTokens,
		metricconv.MetricRagEmbedBatchDuration, metricconv.MetricRagEmbedDimensionMismatchTotal,
		metricconv.MetricRagEmbedGatewayErrorsTotal, "gen_ai.client.operation.duration",
		"db.client.operation.duration", metricconv.MetricRagStoreUp, metricconv.MetricRagStoreErrorsTotal,
		metricconv.MetricRagRetrieveMatches, metricconv.MetricRagRetrieveTopScore,
		metricconv.MetricRagRetrieveEmptyTotal,
	} {
		mustMetric(t, all, name)
	}

	// reconcile.duration: histogram, unit s, carries result+persona.
	rd := mustMetric(t, all, metricconv.MetricPersonaReconcileDuration)
	if rd.Unit != "s" {
		t.Errorf("reconcile.duration unit = %q, want s", rd.Unit)
	}
	hist, ok := rd.Data.(metricdata.Histogram[float64])
	if !ok || len(hist.DataPoints) != 1 {
		t.Fatalf("reconcile.duration data = %T", rd.Data)
	}
	dp := hist.DataPoints[0]
	if got := attrStr(dp.Attributes, metricconv.AttrResult); got != "success" {
		t.Errorf("reconcile.duration result = %q, want success", got)
	}
	if got := attrStr(dp.Attributes, metricconv.AttrPersonaName); got != "demo" {
		t.Errorf("reconcile.duration persona = %q, want demo", got)
	}
	if dp.Count != 1 || dp.Sum < 0.1 {
		t.Errorf("reconcile.duration count=%d sum=%v", dp.Count, dp.Sum)
	}

	// phase up-down counter: +1 on mapped new phase (Indexing), -1 on old (Scraping).
	phase := mustMetric(t, all, metricconv.MetricPersonaPhase)
	sum, ok := phase.Data.(metricdata.Sum[int64])
	if !ok || sum.IsMonotonic {
		t.Fatalf("phase not a non-monotonic sum: %T", phase.Data)
	}
	byPhase := map[string]int64{}
	for _, p := range sum.DataPoints {
		byPhase[attrStr(p.Attributes, metricconv.AttrPersonaPhase)] = p.Value
	}
	if byPhase[string(metricconv.PersonaPhaseIndexing)] != 1 {
		t.Errorf("phase Indexing = %d, want 1 (Embedding must map to Indexing)", byPhase[string(metricconv.PersonaPhaseIndexing)])
	}
	if byPhase["Scraping"] != -1 {
		t.Errorf("phase Scraping = %d, want -1", byPhase["Scraping"])
	}

	// chunk_count gauge value.
	cc := mustMetric(t, all, metricconv.MetricPersonaChunkCount)
	g, ok := cc.Data.(metricdata.Gauge[int64])
	if !ok || g.DataPoints[0].Value != 42 {
		t.Errorf("chunk_count gauge = %v", cc.Data)
	}

	// last_indexed.age ~ 90s.
	age := mustMetric(t, all, metricconv.MetricPersonaLastIndexedAge)
	ag, ok := age.Data.(metricdata.Gauge[float64])
	if !ok || ag.DataPoints[0].Value != 90 {
		t.Errorf("last_indexed.age = %v, want 90", age.Data)
	}

	// scrape.duration carries result+kind and uses the slow-tail buckets.
	sd := mustMetric(t, all, metricconv.MetricRagScrapeDuration)
	sdh := sd.Data.(metricdata.Histogram[float64])
	if got := attrStr(sdh.DataPoints[0].Attributes, metricconv.AttrReferenceKind); got != "article" {
		t.Errorf("scrape.duration kind = %q", got)
	}
	if b := sdh.DataPoints[0].Bounds; len(b) == 0 || b[0] != 0.1 {
		t.Errorf("scrape.duration bounds = %v, want strategy §4.2 boundaries", b)
	}

	// scrape.dropped counts the dropped docs (7), not 1.
	dropped := mustMetric(t, all, metricconv.MetricRagScrapeDroppedTotal)
	if v := dropped.Data.(metricdata.Sum[int64]).DataPoints[0].Value; v != 7 {
		t.Errorf("scrape.dropped = %d, want 7", v)
	}

	// embed.dimension_mismatch carries model + expected_dim.
	dm := mustMetric(t, all, metricconv.MetricRagEmbedDimensionMismatchTotal)
	dmdp := dm.Data.(metricdata.Sum[int64]).DataPoints[0]
	if got := attrStr(dmdp.Attributes, metricconv.AttrGenAiRequestModel); got != "embed" {
		t.Errorf("dimension_mismatch model = %q", got)
	}
	if v, _ := dmdp.Attributes.Value(attribute.Key(metricconv.AttrRagEmbedExpectedDim)); v.AsInt64() != 1536 {
		t.Errorf("dimension_mismatch expected_dim = %v, want 1536", v.AsInt64())
	}

	// gateway errors status_class = 429.
	ge := mustMetric(t, all, metricconv.MetricRagEmbedGatewayErrorsTotal)
	if got := attrStr(ge.Data.(metricdata.Sum[int64]).DataPoints[0].Attributes, metricconv.AttrHttpStatusClass); got != "429" {
		t.Errorf("gateway error status_class = %q, want 429", got)
	}

	// retrieve.top_score only present because matches>0; carries persona.
	ts := mustMetric(t, all, metricconv.MetricRagRetrieveTopScore)
	tsh := ts.Data.(metricdata.Histogram[float64])
	if tsh.DataPoints[0].Sum != 0.83 {
		t.Errorf("retrieve.top_score sum = %v, want 0.83", tsh.DataPoints[0].Sum)
	}

	// store.up gauge = 1.
	up := mustMetric(t, all, metricconv.MetricRagStoreUp)
	if up.Data.(metricdata.Gauge[int64]).DataPoints[0].Value != 1 {
		t.Errorf("store.up = %v, want 1", up.Data)
	}
}

// TestRetrieveTopScore_SkippedWhenEmpty asserts the top-score histogram is not
// polluted with a 0 when a retrieval returned no matches (§4.5).
func TestRetrieveTopScore_SkippedWhenEmpty(t *testing.T) {
	h := newHarness(t)
	h.m.RecordRetrieval(context.Background(), "demo", 0, 0)
	all := h.collect(t)
	if _, ok := all[metricconv.MetricRagRetrieveTopScore]; ok {
		t.Error("top_score recorded for an empty retrieval; want skipped")
	}
	matches := mustMetric(t, all, metricconv.MetricRagRetrieveMatches)
	if matches.Data.(metricdata.Histogram[int64]).DataPoints[0].Count != 1 {
		t.Error("retrieve.matches should record 0-match retrievals")
	}
}

// TestSpans_IndexTimeTreeAndPII asserts the §6.1 span names, the ERROR-on-error
// semantics, and that spans carry the HASHED owner, never the raw subject (§5).
func TestSpans_IndexTimeTreeAndPII(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	const rawSubject = "oidc|super-secret-subject-123"

	_, span := h.m.StartReconcile(ctx, "demo", rawSubject)
	telemetry.EndSpan(span, nil)

	_, sspan := h.m.StartScrape(ctx, "youtube", "https://youtu.be/abc")
	telemetry.EndSpan(sspan, errors.New("boom"))

	_, cspan := h.m.StartChunk(ctx, "article")
	telemetry.EndSpan(cspan, nil)

	_, espan := h.m.StartEmbed(ctx, "embed", 12)
	telemetry.EndSpan(espan, nil)

	_, uspan := h.m.StartStoreUpsert(ctx, 12)
	telemetry.EndSpan(uspan, nil)

	_, qspan := h.m.StartStoreQuery(ctx)
	telemetry.EndSpan(qspan, nil)

	telemetry.EndSpan(nil, nil) // nil-safe

	ended := h.spr.Ended()
	names := map[string]sdktrace.ReadOnlySpan{}
	for _, s := range ended {
		names[s.Name()] = s
	}
	for _, want := range []string{"DevRelPersona.Reconcile", "rag.scrape", "rag.chunk", "rag.embed", "rag.store.upsert", "rag.store.query"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing span %q", want)
		}
	}
	// scrape span ended with an error => ERROR status.
	if st := names["rag.scrape"].Status(); st.Code != otelcodes.Error {
		t.Errorf("rag.scrape status = %v, want Error", st.Code)
	}
	// reconcile span carries hashed owner, never the raw subject.
	rec := names["DevRelPersona.Reconcile"]
	var ownerHashSeen, rawSeen bool
	for _, kv := range rec.Attributes() {
		val := kv.Value.String()
		if kv.Key == "contentremixer.owner.hash" {
			ownerHashSeen = val == telemetry.OwnerHash(rawSubject)
		}
		if val == rawSubject {
			rawSeen = true
		}
	}
	if !ownerHashSeen {
		t.Error("reconcile span missing hashed owner attribute")
	}
	if rawSeen {
		t.Error("PII LEAK: raw owner subject present on span attributes")
	}
}

// TestConditioning_MetricsSpanAndEvent asserts the §4.6 conditioning metrics,
// the §6.2 persona.condition span + its rag.retrieve.embed child, the outcome/
// degraded annotation, and the §5 persona.condition.degraded WARN event.
func TestConditioning_MetricsSpanAndEvent(t *testing.T) {
	h := newHarness(t)
	m := h.m
	ctx := context.Background()

	// A full run and a degraded (store-unavailable) run.
	m.RecordCondition(ctx, "demo", metricconv.ConditionOutcomeFull, 40*time.Millisecond)
	m.RecordCondition(ctx, "demo", metricconv.ConditionOutcomeDegraded, 5*time.Millisecond)
	m.RecordConditionDegraded(ctx, "demo", metricconv.ConditionDegradedReasonStoreUnavailable)
	// A no_persona (Phase-1 style-only) run — carries an empty persona label.
	m.RecordCondition(ctx, "", metricconv.ConditionOutcomeNoPersona, time.Millisecond)

	all := h.collect(t)

	// condition.total: counter with one series per outcome; carries outcome+persona.
	total := mustMetric(t, all, metricconv.MetricPersonaConditionTotal)
	byOutcome := map[string]int64{}
	for _, dp := range total.Data.(metricdata.Sum[int64]).DataPoints {
		byOutcome[attrStr(dp.Attributes, metricconv.AttrConditionOutcome)] = dp.Value
	}
	for _, want := range []string{"full", "degraded", "no_persona"} {
		if byOutcome[want] != 1 {
			t.Errorf("condition.total{outcome=%s} = %d, want 1", want, byOutcome[want])
		}
	}

	// condition.duration: histogram keyed by outcome, unit s, small buckets.
	dur := mustMetric(t, all, metricconv.MetricPersonaConditionDuration)
	if dur.Unit != "s" {
		t.Errorf("condition.duration unit = %q, want s", dur.Unit)
	}
	dh := dur.Data.(metricdata.Histogram[float64])
	if b := dh.DataPoints[0].Bounds; len(b) == 0 || b[0] != 0.005 {
		t.Errorf("condition.duration bounds = %v, want small §4.6 boundaries", b)
	}

	// degraded.total: carries reason+persona.
	deg := mustMetric(t, all, metricconv.MetricPersonaConditionDegradedTotal)
	ddp := deg.Data.(metricdata.Sum[int64]).DataPoints[0]
	if got := attrStr(ddp.Attributes, metricconv.AttrConditionDegradedReason); got != "store_unavailable" {
		t.Errorf("degraded.total reason = %q, want store_unavailable", got)
	}
	if got := attrStr(ddp.Attributes, metricconv.AttrPersonaName); got != "demo" {
		t.Errorf("degraded.total persona = %q, want demo", got)
	}

	// §6.2 span: persona.condition carries hashed owner (never raw), the
	// annotated outcome+degraded, a nested rag.retrieve.embed child, and the
	// §5 degraded WARN event.
	const rawSubject = "oidc|secret-owner"
	sctx, span := m.StartCondition(ctx, "demo", rawSubject)
	_, child := m.StartRetrieveEmbed(sctx, "embed", 1234)
	telemetry.EndSpan(child, nil)
	telemetry.ConditionDegradedEvent(span, metricconv.ConditionDegradedReasonEmbedFailed, "ContentRemixer/site#42")
	telemetry.AnnotateCondition(span, metricconv.ConditionOutcomeDegraded, true)
	telemetry.EndSpan(span, nil)

	ended := map[string]sdktrace.ReadOnlySpan{}
	var condSpan sdktrace.ReadOnlySpan
	for _, s := range h.spr.Ended() {
		ended[s.Name()] = s
		if s.Name() == "persona.condition" {
			condSpan = s
		}
	}
	if _, ok := ended["rag.retrieve.embed"]; !ok {
		t.Error("missing rag.retrieve.embed child span")
	}
	if condSpan == nil {
		t.Fatal("missing persona.condition span")
	}
	// child nests under persona.condition (same trace, parented by it).
	if ch := ended["rag.retrieve.embed"]; ch.Parent().SpanID() != condSpan.SpanContext().SpanID() {
		t.Error("rag.retrieve.embed is not a child of persona.condition")
	}
	var outcomeSeen, degradedSeen, rawSeen bool
	for _, kv := range condSpan.Attributes() {
		switch kv.Key {
		case attribute.Key(metricconv.AttrConditionOutcome):
			outcomeSeen = kv.Value.AsString() == "degraded"
		case "contentremixer.condition.degraded":
			degradedSeen = kv.Value.AsBool()
		}
		if kv.Value.String() == rawSubject {
			rawSeen = true
		}
	}
	if !outcomeSeen || !degradedSeen {
		t.Errorf("persona.condition missing outcome/degraded annotation (outcome=%v degraded=%v)", outcomeSeen, degradedSeen)
	}
	if rawSeen {
		t.Error("PII LEAK: raw owner subject present on persona.condition span")
	}
	// §5 degraded WARN event with reason + run id.
	var evSeen bool
	for _, ev := range condSpan.Events() {
		if ev.Name == "persona.condition.degraded" {
			evSeen = true
			var reason, runID string
			for _, a := range ev.Attributes {
				switch a.Key {
				case attribute.Key(metricconv.AttrConditionDegradedReason):
					reason = a.Value.AsString()
				case "contentremixer.run.id":
					runID = a.Value.AsString()
				}
			}
			if reason != "embed_failed" || runID != "ContentRemixer/site#42" {
				t.Errorf("degraded event attrs reason=%q run.id=%q", reason, runID)
			}
		}
	}
	if !evSeen {
		t.Error("missing persona.condition.degraded event on span")
	}
}

// TestDefault_NoopIsSafe asserts Default() returns a usable recorder bound to
// the (no-op) global providers and that recording through it never panics —
// the zero-config path every stage uses before an exporter is installed.
func TestDefault_NoopIsSafe(t *testing.T) {
	d := telemetry.Default()
	if d == nil {
		t.Fatal("Default() returned nil")
	}
	ctx := context.Background()
	d.RecordReconcile(ctx, "demo", time.Millisecond, metricconv.ResultSuccess)
	d.SetStoreUp(ctx, false)
	d.RecordCondition(ctx, "demo", metricconv.ConditionOutcomeFull, time.Millisecond)
	d.RecordConditionDegraded(ctx, "demo", metricconv.ConditionDegradedReasonEmptyRetrieval)
	_, span := d.StartReconcile(ctx, "demo", "oidc|x")
	telemetry.EndSpan(span, nil)
	_, cspan := d.StartCondition(ctx, "demo", "oidc|x")
	telemetry.AnnotateCondition(cspan, metricconv.ConditionOutcomeFull, false)
	telemetry.ConditionDegradedEvent(cspan, metricconv.ConditionDegradedReasonEmbedFailed, "#1")
	telemetry.EndSpan(cspan, nil)

	// A nil *Metrics must also be inert, not panic.
	var nilM *telemetry.Metrics
	nilM.RecordReconcile(ctx, "demo", 0, metricconv.ResultError)
	nilM.SetPhase(ctx, "demo", "Pending", "Ready")
	nilM.RecordCondition(ctx, "demo", metricconv.ConditionOutcomeNoPersona, 0)
	nilM.RecordConditionDegraded(ctx, "demo", metricconv.ConditionDegradedReasonStoreUnavailable)
	if tr := nilM.Tracer(); tr == nil {
		t.Error("nil Metrics.Tracer() returned nil")
	}
}

// TestNilMetrics_AllInert asserts every recording/observe/start method is a
// safe no-op on a nil *Metrics — the defensive contract that lets stages record
// unconditionally without a nil check of their own.
func TestNilMetrics_AllInert(t *testing.T) {
	var m *telemetry.Metrics
	ctx := context.Background()
	now := time.Now()

	m.RecordReconcile(ctx, "p", time.Second, metricconv.ResultSuccess)
	m.SetPhase(ctx, "p", "Pending", "Scraping")
	m.ObserveChunkCount(ctx, "p", 1)
	m.ObserveIndexedReferences(ctx, "p", 1)
	m.ObserveLastIndexedAge(ctx, "p", now.Add(-time.Minute), now)
	m.RecordReferenceFailed(ctx, "p", metricconv.ReferenceKindArticle, metricconv.ReferenceFailReasonParse)
	m.RecordReindex(ctx, "p", metricconv.ReindexTriggerPeriodicResync)
	m.RecordScrape(ctx, metricconv.ReferenceKindArticle, time.Second, metricconv.ResultSuccess, 1)
	m.RecordScrapeDropped(ctx, metricconv.ReferenceKindArticle, metricconv.ReferenceFailReasonCapExceeded, 1)
	m.RecordScrapeError(ctx, metricconv.ReferenceKindArticle, metricconv.ReferenceFailReasonNetwork)
	m.RecordChunking(ctx, metricconv.ReferenceKindArticle, 1, []int{10})
	m.RecordEmbedBatch(ctx, time.Second, metricconv.ResultSuccess)
	m.RecordGenAIEmbedding(ctx, "e", time.Second, "")
	m.RecordEmbedDimensionMismatch(ctx, "e", 1536)
	m.RecordEmbedGatewayError(ctx, "e", metricconv.HttpStatusClassServerError)
	m.RecordStoreOp(ctx, "query", time.Second)
	m.SetStoreUp(ctx, true)
	m.RecordStoreError(ctx, "query", telemetry.StoreErrorOther)
	m.RecordRetrieval(ctx, "p", 1, 0.5)
	m.RecordRetrievalEmpty(ctx, "p", telemetry.RetrieveEmptyBelowMinSco)
	m.RecordCondition(ctx, "p", metricconv.ConditionOutcomeDegraded, time.Second)
	m.RecordConditionDegraded(ctx, "p", metricconv.ConditionDegradedReasonPersonaNotReady)

	// Start* on nil must still return a usable (no-op) span.
	_, s1 := m.StartReconcile(ctx, "p", "sub")
	_, s2 := m.StartScrape(ctx, "article", "u")
	_, s3 := m.StartChunk(ctx, "article")
	_, s4 := m.StartEmbed(ctx, "e", 1)
	_, s5 := m.StartStoreUpsert(ctx, 1)
	_, s6 := m.StartStoreQuery(ctx)
	_, s7 := m.StartCondition(ctx, "p", "sub")
	_, s8 := m.StartRetrieveEmbed(ctx, "e", 10)
	telemetry.AnnotateCondition(s7, metricconv.ConditionOutcomeDegraded, true)
	telemetry.ConditionDegradedEvent(s7, metricconv.ConditionDegradedReasonEmbedFailed, "#1")
	telemetry.EndSpan(s1, nil)
	telemetry.EndSpan(s2, nil)
	telemetry.EndSpan(s3, nil)
	telemetry.EndSpan(s4, nil)
	telemetry.EndSpan(s5, nil)
	telemetry.EndSpan(s6, nil)
	telemetry.EndSpan(s7, nil)
	telemetry.EndSpan(s8, nil)

	// nil-span annotate/event helpers must be inert too.
	telemetry.AnnotateCondition(nil, metricconv.ConditionOutcomeFull, false)
	telemetry.ConditionDegradedEvent(nil, metricconv.ConditionDegradedReasonEmptyRetrieval, "#0")
}

func TestOwnerHash(t *testing.T) {
	if got := telemetry.OwnerHash(""); got != "" {
		t.Errorf("OwnerHash(\"\") = %q, want empty", got)
	}
	h := telemetry.OwnerHash("oidc|abc")
	if len(h) != 12 {
		t.Errorf("OwnerHash len = %d, want 12", len(h))
	}
	if telemetry.OwnerHash("oidc|abc") != h {
		t.Error("OwnerHash not deterministic")
	}
	if telemetry.OwnerHash("oidc|xyz") == h {
		t.Error("OwnerHash collided on distinct subjects")
	}
}

func TestMapPhase(t *testing.T) {
	cases := map[string]string{
		"Embedding": string(metricconv.PersonaPhaseIndexing),
		"Error":     string(metricconv.PersonaPhaseDegraded),
		"Pending":   "Pending",
		"Scraping":  "Scraping",
		"Ready":     "Ready",
		"Idle":      "Idle",
	}
	for in, want := range cases {
		if got := telemetry.MapPhase(in); got != want {
			t.Errorf("MapPhase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyHTTPStatus(t *testing.T) {
	cases := []struct {
		code int
		want metricconv.HttpStatusClass
		ok   bool
	}{
		{429, metricconv.HttpStatusClassRateLimited, true},
		{503, metricconv.HttpStatusClassServerError, true},
		{404, metricconv.HttpStatusClassClientError, true},
		{200, "", false},
	}
	for _, c := range cases {
		got, ok := telemetry.ClassifyHTTPStatus(c.code)
		if ok != c.ok || got != c.want {
			t.Errorf("ClassifyHTTPStatus(%d) = (%q,%v), want (%q,%v)", c.code, got, ok, c.want, c.ok)
		}
	}
}

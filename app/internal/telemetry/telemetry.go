// Package telemetry is the Phase-2 persona/RAG instrumentation layer (O11y-2,
// ISI-7159). It turns the dependency-free semantic-convention stubs in
// ./metricconv (O11y-1, ISI-7158) into live OpenTelemetry instruments and the
// typed recording + span helpers the DevRelPersona reconciler and the
// internal/rag stages call. It implements the §4.1–4.5 metrics, §6.1 index-time
// spans and the §5 PII helper from the Phase-2 observability strategy
// (docs/observability/phase2-personas-rag-observability.md).
//
// It depends ONLY on the OpenTelemetry API (never the SDK): instruments are
// created against whatever MeterProvider/TracerProvider is installed, and the
// default global providers are no-ops until the operator wires an OTLP exporter
// (O11y-4 / deploy). Callers therefore need no configuration — recording is a
// cheap no-op out of the box and becomes live telemetry once a provider is set,
// the standard Go library-instrumentation pattern.
//
// PII policy (§5.3) is enforced here by construction: owner subjects are only
// ever exposed through OwnerHash, chunk text / embedding vectors / query
// literals are never accepted by any recording method, and unbounded values
// (source URLs, chunk IDs) are span attributes only, never metric labels (§3).
package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	mnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tnoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
)

// scopeName is the OTel instrumentation scope for every persona/RAG instrument
// and span. It is the import path of this package, per OTel convention.
const scopeName = "github.com/ContentRemixer/Contentremixer/app/internal/telemetry"

// Standard-semantic-convention keys the strategy reuses (§2) that are not part
// of the custom contentremixer.* registry in metricconv.
const (
	attrGenAiOperationName = "gen_ai.operation.name"
	attrErrorType          = "error.type"
	attrDbSystemName       = "db.system.name"

	genAiOperationEmbeddings = "embeddings"
	dbSystemPostgreSQL       = "postgresql"
)

// Explicit histogram bucket boundaries, tuned per signal (§4.2/§4.4/§4.5).
var (
	bucketsScrapeDurationS = []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60}
	bucketsEmbedDurationS  = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10}
	bucketsDBDurationS     = []float64{0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}
	bucketsTopScore        = []float64{0, 0.3, 0.5, 0.6, 0.7, 0.8, 0.85, 0.9, 0.95, 1.0}
	// Not pinned by the strategy; sensible defaults for the remaining signals.
	bucketsReconcileDurationS = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	bucketsDocsPerRef         = []float64{1, 2, 5, 10, 20, 50}
	bucketsChunksPerDoc       = []float64{1, 2, 5, 10, 20, 50, 100}
	bucketsChunkSizeTokens    = []float64{64, 128, 256, 384, 512, 640, 768, 1024}
	bucketsRetrieveMatches    = []float64{0, 1, 2, 3, 5, 8, 13, 21}
	// Conditioning overhead on the writer path must stay small — RAG is
	// additive, never a gate (§4.6/§5). Fine low-end buckets surface any
	// regression into the content pipeline's critical path.
	bucketsConditionDurationS = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2}
)

// Reasons the strategy enumerates (§4.5) that metricconv carries as free
// attribute keys without a generated enum type.
type (
	// StoreErrorReason classifies a pgvector store failure (§4.5 store.errors).
	StoreErrorReason string
	// RetrieveEmptyReason classifies why a retrieval returned no usable match
	// (§4.5 retrieve.empty).
	RetrieveEmptyReason string
)

const (
	StoreErrorConn       StoreErrorReason = "conn"       // connection/transport failure reaching Postgres.
	StoreErrorTimeout    StoreErrorReason = "timeout"    // context deadline / statement timeout.
	StoreErrorConstraint StoreErrorReason = "constraint" // constraint / data violation.
	StoreErrorOther      StoreErrorReason = "other"      // any other store error.

	RetrieveEmptyNoRows      RetrieveEmptyReason = "no_rows"        // the owner-scoped query matched no chunks.
	RetrieveEmptyBelowMinSco RetrieveEmptyReason = "below_minscore" // matches existed but all fell below MinScore.
)

// Metrics holds every Phase-2 persona/RAG instrument (§4.1–4.5) plus the tracer
// used for the §6.1 index-time span tree. Build one with New (typically via
// Default) and share it; it is safe for concurrent use.
type Metrics struct {
	tracer trace.Tracer

	// §4.1 reconciler
	reconcileDuration metric.Float64Histogram
	reconcileTotal    metric.Int64Counter
	phase             metric.Int64UpDownCounter
	indexedReferences metric.Int64Gauge
	chunkCount        metric.Int64Gauge
	referenceFailed   metric.Int64Counter
	reindexTotal      metric.Int64Counter
	lastIndexedAge    metric.Float64Gauge

	// §4.2 scrape
	scrapeDuration metric.Float64Histogram
	scrapeDocs     metric.Int64Histogram
	scrapeDropped  metric.Int64Counter
	scrapeErrors   metric.Int64Counter

	// §4.3 chunk
	chunkCountPerDoc metric.Int64Histogram
	chunkSizeTokens  metric.Int64Histogram

	// §4.4 embed + LiteLLM
	embedBatchDuration metric.Float64Histogram
	embedDimMismatch   metric.Int64Counter
	embedGatewayErrors metric.Int64Counter
	genaiOpDuration    metric.Float64Histogram

	// §4.5 store + retrieve
	dbOpDuration     metric.Float64Histogram
	storeUp          metric.Int64Gauge
	storeErrors      metric.Int64Counter
	retrieveMatches  metric.Int64Histogram
	retrieveTopScore metric.Float64Histogram
	retrieveEmpty    metric.Int64Counter

	// §4.6 writer conditioning (P2-D / O11y-3)
	conditionTotal    metric.Int64Counter
	conditionDuration metric.Float64Histogram
	conditionDegraded metric.Int64Counter
}

// New constructs the full instrument set against mp and a tracer from tp. Nil
// providers fall back to the installed globals (otel.GetMeterProvider /
// GetTracerProvider), which default to no-ops. The returned *Metrics always has
// usable (never nil) instruments even if some failed to register; the joined
// error is advisory and can be ignored by callers that want best-effort
// telemetry.
func New(mp metric.MeterProvider, tp trace.TracerProvider) (*Metrics, error) {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	meter := mp.Meter(scopeName,
		metric.WithInstrumentationVersion("0.1.0"),
		metric.WithSchemaURL(metricconv.SchemaURL),
	)
	m := &Metrics{tracer: tp.Tracer(scopeName, trace.WithSchemaURL(metricconv.SchemaURL))}

	var errs []error
	hist := func(name, unit, desc string, bounds []float64) metric.Float64Histogram {
		h, err := meter.Float64Histogram(name,
			metric.WithUnit(unit), metric.WithDescription(desc),
			metric.WithExplicitBucketBoundaries(bounds...))
		errs = append(errs, err)
		return h
	}
	ihist := func(name, unit, desc string, bounds []float64) metric.Int64Histogram {
		h, err := meter.Int64Histogram(name,
			metric.WithUnit(unit), metric.WithDescription(desc),
			metric.WithExplicitBucketBoundaries(bounds...))
		errs = append(errs, err)
		return h
	}
	cnt := func(name, unit, desc string) metric.Int64Counter {
		c, err := meter.Int64Counter(name, metric.WithUnit(unit), metric.WithDescription(desc))
		errs = append(errs, err)
		return c
	}
	igauge := func(name, unit, desc string) metric.Int64Gauge {
		g, err := meter.Int64Gauge(name, metric.WithUnit(unit), metric.WithDescription(desc))
		errs = append(errs, err)
		return g
	}
	fgauge := func(name, unit, desc string) metric.Float64Gauge {
		g, err := meter.Float64Gauge(name, metric.WithUnit(unit), metric.WithDescription(desc))
		errs = append(errs, err)
		return g
	}
	udc := func(name, unit, desc string) metric.Int64UpDownCounter {
		c, err := meter.Int64UpDownCounter(name, metric.WithUnit(unit), metric.WithDescription(desc))
		errs = append(errs, err)
		return c
	}
	def := func(name string) string {
		if d, ok := metricconv.Lookup(name); ok {
			return d.Brief
		}
		return ""
	}

	// §4.1 reconciler
	m.reconcileDuration = hist(metricconv.MetricPersonaReconcileDuration, "s", def(metricconv.MetricPersonaReconcileDuration), bucketsReconcileDurationS)
	m.reconcileTotal = cnt(metricconv.MetricPersonaReconcileTotal, "1", def(metricconv.MetricPersonaReconcileTotal))
	m.phase = udc(metricconv.MetricPersonaPhase, "1", def(metricconv.MetricPersonaPhase))
	m.indexedReferences = igauge(metricconv.MetricPersonaIndexedReferences, "1", def(metricconv.MetricPersonaIndexedReferences))
	m.chunkCount = igauge(metricconv.MetricPersonaChunkCount, "1", def(metricconv.MetricPersonaChunkCount))
	m.referenceFailed = cnt(metricconv.MetricPersonaReferenceFailedTotal, "1", def(metricconv.MetricPersonaReferenceFailedTotal))
	m.reindexTotal = cnt(metricconv.MetricPersonaReindexTotal, "1", def(metricconv.MetricPersonaReindexTotal))
	m.lastIndexedAge = fgauge(metricconv.MetricPersonaLastIndexedAge, "s", def(metricconv.MetricPersonaLastIndexedAge))

	// §4.2 scrape
	m.scrapeDuration = hist(metricconv.MetricRagScrapeDuration, "s", def(metricconv.MetricRagScrapeDuration), bucketsScrapeDurationS)
	m.scrapeDocs = ihist(metricconv.MetricRagScrapeDocs, "1", def(metricconv.MetricRagScrapeDocs), bucketsDocsPerRef)
	m.scrapeDropped = cnt(metricconv.MetricRagScrapeDroppedTotal, "1", def(metricconv.MetricRagScrapeDroppedTotal))
	m.scrapeErrors = cnt(metricconv.MetricRagScrapeErrorsTotal, "1", def(metricconv.MetricRagScrapeErrorsTotal))

	// §4.3 chunk
	m.chunkCountPerDoc = ihist(metricconv.MetricRagChunkCount, "1", def(metricconv.MetricRagChunkCount), bucketsChunksPerDoc)
	m.chunkSizeTokens = ihist(metricconv.MetricRagChunkSizeTokens, "1", def(metricconv.MetricRagChunkSizeTokens), bucketsChunkSizeTokens)

	// §4.4 embed + LiteLLM
	m.embedBatchDuration = hist(metricconv.MetricRagEmbedBatchDuration, "s", def(metricconv.MetricRagEmbedBatchDuration), bucketsEmbedDurationS)
	m.embedDimMismatch = cnt(metricconv.MetricRagEmbedDimensionMismatchTotal, "1", def(metricconv.MetricRagEmbedDimensionMismatchTotal))
	m.embedGatewayErrors = cnt(metricconv.MetricRagEmbedGatewayErrorsTotal, "1", def(metricconv.MetricRagEmbedGatewayErrorsTotal))
	m.genaiOpDuration = hist("gen_ai.client.operation.duration", "s", "Duration of a GenAI embedding operation through the LiteLLM gateway.", bucketsEmbedDurationS)

	// §4.5 store + retrieve
	m.dbOpDuration = hist("db.client.operation.duration", "s", "Duration of a pgvector store operation.", bucketsDBDurationS)
	m.storeUp = igauge(metricconv.MetricRagStoreUp, "1", def(metricconv.MetricRagStoreUp))
	m.storeErrors = cnt(metricconv.MetricRagStoreErrorsTotal, "1", def(metricconv.MetricRagStoreErrorsTotal))
	m.retrieveMatches = ihist(metricconv.MetricRagRetrieveMatches, "1", def(metricconv.MetricRagRetrieveMatches), bucketsRetrieveMatches)
	m.retrieveTopScore = hist(metricconv.MetricRagRetrieveTopScore, "1", def(metricconv.MetricRagRetrieveTopScore), bucketsTopScore)
	m.retrieveEmpty = cnt(metricconv.MetricRagRetrieveEmptyTotal, "1", def(metricconv.MetricRagRetrieveEmptyTotal))

	// §4.6 writer conditioning
	m.conditionTotal = cnt(metricconv.MetricPersonaConditionTotal, "1", def(metricconv.MetricPersonaConditionTotal))
	m.conditionDuration = hist(metricconv.MetricPersonaConditionDuration, "s", def(metricconv.MetricPersonaConditionDuration), bucketsConditionDurationS)
	m.conditionDegraded = cnt(metricconv.MetricPersonaConditionDegradedTotal, "1", def(metricconv.MetricPersonaConditionDegradedTotal))

	return m, errors.Join(errs...)
}

// defaultMetrics memoises a *Metrics built against the installed global
// providers. The OTel global providers delegate, so instruments created here
// before SetMeterProvider/SetTracerProvider are wired up when a real provider
// is later installed — one build is correct for the process lifetime.
var defaultMetrics = sync.OnceValue(func() *Metrics {
	m, _ := New(otel.GetMeterProvider(), otel.GetTracerProvider())
	if m == nil { // defensive; New never returns nil, but never record on nil
		m, _ = New(mnoop.NewMeterProvider(), tnoop.NewTracerProvider())
	}
	return m
})

// Default returns the process-wide *Metrics bound to the global OTel providers.
// It is what the reconciler and RAG stages record through; a no-op until the
// operator installs an exporter.
func Default() *Metrics { return defaultMetrics() }

// OwnerHash is the §5.1/§3-2 PII guard: the sha256 (first 12 hex) of an OIDC
// owner subject. Use it anywhere an owner would otherwise be user-visible in a
// log body or shared dashboard. An empty subject hashes to "" so "no owner" is
// not conflated with a real pseudonym.
func OwnerHash(subject string) string {
	if subject == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])[:12]
}

// ----- §4.1 reconciler recording -------------------------------------------

func personaAttr(persona string) attribute.KeyValue {
	return attribute.String(metricconv.AttrPersonaName, persona)
}

// RecordReconcile records the duration and outcome of one DevRelPersona
// reconcile (reconcile.duration + reconcile.total).
func (m *Metrics) RecordReconcile(ctx context.Context, persona string, d time.Duration, result metricconv.Result) {
	if m == nil {
		return
	}
	set := metric.WithAttributes(personaAttr(persona), attribute.String(metricconv.AttrResult, string(result)))
	m.reconcileDuration.Record(ctx, d.Seconds(), set)
	m.reconcileTotal.Add(ctx, 1, set)
}

// SetPhase moves one persona's phase-occupancy series: it decrements the old
// phase and increments the new one so each contentremixer.persona.phase{phase}
// series carries the live count of personas in that phase (§4.1). Call it only
// on an actual transition (old != new); passing an empty old skips the
// decrement (first observation of the persona).
func (m *Metrics) SetPhase(ctx context.Context, persona, oldPhase, newPhase string) {
	if m == nil || oldPhase == newPhase {
		return
	}
	if oldPhase != "" {
		m.phase.Add(ctx, -1, metric.WithAttributes(personaAttr(persona), attribute.String(metricconv.AttrPersonaPhase, MapPhase(oldPhase))))
	}
	if newPhase != "" {
		m.phase.Add(ctx, 1, metric.WithAttributes(personaAttr(persona), attribute.String(metricconv.AttrPersonaPhase, MapPhase(newPhase))))
	}
}

// ObserveChunkCount publishes the per-persona stored-chunk gauge (§4.1
// chunk_count) from status.
func (m *Metrics) ObserveChunkCount(ctx context.Context, persona string, chunkCount int) {
	if m == nil {
		return
	}
	m.chunkCount.Record(ctx, int64(chunkCount), metric.WithAttributes(personaAttr(persona)))
}

// ObserveIndexedReferences publishes the per-persona indexed-reference gauge
// (§4.1 indexed_references) from status. The P2-A reconciler does not call this
// — the CRD status (api/v1alpha1) carries ExemplarCount but no
// indexedReferences field yet (see the O11y-2 drift note); it is here for the
// P2-C / orchestrator path that owns that count.
func (m *Metrics) ObserveIndexedReferences(ctx context.Context, persona string, indexedReferences int) {
	if m == nil {
		return
	}
	m.indexedReferences.Record(ctx, int64(indexedReferences), metric.WithAttributes(personaAttr(persona)))
}

// ObserveLastIndexedAge publishes the staleness gauge (§4.1 last_indexed.age):
// now − the last successful index time. A zero lastIndexed is skipped (never
// indexed), and a future timestamp clamps to 0.
func (m *Metrics) ObserveLastIndexedAge(ctx context.Context, persona string, lastIndexed, now time.Time) {
	if m == nil || lastIndexed.IsZero() {
		return
	}
	age := now.Sub(lastIndexed).Seconds()
	if age < 0 {
		age = 0
	}
	m.lastIndexedAge.Record(ctx, age, metric.WithAttributes(personaAttr(persona)))
}

// RecordReferenceFailed records a per-reference indexing failure (§4.1
// reference.failed) — the metric behind the persona.reference.failed log. The
// source URL is an exemplar/log field, never a label (§3-3).
func (m *Metrics) RecordReferenceFailed(ctx context.Context, persona string, kind metricconv.ReferenceKind, reason metricconv.ReferenceFailReason) {
	if m == nil {
		return
	}
	m.referenceFailed.Add(ctx, 1, metric.WithAttributes(
		personaAttr(persona),
		attribute.String(metricconv.AttrReferenceKind, string(kind)),
		attribute.String(metricconv.AttrReferenceFailReason, string(reason)),
	))
}

// RecordReindex records a persona re-index run by trigger (§4.1 reindex.total).
func (m *Metrics) RecordReindex(ctx context.Context, persona string, trigger metricconv.ReindexTrigger) {
	if m == nil {
		return
	}
	m.reindexTotal.Add(ctx, 1, metric.WithAttributes(
		personaAttr(persona),
		attribute.String(metricconv.AttrReindexTrigger, string(trigger)),
	))
}

// ----- §4.2 scrape recording ------------------------------------------------

// RecordScrape records one reference scrape: its duration and outcome
// (scrape.duration) plus, on success, the fan-out doc count (scrape.docs).
func (m *Metrics) RecordScrape(ctx context.Context, kind metricconv.ReferenceKind, d time.Duration, result metricconv.Result, docs int) {
	if m == nil {
		return
	}
	kindAttr := attribute.String(metricconv.AttrReferenceKind, string(kind))
	m.scrapeDuration.Record(ctx, d.Seconds(), metric.WithAttributes(kindAttr, attribute.String(metricconv.AttrResult, string(result))))
	if result == metricconv.ResultSuccess {
		m.scrapeDocs.Record(ctx, int64(docs), metric.WithAttributes(kindAttr))
	}
}

// RecordScrapeDropped records docs dropped before indexing to honour the
// per-reference fan-out cap — no silent truncation (§4.2 scrape.dropped / §3.1).
func (m *Metrics) RecordScrapeDropped(ctx context.Context, kind metricconv.ReferenceKind, reason metricconv.ReferenceFailReason, dropped int) {
	if m == nil || dropped <= 0 {
		return
	}
	m.scrapeDropped.Add(ctx, int64(dropped), metric.WithAttributes(
		attribute.String(metricconv.AttrReferenceKind, string(kind)),
		attribute.String(metricconv.AttrRagScrapeDropReason, string(reason)),
	))
}

// RecordScrapeError records a scrape failure by kind and reason (§4.2
// scrape.errors) — the stage-local signal that feeds reference.failed.
func (m *Metrics) RecordScrapeError(ctx context.Context, kind metricconv.ReferenceKind, reason metricconv.ReferenceFailReason) {
	if m == nil {
		return
	}
	m.scrapeErrors.Add(ctx, 1, metric.WithAttributes(
		attribute.String(metricconv.AttrReferenceKind, string(kind)),
		attribute.String(metricconv.AttrRagScrapeDropReason, string(reason)),
	))
}

// ----- §4.3 chunk recording -------------------------------------------------

// RecordChunking records the chunker output for one RawDoc (§4.3): the chunk
// count for the doc and each chunk's size in estimated tokens.
func (m *Metrics) RecordChunking(ctx context.Context, kind metricconv.ReferenceKind, chunkCount int, sizesTokens []int) {
	if m == nil {
		return
	}
	m.chunkCountPerDoc.Record(ctx, int64(chunkCount), metric.WithAttributes(attribute.String(metricconv.AttrReferenceKind, string(kind))))
	for _, n := range sizesTokens {
		m.chunkSizeTokens.Record(ctx, int64(n))
	}
}

// ----- §4.4 embed recording -------------------------------------------------

// RecordEmbedBatch records one embedding batch's duration and outcome under the
// pipeline-level custom histogram (§4.4 embed.batch.duration). The embed Stage
// owns this; the GenAI semconv duration is recorded separately at the gateway
// layer by RecordGenAIEmbedding.
func (m *Metrics) RecordEmbedBatch(ctx context.Context, d time.Duration, result metricconv.Result) {
	if m == nil {
		return
	}
	m.embedBatchDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(metricconv.AttrResult, string(result))))
}

// RecordGenAIEmbedding records one GenAI embeddings call under the OTel GenAI
// semconv duration histogram (§2/§4.4 gen_ai.client.operation.duration). errType
// is "" on success, else the GenAI error.type (a status class or "timeout").
// Recorded at the LiteLLM gateway layer, which knows the model alias and status.
func (m *Metrics) RecordGenAIEmbedding(ctx context.Context, model string, d time.Duration, errType string) {
	if m == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(attrGenAiOperationName, genAiOperationEmbeddings),
		attribute.String(metricconv.AttrGenAiRequestModel, model),
	}
	if errType != "" {
		attrs = append(attrs, attribute.String(attrErrorType, errType))
	}
	m.genaiOpDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attrs...))
}

// ClassifyHTTPStatus maps an HTTP status code to the bounded §4.4 status-class
// enum for the embed.gateway.errors label (4xx excluding 429, 429, 5xx).
func ClassifyHTTPStatus(code int) (metricconv.HttpStatusClass, bool) {
	switch {
	case code == 429:
		return metricconv.HttpStatusClassRateLimited, true
	case code >= 500:
		return metricconv.HttpStatusClassServerError, true
	case code >= 400:
		return metricconv.HttpStatusClassClientError, true
	default:
		return "", false
	}
}

// RecordEmbedDimensionMismatch records the §6 hard-contract violation: a vector
// whose length != the store's expected dimension (embed.dimension_mismatch).
func (m *Metrics) RecordEmbedDimensionMismatch(ctx context.Context, model string, expectedDim int) {
	if m == nil {
		return
	}
	m.embedDimMismatch.Add(ctx, 1, metric.WithAttributes(
		attribute.String(metricconv.AttrGenAiRequestModel, model),
		attribute.Int(metricconv.AttrRagEmbedExpectedDim, expectedDim),
	))
}

// RecordEmbedGatewayError records a LiteLLM embed-path gateway error by status
// class (§4.4 embed.gateway.errors).
func (m *Metrics) RecordEmbedGatewayError(ctx context.Context, model string, class metricconv.HttpStatusClass) {
	if m == nil {
		return
	}
	m.embedGatewayErrors.Add(ctx, 1, metric.WithAttributes(
		attribute.String(metricconv.AttrHttpStatusClass, string(class)),
		attribute.String(metricconv.AttrGenAiRequestModel, model),
	))
}

// ----- §4.5 store + retrieve recording --------------------------------------

// RecordStoreOp records a pgvector operation's latency under the DB semconv
// db.client.operation.duration (§4.5) — the hot ANN query path and upsert cost.
func (m *Metrics) RecordStoreOp(ctx context.Context, op string, d time.Duration) {
	if m == nil {
		return
	}
	m.dbOpDuration.Record(ctx, d.Seconds(), metric.WithAttributes(
		attribute.String(metricconv.AttrDbOperationName, op),
		attribute.String(attrDbSystemName, dbSystemPostgreSQL),
	))
}

// SetStoreUp publishes the authoritative operator-vantage reachability gauge
// (§4.5/§9 store.up): 1 when the last store operation / health ping succeeded.
func (m *Metrics) SetStoreUp(ctx context.Context, up bool) {
	if m == nil {
		return
	}
	v := int64(0)
	if up {
		v = 1
	}
	m.storeUp.Record(ctx, v)
}

// RecordStoreError records a pgvector store failure by operation and reason
// (§4.5 store.errors), feeding the degradation signals.
func (m *Metrics) RecordStoreError(ctx context.Context, op string, reason StoreErrorReason) {
	if m == nil {
		return
	}
	m.storeErrors.Add(ctx, 1, metric.WithAttributes(
		attribute.String(metricconv.AttrDbOperationName, op),
		attribute.String(metricconv.AttrRagStoreErrorReason, string(reason)),
	))
}

// RecordRetrieval records the outcome of one owner-scoped retrieval (§4.5):
// match count and, when matches exist, the top similarity score; an empty
// result also increments retrieve.empty with the given reason.
func (m *Metrics) RecordRetrieval(ctx context.Context, persona string, matches int, topScore float64) {
	if m == nil {
		return
	}
	set := metric.WithAttributes(personaAttr(persona))
	m.retrieveMatches.Record(ctx, int64(matches), set)
	if matches > 0 {
		m.retrieveTopScore.Record(ctx, topScore, set)
	}
}

// RecordRetrievalEmpty records a retrieval that returned no usable match (§4.5
// retrieve.empty) — the degradation trigger (§5.4).
func (m *Metrics) RecordRetrievalEmpty(ctx context.Context, persona string, reason RetrieveEmptyReason) {
	if m == nil {
		return
	}
	m.retrieveEmpty.Add(ctx, 1, metric.WithAttributes(
		personaAttr(persona),
		attribute.String(metricconv.AttrRagRetrieveEmptyReason, string(reason)),
	))
}

// ----- §4.6 writer-conditioning recording -----------------------------------

// RecordCondition records one writer-conditioning application: its position on
// the §5.4 fallback ladder (condition.total{outcome,persona}) and the time it
// added to the writer path (condition.duration{outcome}). persona is the
// attributed DevRelPersona, empty on the no_persona (Phase-1 style-only) path.
// The counter is the single best "is conditioning healthy" signal (§4.6); the
// SLI (§10) reads it as degraded.total / total{outcome!=no_persona}.
func (m *Metrics) RecordCondition(ctx context.Context, persona string, outcome metricconv.ConditionOutcome, d time.Duration) {
	if m == nil {
		return
	}
	outcomeAttr := attribute.String(metricconv.AttrConditionOutcome, string(outcome))
	m.conditionTotal.Add(ctx, 1, metric.WithAttributes(personaAttr(persona), outcomeAttr))
	m.conditionDuration.Record(ctx, d.Seconds(), metric.WithAttributes(outcomeAttr))
}

// RecordConditionDegraded records the root cause of a degraded conditioning run
// (condition.degraded.total{reason,persona}) — the by-cause breakdown of the
// `degraded` outcome that drives the §10.A3 alert. Record it ONLY for the
// degraded outcome: the sparse-corpus fallback (self_description_only) is
// correct behaviour (§5.4), not a degrade, and is deliberately excluded from the
// conditioning-availability error budget (§10).
func (m *Metrics) RecordConditionDegraded(ctx context.Context, persona string, reason metricconv.ConditionDegradedReason) {
	if m == nil {
		return
	}
	m.conditionDegraded.Add(ctx, 1, metric.WithAttributes(
		personaAttr(persona),
		attribute.String(metricconv.AttrConditionDegradedReason, string(reason)),
	))
}

// ----- §6.1 index-time spans ------------------------------------------------

// Tracer is the persona/RAG tracer for callers that need to start spans the
// typed helpers below do not cover.
func (m *Metrics) Tracer() trace.Tracer {
	if m == nil || m.tracer == nil {
		return tnoop.NewTracerProvider().Tracer(scopeName)
	}
	return m.tracer
}

// StartReconcile opens the root §6.1 index-time span for a reconcile, carrying
// the persona name and the HASHED owner (§5.1 — never the raw subject).
func (m *Metrics) StartReconcile(ctx context.Context, persona, ownerSubject string) (context.Context, trace.Span) {
	return m.Tracer().Start(ctx, "DevRelPersona.Reconcile", trace.WithAttributes(
		attribute.String(metricconv.AttrPersonaName, persona),
		attribute.String("contentremixer.owner.hash", OwnerHash(ownerSubject)),
	))
}

// StartScrape opens the per-reference rag.scrape span. source.url is a span
// attribute (allowed, §3-3/§6.1), never a metric label.
func (m *Metrics) StartScrape(ctx context.Context, kind, sourceURL string) (context.Context, trace.Span) {
	return m.Tracer().Start(ctx, "rag.scrape", trace.WithAttributes(
		attribute.String(metricconv.AttrReferenceKind, kind),
		attribute.String("contentremixer.rag.source.url", sourceURL),
	))
}

// StartChunk opens the rag.chunk span.
func (m *Metrics) StartChunk(ctx context.Context, kind string) (context.Context, trace.Span) {
	return m.Tracer().Start(ctx, "rag.chunk", trace.WithAttributes(
		attribute.String(metricconv.AttrReferenceKind, kind),
	))
}

// StartEmbed opens the rag.embed span (GenAI embeddings).
func (m *Metrics) StartEmbed(ctx context.Context, model string, chunks int) (context.Context, trace.Span) {
	return m.Tracer().Start(ctx, "rag.embed", trace.WithAttributes(
		attribute.String(attrGenAiOperationName, genAiOperationEmbeddings),
		attribute.String(metricconv.AttrGenAiRequestModel, model),
		attribute.Int("contentremixer.rag.embed.chunks", chunks),
	))
}

// StartStoreUpsert opens the rag.store.upsert DB span.
func (m *Metrics) StartStoreUpsert(ctx context.Context, chunks int) (context.Context, trace.Span) {
	return m.Tracer().Start(ctx, "rag.store.upsert", trace.WithAttributes(
		attribute.String(metricconv.AttrDbOperationName, "upsert"),
		attribute.String(attrDbSystemName, dbSystemPostgreSQL),
		attribute.String("db.collection.name", "persona_chunks"),
		attribute.Int("contentremixer.rag.store.chunks", chunks),
	))
}

// StartStoreQuery opens the rag.store.query DB span (the hot ANN path).
func (m *Metrics) StartStoreQuery(ctx context.Context) (context.Context, trace.Span) {
	return m.Tracer().Start(ctx, "rag.store.query", trace.WithAttributes(
		attribute.String(metricconv.AttrDbOperationName, "query"),
		attribute.String(attrDbSystemName, dbSystemPostgreSQL),
		attribute.String("db.collection.name", "persona_chunks"),
	))
}

// ----- §6.2 query-time (writer-conditioning) spans --------------------------

// StartCondition opens the §6.2 persona.condition span — the query-time
// conditioning span that links into the Phase-1 recycle-run writer step. It is a
// child of whatever span the recycle run already put on ctx (the writer step):
// no new propagation, it reuses the context.Context chain threaded through
// Writer.Write → PersonaConditioner.Condition. It carries the persona name and
// the HASHED owner (§5.1, never the raw subject); the resolved outcome + the
// degraded flag are set at the end via AnnotateCondition. The rag.retrieve.embed
// and rag.store.query spans the conditioner's retrieval starts under the
// returned ctx nest beneath it, completing the §6.2 tree.
func (m *Metrics) StartCondition(ctx context.Context, persona, ownerSubject string) (context.Context, trace.Span) {
	return m.Tracer().Start(ctx, "persona.condition", trace.WithAttributes(
		attribute.String(metricconv.AttrPersonaName, persona),
		attribute.String("contentremixer.owner.hash", OwnerHash(ownerSubject)),
	))
}

// StartRetrieveEmbed opens the §6.2 rag.retrieve.embed span: the GenAI
// embeddings call that turns the retrieval seed into a query vector, a child of
// persona.condition. seedChars is the seed length (a span attribute, never a
// metric label, §3); the seed text itself is never attached (§5.3).
func (m *Metrics) StartRetrieveEmbed(ctx context.Context, model string, seedChars int) (context.Context, trace.Span) {
	return m.Tracer().Start(ctx, "rag.retrieve.embed", trace.WithAttributes(
		attribute.String(attrGenAiOperationName, genAiOperationEmbeddings),
		attribute.String(metricconv.AttrGenAiRequestModel, model),
		attribute.Int("contentremixer.rag.retrieve.seed_chars", seedChars),
	))
}

// AnnotateCondition records the resolved fallback-ladder result on a
// persona.condition span (§6.2 {outcome, degraded}). degraded mirrors
// outcome==degraded onto a boolean so the §7 tail-sampling policy — "keep 100%
// when degraded==true OR outcome==degraded" — can match on a single attribute.
func AnnotateCondition(span trace.Span, outcome metricconv.ConditionOutcome, degraded bool) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.String(metricconv.AttrConditionOutcome, string(outcome)),
		attribute.Bool("contentremixer.condition.degraded", degraded),
	)
}

// ConditionDegradedEvent adds the §5 persona.condition.degraded WARN event to a
// persona.condition span: the trace-pillar carrier of the fallback-to-style-only
// signal, tagged with its root cause and the recycle run id. Exemplar/chunk text
// is never attached (§5.3) — only the bounded reason and the run id.
func ConditionDegradedEvent(span trace.Span, reason metricconv.ConditionDegradedReason, runID string) {
	if span == nil {
		return
	}
	span.AddEvent("persona.condition.degraded", trace.WithAttributes(
		attribute.String(metricconv.AttrConditionDegradedReason, string(reason)),
		attribute.String("contentremixer.run.id", runID),
	))
}

// EndSpan ends span, recording err as an ERROR status + exception event when
// non-nil. A nil err leaves the span's existing status untouched so per-
// reference best-effort semantics stay legible (§6.1): a child scrape span can
// be ERROR while the parent reconcile stays OK.
func EndSpan(span trace.Span, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// ----- phase-name mapping ----------------------------------------------------

// MapPhase maps the DevRelPersona CRD phase strings (api/v1alpha1:
// Idle/Pending/Scraping/Embedding/Ready/Error) onto the strategy's phase enum
// (metricconv.PersonaPhase*: Pending/Scraping/Indexing/Ready/Degraded) so the
// contentremixer.persona.phase metric stays within the registry's bounded value
// set and §10 alerts (phase=Degraded) fire. Embedding→Indexing and Error→
// Degraded; Idle (a terminal "no references" state with no enum counterpart) is
// passed through unchanged. See the drift note in the O11y-2 change log: the
// CRD and the frozen design the strategy was written against diverged on these
// names.
func MapPhase(crdPhase string) string {
	switch crdPhase {
	case "Embedding":
		return string(metricconv.PersonaPhaseIndexing)
	case "Error":
		return string(metricconv.PersonaPhaseDegraded)
	default:
		return crdPhase
	}
}

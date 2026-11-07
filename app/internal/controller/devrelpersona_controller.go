package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry"
	"github.com/ContentRemixer/Contentremixer/app/internal/telemetry/metricconv"
)

// DevRelPersonaReconciler reconciles a DevRelPersona object (P2-A).
//
// Scope boundary (the sibling contract): this reconciler accepts the spec and
// EDGE-TRIGGERS ingestion — nothing more. It never scrapes (P2-B) and never
// embeds (P2-C). On a spec.references change it drives the persona to
// Pending and raises IngestionPending so the downstream controllers pick the
// work up; it then records the reference fingerprint so a steady-state
// resync does not re-trigger. status.exemplarCount / status.lastEmbeddedAt
// belong to P2-C and are read-only here.
type DevRelPersonaReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=contentremixer.io,resources=devrelpersonas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=contentremixer.io,resources=devrelpersonas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=contentremixer.io,resources=devrelpersonas/finalizers,verbs=update

// Reconcile accepts the persona and edge-triggers scrape→embed work when the
// reference list changes.
func (r *DevRelPersonaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var persona contentremixerv1alpha1.DevRelPersona
	if err := r.Get(ctx, req.NamespacedName, &persona); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// §6.1 index-time trace root span + §4.1/§5 telemetry envelope. The span is
	// opened once the object exists so a NotFound (deletion) is not traced as a
	// reconcile. Owner is only ever exposed hashed (§5.1/PII policy).
	tel := telemetry.Default()
	ctx, span := tel.StartReconcile(ctx, persona.Name, persona.Spec.Owner.Subject)
	started := time.Now()
	ownerHash := telemetry.OwnerHash(persona.Spec.Owner.Subject)
	traceID := span.SpanContext().TraceID().String()
	oldPhase := persona.Status.Phase
	log.V(1).Info("persona.reconcile.start",
		"event", "persona.reconcile.start", "persona", persona.Name, "owner_hash", ownerHash,
		"generation", persona.Generation, "phase", oldPhase, "trace_id", traceID)

	status := persona.Status.DeepCopy()

	// Acceptance (defence in depth behind the webhook): a persona must have an
	// owner. We never reject here — admission already does — but we surface it
	// so an owner-less object that slipped in (e.g. webhook disabled) is
	// visibly degraded rather than silently enqueued.
	accepted := persona.Spec.Owner.Subject != ""
	if accepted {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: contentremixerv1alpha1.PersonaConditionAccepted, Status: metav1.ConditionTrue,
			Reason: "Admitted", Message: fmt.Sprintf("owned by %q", persona.Spec.Owner.Subject),
		})
	} else {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: contentremixerv1alpha1.PersonaConditionAccepted, Status: metav1.ConditionFalse,
			Reason: "OwnerMissing", Message: "spec.owner.subject is empty",
		})
	}

	hash := referenceHash(persona.Spec.References)
	changed := hash != status.ObservedReferenceHash

	switch {
	case !accepted:
		// Not enqueued while unowned; leave any existing phase untouched.
	case len(persona.Spec.References) == 0:
		// Nothing to ingest. Idle is a terminal P2-A state; no exemplars are
		// implied by it (P2-C owns exemplarCount).
		if changed {
			status.Phase = contentremixerv1alpha1.PersonaPhaseIdle
			meta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type: contentremixerv1alpha1.PersonaConditionIngestionPending, Status: metav1.ConditionFalse,
				Reason: "NoReferences", Message: "no references to ingest",
			})
		}
	case changed:
		// References changed -> enqueue scrape→embed for P2-B/P2-C.
		status.Phase = contentremixerv1alpha1.PersonaPhasePending
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: contentremixerv1alpha1.PersonaConditionIngestionPending, Status: metav1.ConditionTrue,
			Reason: "ReferencesChanged", Message: fmt.Sprintf("%d reference(s) to (re)ingest", len(persona.Spec.References)),
		})
		// Hold Ready False until P2-C re-embeds the new set, so the Booth API
		// never serves a stale index as current.
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: contentremixerv1alpha1.PersonaConditionReady, Status: metav1.ConditionFalse,
			Reason: "IngestionPending", Message: "waiting for scrape+embed of the updated references",
		})
	}

	// Record the fingerprint once we've acted on it, so a steady-state resync
	// (or a status write by a sibling) does not re-trigger ingestion.
	if accepted {
		status.ObservedReferenceHash = hash
	}

	var retErr error
	if !personaStatusEqual(persona.Status, *status) {
		persona.Status = *status
		retErr = r.Status().Update(ctx, &persona)
	}

	// §4.1 metrics + §5 reconcile.end log. result maps the reconcile outcome:
	// a write error is error; a persona left in the Error phase is a (correct,
	// best-effort) degraded outcome; otherwise success.
	newPhase := status.Phase
	result := metricconv.ResultSuccess
	switch {
	case retErr != nil:
		result = metricconv.ResultError
	case newPhase == contentremixerv1alpha1.PersonaPhaseError:
		result = metricconv.ResultDegraded
	}
	dur := time.Since(started)
	tel.RecordReconcile(ctx, persona.Name, dur, result)
	tel.SetPhase(ctx, persona.Name, oldPhase, newPhase)
	// Corpus gauges are read-only projections of P2-C-owned status. The CRD
	// carries ExemplarCount (embedded units ≈ chunks) and LastEmbeddedAt;
	// indexed_references has no faithful status source yet (drift note).
	tel.ObserveChunkCount(ctx, persona.Name, persona.Status.ExemplarCount)
	if persona.Status.LastEmbeddedAt != nil {
		tel.ObserveLastIndexedAge(ctx, persona.Name, persona.Status.LastEmbeddedAt.Time, time.Now())
	}
	// A spec.references change is the edge-trigger for a re-index (§4.1).
	if accepted && changed && len(persona.Spec.References) > 0 {
		tel.RecordReindex(ctx, persona.Name, metricconv.ReindexTriggerSpecChange)
	}

	log.V(1).Info("persona.reconcile.end",
		"event", "persona.reconcile.end", "persona", persona.Name, "owner_hash", ownerHash,
		"generation", persona.Generation, "phase", newPhase, "result", string(result),
		"duration_ms", dur.Milliseconds(), "accepted", accepted, "refChanged", changed, "trace_id", traceID)
	telemetry.EndSpan(span, retErr)
	return ctrl.Result{}, retErr
}

// SetupWithManager wires the reconciler.
func (r *DevRelPersonaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&contentremixerv1alpha1.DevRelPersona{}).
		Named("devrelpersona").
		Complete(r)
}

// referenceHash is the order-sensitive fingerprint of the reference list used
// to edge-trigger ingestion. References is +listType=atomic, so order is part
// of the desired state and a reorder is a legitimate re-ingest trigger.
func referenceHash(refs []contentremixerv1alpha1.PersonaReference) string {
	h := sha256.New()
	for _, ref := range refs {
		// Length-prefix each field so {"ab","c"} and {"a","bc"} never collide.
		_, _ = fmt.Fprintf(h, "%d:%s|%d:%s|", len(ref.Kind), ref.Kind, len(ref.URL), ref.URL)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func personaStatusEqual(a, b contentremixerv1alpha1.DevRelPersonaStatus) bool {
	if a.Phase != b.Phase ||
		a.ExemplarCount != b.ExemplarCount ||
		a.ObservedReferenceHash != b.ObservedReferenceHash {
		return false
	}
	if (a.LastEmbeddedAt == nil) != (b.LastEmbeddedAt == nil) {
		return false
	}
	if a.LastEmbeddedAt != nil && !a.LastEmbeddedAt.Equal(b.LastEmbeddedAt) {
		return false
	}
	if len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for i := range a.Conditions {
		if a.Conditions[i] != b.Conditions[i] {
			return false
		}
	}
	return true
}

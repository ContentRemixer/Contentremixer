package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// ReferenceRepoReconciler reconciles a ReferenceRepo object.
//
// Design §2.2: resolve the GitHub PAT from the TokenRef Secret, validate
// labelRoutes, mirror RouteCount/TokenResolved into status. Secret existence
// is a controller concern (not admission) so the CR can be applied before
// its Secrets exist.
type ReferenceRepoReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=contentremixer.io,resources=referencerepos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=contentremixer.io,resources=referencerepos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=contentremixer.io,resources=referencerepos/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile resolves the PAT + webhook HMAC Secret refs, re-validates
// labelRoutes and updates status.
func (r *ReferenceRepoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var repo contentremixerv1alpha1.ReferenceRepo
	if err := r.Get(ctx, req.NamespacedName, &repo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status := repo.Status.DeepCopy()

	// Resolve PAT Secret (TokenRef).
	tokenErr := resolveSecretKey(ctx, r, repo.Namespace, repo.Spec.TokenRef)
	status.TokenResolved = tokenErr == nil
	setRepoCondition(status, "TokenResolved", status.TokenResolved, tokenErr)

	// Resolve webhook HMAC Secret (WebhookSecretRef).
	hookErr := resolveSecretKey(ctx, r, repo.Namespace, repo.Spec.WebhookSecretRef)
	setRepoCondition(status, "WebhookSecretResolved", hookErr == nil, hookErr)

	// Re-validate labelRoutes (defence in depth behind the webhook).
	status.RouteCount = len(repo.Spec.LabelRoutes)
	routesErr := ValidateLabelRoutes(repo.Spec.LabelRoutes)
	setRepoCondition(status, "RoutesValid", routesErr == nil, routesErr)

	ready := tokenErr == nil && hookErr == nil && routesErr == nil
	msg := fmt.Sprintf("token=%t webhookSecret=%t routes=%d", status.TokenResolved, hookErr == nil, status.RouteCount)
	if ready {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: "Ready", Status: metav1.ConditionTrue, Reason: "Reconciled", Message: msg,
		})
	} else {
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type: "Ready", Status: metav1.ConditionFalse, Reason: "Unresolved", Message: msg,
		})
	}

	if !repoStatusEqual(repo.Status, *status) {
		repo.Status = *status
		if err := r.Status().Update(ctx, &repo); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.V(1).Info("reconciled ReferenceRepo", "ready", ready, "routes", status.RouteCount)
	return ctrl.Result{}, nil
}

// SetupWithManager wires the reconciler. Secrets are watched too (design
// §2.2 fail-closed recovery): the CR may be applied before its Secrets
// exist, so a create/update of a Secret named by any ReferenceRepo's
// spec.tokenRef or spec.webhookSecretRef must enqueue that ReferenceRepo
// instead of waiting for the 10h resync.
func (r *ReferenceRepoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&contentremixerv1alpha1.ReferenceRepo{}).
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				var list contentremixerv1alpha1.ReferenceRepoList
				if err := r.List(ctx, &list, client.InNamespace(o.GetNamespace())); err != nil {
					return nil
				}
				reqs := make([]reconcile.Request, 0, len(list.Items))
				for i := range list.Items {
					if list.Items[i].Spec.TokenRef.Name == o.GetName() ||
						list.Items[i].Spec.WebhookSecretRef.Name == o.GetName() {
						reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
							Namespace: list.Items[i].Namespace, Name: list.Items[i].Name,
						}})
					}
				}
				return reqs
			})).
		Named("referencerepo").
		Complete(r)
}

// resolveSecretKey fetches a Secret/key pair and reports why it failed.
func resolveSecretKey(ctx context.Context, r *ReferenceRepoReconciler, ns string, ref contentremixerv1alpha1.SecretKeyRef) error {
	var s corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, &s)
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("secret %q not found in namespace %q", ref.Name, ns)
	}
	if err != nil {
		return err
	}
	if _, ok := s.Data[ref.Key]; !ok {
		return fmt.Errorf("secret %q has no key %q", ref.Name, ref.Key)
	}
	return nil
}

// ValidateLabelRoutes re-checks the webhook rules on the current spec:
// duplicate labels and unknown formats.
func ValidateLabelRoutes(routes []contentremixerv1alpha1.LabelRoute) error {
	seen := map[string]bool{}
	for i, rt := range routes {
		if seen[rt.Label] {
			return fmt.Errorf("duplicate label %q at index %d in spec.labelRoutes", rt.Label, i)
		}
		seen[rt.Label] = true
		if !contains(contentremixerv1alpha1.KnownInputFormats, rt.InputFormat) {
			return fmt.Errorf("unknown inputFormat %q at index %d", rt.InputFormat, i)
		}
		if !contains(contentremixerv1alpha1.KnownOutputFormats, rt.OutputFormat) {
			return fmt.Errorf("unknown outputFormat %q at index %d (styles/%s.md missing from the Phase-1 set)", rt.OutputFormat, i, rt.OutputFormat)
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func setRepoCondition(status *contentremixerv1alpha1.ReferenceRepoStatus, condType string, ok bool, err error) {
	st, reason, msg := metav1.ConditionTrue, "Resolved", "resolved successfully"
	if !ok {
		st, reason, msg = metav1.ConditionFalse, "Unresolved", ""
		if err != nil {
			msg = err.Error()
		}
	}
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: condType, Status: st, Reason: reason, Message: msg,
	})
}

func repoStatusEqual(a, b contentremixerv1alpha1.ReferenceRepoStatus) bool {
	if a.RouteCount != b.RouteCount || a.TokenResolved != b.TokenResolved {
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

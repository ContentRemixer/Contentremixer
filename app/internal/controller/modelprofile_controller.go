// Package controller contains the reconcilers for the ContentRemixer CRDs.
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
	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
)

// ModelProfileReconciler reconciles a ModelProfile object.
//
// Design §2.1: the controller renders the LiteLLM config from ALL
// ModelProfiles in the namespace and re-checks the exactly-one-default
// invariant (defence in depth behind the validating webhook).
type ModelProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=contentremixer.io,resources=modelprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=contentremixer.io,resources=modelprofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=contentremixer.io,resources=modelprofiles/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile lists every ModelProfile in the namespace, re-checks the
// default/alias invariants, renders the litellm-config ConfigMap and updates
// this profile's status.
func (r *ModelProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var profile contentremixerv1alpha1.ModelProfile
	if err := r.Get(ctx, req.NamespacedName, &profile); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var all contentremixerv1alpha1.ModelProfileList
	if err := r.List(ctx, &all, client.InNamespace(profile.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	// Defence-in-depth invariant re-checks (webhook is the primary gate).
	defaults := []string{}
	aliasOwners := map[string][]string{}
	for i := range all.Items {
		p := all.Items[i]
		if p.Spec.Default {
			defaults = append(defaults, p.Name)
		}
		aliasOwners[p.Spec.LiteLLMName] = append(aliasOwners[p.Spec.LiteLLMName], p.Name)
	}
	dupAliases := map[string]bool{}
	for alias, owners := range aliasOwners {
		if len(owners) > 1 {
			dupAliases[alias] = true
		}
	}

	// Render the litellm-config ConfigMap from all profiles (minimal renderer;
	// full wiring lands with subtask C).
	cm := litellm.DesiredConfigMap(profile.Namespace, all.Items)
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}, existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, cm); err != nil {
			return ctrl.Result{}, fmt.Errorf("creating litellm-config ConfigMap: %w", err)
		}
	case err != nil:
		return ctrl.Result{}, err
	default:
		if existing.Data == nil {
			existing.Data = map[string]string{}
		}
		if existing.Data[litellm.ConfigMapKey] != cm.Data[litellm.ConfigMapKey] {
			existing.Data[litellm.ConfigMapKey] = cm.Data[litellm.ConfigMapKey]
			if err := r.Update(ctx, existing); err != nil {
				return ctrl.Result{}, fmt.Errorf("updating litellm-config ConfigMap: %w", err)
			}
		}
	}
	rendered := cm.Data[litellm.ConfigMapKey] != ""

	status := profile.Status.DeepCopy()
	status.Rendered = false
	for i := range all.Items {
		if all.Items[i].Spec.LiteLLMName == profile.Spec.LiteLLMName {
			status.Rendered = rendered
			break
		}
	}

	// Ready condition.
	switch {
	case len(defaults) > 1:
		setCondition(status, "Ready", metav1.ConditionFalse, "DuplicateDefault",
			fmt.Sprintf("%d ModelProfiles set spec.default=true (%v); exactly one allowed", len(defaults), defaults))
	case dupAliases[profile.Spec.LiteLLMName]:
		setCondition(status, "Ready", metav1.ConditionFalse, "AliasConflict",
			fmt.Sprintf("litellmName %q is claimed by %v", profile.Spec.LiteLLMName, aliasOwners[profile.Spec.LiteLLMName]))
	case !status.Rendered:
		setCondition(status, "Ready", metav1.ConditionFalse, "RenderFailed", "profile not present in rendered litellm-config")
	default:
		setCondition(status, "Ready", metav1.ConditionTrue, "Rendered", "profile rendered into litellm-config")
	}

	// Zero-default degraded condition (design §2.1a): still renders, but the
	// pipeline must fail closed rather than guess a default.
	if len(defaults) == 0 {
		setCondition(status, "DefaultMissing", metav1.ConditionTrue, "NoDefault",
			"no ModelProfile sets spec.default=true; system default unavailable (degraded)")
	} else {
		setCondition(status, "DefaultMissing", metav1.ConditionFalse, "DefaultPresent", "a default ModelProfile exists")
	}

	if !apiequalStatus(profile.Status, *status) {
		profile.Status = *status
		if err := r.Status().Update(ctx, &profile); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.V(1).Info("reconciled ModelProfile", "rendered", status.Rendered, "defaults", defaults)
	return ctrl.Result{}, nil
}

// SetupWithManager wires the reconciler. Any ModelProfile change in a
// namespace re-reconciles every profile in that namespace, because the
// rendered config and the default/alias invariants are cross-object.
func (r *ModelProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&contentremixerv1alpha1.ModelProfile{}).
		Watches(&contentremixerv1alpha1.ModelProfile{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				var list contentremixerv1alpha1.ModelProfileList
				if err := r.List(ctx, &list, client.InNamespace(o.GetNamespace())); err != nil {
					return nil
				}
				reqs := make([]reconcile.Request, 0, len(list.Items))
				for i := range list.Items {
					reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
						Namespace: list.Items[i].Namespace, Name: list.Items[i].Name,
					}})
				}
				return reqs
			})).
		Named("modelprofile").
		Complete(r)
}

func setCondition(status *contentremixerv1alpha1.ModelProfileStatus, condType string, condStatus metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type: condType, Status: condStatus, Reason: reason, Message: msg,
		ObservedGeneration: 0,
	})
}

func apiequalStatus(a, b contentremixerv1alpha1.ModelProfileStatus) bool {
	if a.Rendered != b.Rendered {
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

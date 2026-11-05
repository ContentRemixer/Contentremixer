// Package webhook contains the validating admission webhooks for the
// ContentRemixer CRDs (design §2.1a, §2.2a).
package webhook

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// ModelProfileCustomValidator enforces the cross-object ModelProfile
// invariants that OpenAPI cannot express (design §2.1a):
//   - exactly-one-default: a create/update setting spec.default=true is
//     rejected when ANOTHER ModelProfile in the namespace is already default.
//     The webhook never auto-flips the previous default.
//   - litellmName uniqueness across the namespace.
//
// Zero defaults is allowed (mid-edit state); the controller surfaces it as a
// degraded DefaultMissing condition and the pipeline fails closed.
type ModelProfileCustomValidator struct {
	Client client.Client
}

var _ admission.CustomValidator = &ModelProfileCustomValidator{}

// SetupWithManager registers the validating webhook with the manager.
func (v *ModelProfileCustomValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&contentremixerv1alpha1.ModelProfile{}).
		WithValidator(v).
		Complete()
}

// +kubebuilder:webhook:path=/validate-contentremixer-io-v1alpha1-modelprofile,mutating=false,failurePolicy=fail,sideEffects=None,groups=contentremixer.io,resources=modelprofiles,verbs=create;update,versions=v1alpha1,name=vmodelprofile-v1alpha1.contentremixer.io,admissionReviewVersions=v1

// ValidateCreate implements admission.CustomValidator.
func (v *ModelProfileCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	mp, ok := obj.(*contentremixerv1alpha1.ModelProfile)
	if !ok {
		return nil, fmt.Errorf("expected a ModelProfile but got %T", obj)
	}
	logf.FromContext(ctx).Info("validation for ModelProfile create", "name", mp.Name)
	return nil, v.validate(ctx, mp)
}

// ValidateUpdate implements admission.CustomValidator.
func (v *ModelProfileCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	mp, ok := newObj.(*contentremixerv1alpha1.ModelProfile)
	if !ok {
		return nil, fmt.Errorf("expected a ModelProfile but got %T", newObj)
	}
	logf.FromContext(ctx).Info("validation for ModelProfile update", "name", mp.Name)
	return nil, v.validate(ctx, mp)
}

// ValidateDelete implements admission.CustomValidator.
func (v *ModelProfileCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validate lists the OTHER ModelProfiles in the namespace and rejects
// default/alias collisions.
func (v *ModelProfileCustomValidator) validate(ctx context.Context, mp *contentremixerv1alpha1.ModelProfile) error {
	var all contentremixerv1alpha1.ModelProfileList
	if err := v.Client.List(ctx, &all, client.InNamespace(mp.Namespace)); err != nil {
		return err
	}

	var errs field.ErrorList
	for i := range all.Items {
		other := all.Items[i]
		// Self-match: on UPDATE the existing object appears in the list (match by
		// UID or name). On CREATE no other object with the same name can survive,
		// so a name match is a safe self-skip in both paths.
		if other.UID == mp.UID || other.Name == mp.Name {
			continue
		}
		if mp.Spec.Default && other.Spec.Default {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "default"),
				true,
				fmt.Sprintf("another ModelProfile (%s) is already the default; unset it first", other.Name),
			))
		}
		if mp.Spec.LiteLLMName == other.Spec.LiteLLMName {
			errs = append(errs, field.Duplicate(
				field.NewPath("spec", "litellmName"),
				fmt.Sprintf("litellmName %q already claimed by ModelProfile %s", mp.Spec.LiteLLMName, other.Name),
			))
		}
	}
	if len(errs) > 0 {
		return apierrors.NewInvalid(
			contentremixerv1alpha1.GroupVersion.WithKind("ModelProfile").GroupKind(),
			mp.Name, errs)
	}
	return nil
}

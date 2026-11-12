package webhook

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// DevRelPersonaCustomValidator enforces the DevRelPersona admission rules
// (design §6 ownership, P2-A). It follows the Phase-1 ModelProfile/
// ReferenceRepo pattern: field-level enum/pattern checks live in the CRD
// OpenAPI schema and value defaulting is schema-driven (+kubebuilder:default),
// while this validating webhook adds the cross-field invariants OpenAPI
// cannot express:
//   - require owner: spec.owner.subject must be non-empty (unlike the
//     Admin-owned Phase-1 CRDs, a persona is always owned by a DevRel);
//   - reject unknown reference kind: a defence-in-depth re-check of the
//     CRD enum so the error message names the allowed kinds;
//   - reject unknown content-format-target content type: the same
//     defence-in-depth re-check for spec.contentFormatTargets (D1b, ISI-7370).
type DevRelPersonaCustomValidator struct{}

var _ admission.CustomValidator = &DevRelPersonaCustomValidator{}

// SetupWithManager registers the validating webhook with the manager.
func (v *DevRelPersonaCustomValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&contentremixerv1alpha1.DevRelPersona{}).
		WithValidator(v).
		Complete()
}

// +kubebuilder:webhook:path=/validate-contentremixer-io-v1alpha1-devrelpersona,mutating=false,failurePolicy=fail,sideEffects=None,groups=contentremixer.io,resources=devrelpersonas,verbs=create;update,versions=v1alpha1,name=vdevrelpersona-v1alpha1.contentremixer.io,admissionReviewVersions=v1

// ValidateCreate implements admission.CustomValidator.
func (v *DevRelPersonaCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	p, ok := obj.(*contentremixerv1alpha1.DevRelPersona)
	if !ok {
		return nil, fmt.Errorf("expected a DevRelPersona but got %T", obj)
	}
	logf.FromContext(ctx).Info("validation for DevRelPersona create", "name", p.Name)
	return nil, validateDevRelPersona(p)
}

// ValidateUpdate implements admission.CustomValidator.
func (v *DevRelPersonaCustomValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	p, ok := newObj.(*contentremixerv1alpha1.DevRelPersona)
	if !ok {
		return nil, fmt.Errorf("expected a DevRelPersona but got %T", newObj)
	}
	logf.FromContext(ctx).Info("validation for DevRelPersona update", "name", p.Name)
	return nil, validateDevRelPersona(p)
}

// ValidateDelete implements admission.CustomValidator.
func (v *DevRelPersonaCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateDevRelPersona(p *contentremixerv1alpha1.DevRelPersona) error {
	var errs field.ErrorList

	if p.Spec.Owner.Subject == "" {
		errs = append(errs, field.Required(
			field.NewPath("spec", "owner", "subject"),
			"a DevRelPersona must be owned by a DevRel (spec.owner.subject is required)",
		))
	}

	for i, ref := range p.Spec.References {
		if !knownReferenceKind(ref.Kind) {
			errs = append(errs, field.NotSupported(
				field.NewPath("spec", "references").Index(i).Child("kind"),
				ref.Kind,
				contentremixerv1alpha1.KnownReferenceKinds,
			))
		}
	}

	for i, t := range p.Spec.ContentFormatTargets {
		if !knownContentFormatType(t.ContentType) {
			errs = append(errs, field.NotSupported(
				field.NewPath("spec", "contentFormatTargets").Index(i).Child("contentType"),
				t.ContentType,
				contentremixerv1alpha1.KnownContentFormatTypes,
			))
		}
	}

	if len(errs) > 0 {
		return apierrors.NewInvalid(
			contentremixerv1alpha1.GroupVersion.WithKind("DevRelPersona").GroupKind(),
			p.Name, errs)
	}
	return nil
}

func knownReferenceKind(kind string) bool {
	for _, k := range contentremixerv1alpha1.KnownReferenceKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func knownContentFormatType(t contentremixerv1alpha1.ContentFormatType) bool {
	for _, k := range contentremixerv1alpha1.KnownContentFormatTypes {
		if k == t {
			return true
		}
	}
	return false
}

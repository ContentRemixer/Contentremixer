// ContentStyle admission validation (ISI-7975 / plan W1).
package webhook

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// ContentStyleCustomValidator enforces the ContentStyle invariants that
// OpenAPI alone cannot express (ISI-7975 / plan W1):
//   - outputFormat must be a known writer format (the CRD enum is the first
//     gate; this is the re-check),
//   - guide must be non-empty after trimming,
//   - exactly one ContentStyle per outputFormat per namespace: duplicates
//     are rejected (GLOBAL scope - one set of guides shared across repos).
type ContentStyleCustomValidator struct {
	Client client.Client
}

var _ admission.CustomValidator = &ContentStyleCustomValidator{}

// SetupWithManager registers the validating webhook with the manager.
func (v *ContentStyleCustomValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&contentremixerv1alpha1.ContentStyle{}).
		WithValidator(v).
		Complete()
}

// +kubebuilder:webhook:path=/validate-contentremixer-io-v1alpha1-contentstyle,mutating=false,failurePolicy=fail,sideEffects=None,groups=contentremixer.io,resources=contentstyles,verbs=create;update,versions=v1alpha1,name=vcontentstyle-v1alpha1.contentremixer.io,admissionReviewVersions=v1

// ValidateCreate implements admission.CustomValidator.
func (v *ContentStyleCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	cs, ok := obj.(*contentremixerv1alpha1.ContentStyle)
	if !ok {
		return nil, fmt.Errorf("expected a ContentStyle but got %T", obj)
	}
	logf.FromContext(ctx).Info("validation for ContentStyle create", "name", cs.Name)
	return nil, v.validate(ctx, cs)
}

// ValidateUpdate implements admission.CustomValidator.
func (v *ContentStyleCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	cs, ok := newObj.(*contentremixerv1alpha1.ContentStyle)
	if !ok {
		return nil, fmt.Errorf("expected a ContentStyle but got %T", newObj)
	}
	logf.FromContext(ctx).Info("validation for ContentStyle update", "name", cs.Name)
	return nil, v.validate(ctx, cs)
}

// ValidateDelete implements admission.CustomValidator.
func (v *ContentStyleCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validate checks the field-level invariants, then lists the OTHER
// ContentStyles in the namespace and rejects outputFormat collisions.
func (v *ContentStyleCustomValidator) validate(ctx context.Context, cs *contentremixerv1alpha1.ContentStyle) error {
	var errs field.ErrorList

	if !knownOutputFormat(cs.Spec.OutputFormat) {
		errs = append(errs, field.NotSupported(
			field.NewPath("spec", "outputFormat"),
			cs.Spec.OutputFormat,
			contentremixerv1alpha1.KnownOutputFormats,
		))
	}
	if strings.TrimSpace(cs.Spec.Guide) == "" {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "guide"),
			cs.Spec.Guide,
			"guide must be a non-empty markdown style guide",
		))
	}

	var all contentremixerv1alpha1.ContentStyleList
	if err := v.Client.List(ctx, &all, client.InNamespace(cs.Namespace)); err != nil {
		return err
	}
	for i := range all.Items {
		other := all.Items[i]
		// Self-match: on UPDATE the existing object appears in the list (match
		// by UID or name); on CREATE no other object with the same name can
		// survive, so a name match is a safe self-skip in both paths.
		if other.UID == cs.UID || other.Name == cs.Name {
			continue
		}
		if other.Spec.OutputFormat == cs.Spec.OutputFormat {
			errs = append(errs, field.Duplicate(
				field.NewPath("spec", "outputFormat"),
				fmt.Sprintf("outputFormat %q already has a ContentStyle (%s); one guide per format", cs.Spec.OutputFormat, other.Name),
			))
		}
	}

	if len(errs) > 0 {
		return apierrors.NewInvalid(
			contentremixerv1alpha1.GroupVersion.WithKind("ContentStyle").GroupKind(),
			cs.Name, errs)
	}
	return nil
}

// knownOutputFormat reports whether f is one of the known writer formats.
func knownOutputFormat(f string) bool {
	for _, k := range contentremixerv1alpha1.KnownOutputFormats {
		if k == f {
			return true
		}
	}
	return false
}

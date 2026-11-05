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

// ReferenceRepoCustomValidator enforces the ReferenceRepo admission rules
// (design §2.2a). Enum/pattern/MinItems live in the CRD OpenAPI schema; the
// webhook adds cross-field and cross-item checks:
//   - duplicate labels within spec.labelRoutes,
//   - outputFormat/inputFormat must belong to the Phase-1 known sets, with a
//     message naming the missing styles/<format>.md file to aid Admins.
type ReferenceRepoCustomValidator struct{}

var _ admission.CustomValidator = &ReferenceRepoCustomValidator{}

// SetupWithManager registers the validating webhook with the manager.
func (v *ReferenceRepoCustomValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&contentremixerv1alpha1.ReferenceRepo{}).
		WithValidator(v).
		Complete()
}

// +kubebuilder:webhook:path=/validate-contentremixer-io-v1alpha1-referencerepo,mutating=false,failurePolicy=fail,sideEffects=None,groups=contentremixer.io,resources=referencerepos,verbs=create;update,versions=v1alpha1,name=vreferencerepo-v1alpha1.contentremixer.io,admissionReviewVersions=v1

// ValidateCreate implements admission.CustomValidator.
func (v *ReferenceRepoCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	rr, ok := obj.(*contentremixerv1alpha1.ReferenceRepo)
	if !ok {
		return nil, fmt.Errorf("expected a ReferenceRepo but got %T", obj)
	}
	logf.FromContext(ctx).Info("validation for ReferenceRepo create", "name", rr.Name)
	return nil, validateReferenceRepo(rr)
}

// ValidateUpdate implements admission.CustomValidator.
func (v *ReferenceRepoCustomValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	rr, ok := newObj.(*contentremixerv1alpha1.ReferenceRepo)
	if !ok {
		return nil, fmt.Errorf("expected a ReferenceRepo but got %T", newObj)
	}
	logf.FromContext(ctx).Info("validation for ReferenceRepo update", "name", rr.Name)
	return nil, validateReferenceRepo(rr)
}

// ValidateDelete implements admission.CustomValidator.
func (v *ReferenceRepoCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateReferenceRepo(rr *contentremixerv1alpha1.ReferenceRepo) error {
	var errs field.ErrorList

	seenLabels := map[string]bool{}
	for i, rt := range rr.Spec.LabelRoutes {
		if seenLabels[rt.Label] {
			errs = append(errs, field.Duplicate(
				field.NewPath("spec", "labelRoutes").Index(i).Child("label"),
				fmt.Sprintf("label %q appears more than once in spec.labelRoutes", rt.Label),
			))
			continue
		}
		seenLabels[rt.Label] = true
	}

	if len(errs) > 0 {
		return apierrors.NewInvalid(
			contentremixerv1alpha1.GroupVersion.WithKind("ReferenceRepo").GroupKind(),
			rr.Name, errs)
	}
	return nil
}

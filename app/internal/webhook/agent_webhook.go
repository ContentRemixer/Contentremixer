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

// AgentCustomValidator enforces the Agent invariants OpenAPI cannot express
// (ISI-7800 §4, the fail-closed companion to the ISI-7803 CRUD UI):
//
//   - skillKind ∈ the registry: the closed v1alpha1.KnownSkillKinds set. The CRD
//     OpenAPI enum already rejects an unknown kind at decode; this re-check is
//     defense in depth and names the known set in the error so a manifest that
//     drifts ahead of the compiled set fails closed with a clear message.
//   - at-most-one-default-per-kind: a create/update setting spec.default=true is
//     rejected when ANOTHER Agent of the same skillKind is already default. The
//     seeded rows are the canonical defaults; the webhook never auto-flips one.
//   - modelProfile resolves: a non-empty, non-"default" modelProfile MUST name an
//     existing ModelProfile's litellmName (ISI-7800 §2.2, same fail-closed stance
//     as the engine's model resolution).
//
// skillPacks are deliberately NOT validated here: unknown packs degrade (warn) at
// run time, they do not gate admission (AgentSpec.SkillPacks note / UX spec §1.4).
// Zero defaults for a kind is allowed (mid-edit state).
type AgentCustomValidator struct {
	Client client.Client
}

var _ admission.CustomValidator = &AgentCustomValidator{}

// SetupWithManager registers the validating webhook with the manager.
func (v *AgentCustomValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&contentremixerv1alpha1.Agent{}).
		WithValidator(v).
		Complete()
}

// +kubebuilder:webhook:path=/validate-contentremixer-io-v1alpha1-agent,mutating=false,failurePolicy=fail,sideEffects=None,groups=contentremixer.io,resources=agents,verbs=create;update,versions=v1alpha1,name=vagent-v1alpha1.contentremixer.io,admissionReviewVersions=v1

// ValidateCreate implements admission.CustomValidator.
func (v *AgentCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	a, ok := obj.(*contentremixerv1alpha1.Agent)
	if !ok {
		return nil, fmt.Errorf("expected an Agent but got %T", obj)
	}
	logf.FromContext(ctx).Info("validation for Agent create", "name", a.Name)
	return nil, v.validate(ctx, a)
}

// ValidateUpdate implements admission.CustomValidator.
func (v *AgentCustomValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	a, ok := newObj.(*contentremixerv1alpha1.Agent)
	if !ok {
		return nil, fmt.Errorf("expected an Agent but got %T", newObj)
	}
	logf.FromContext(ctx).Info("validation for Agent update", "name", a.Name)
	return nil, v.validate(ctx, a)
}

// ValidateDelete implements admission.CustomValidator. Delete is allowed — the
// seeder re-creates any missing default on next boot, so deleting a default is a
// no-op in practice, not an error to block here (the UI guards it as UX).
func (v *AgentCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *AgentCustomValidator) validate(ctx context.Context, a *contentremixerv1alpha1.Agent) error {
	var errs field.ErrorList

	// skillKind ∈ KnownSkillKinds (defense in depth over the CRD enum).
	if !knownSkillKind(a.Spec.SkillKind) {
		errs = append(errs, field.NotSupported(
			field.NewPath("spec", "skillKind"),
			string(a.Spec.SkillKind),
			skillKindStrings(),
		))
	}

	// Cross-object checks need the rest of the namespace.
	var all contentremixerv1alpha1.AgentList
	if err := v.Client.List(ctx, &all, client.InNamespace(a.Namespace)); err != nil {
		return err
	}
	if a.Spec.Default {
		for i := range all.Items {
			other := all.Items[i]
			if other.UID == a.UID || other.Name == a.Name {
				continue // self on update / name-collision impossible on create
			}
			if other.Spec.Default && other.Spec.SkillKind == a.Spec.SkillKind {
				errs = append(errs, field.Invalid(
					field.NewPath("spec", "default"),
					true,
					fmt.Sprintf("another Agent (%s) is already the default for skillKind %q; unset it first", other.Name, a.Spec.SkillKind),
				))
				break
			}
		}
	}

	// modelProfile resolves: a non-empty, non-"default" value must name an
	// existing ModelProfile's litellmName.
	if mp := a.Spec.ModelProfile; mp != "" && mp != "default" {
		var mps contentremixerv1alpha1.ModelProfileList
		if err := v.Client.List(ctx, &mps, client.InNamespace(a.Namespace)); err != nil {
			return err
		}
		found := false
		for i := range mps.Items {
			if mps.Items[i].Spec.LiteLLMName == mp {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "modelProfile"),
				mp,
				"no ModelProfile with this litellmName exists; create it first or use \"default\"",
			))
		}
	}

	if len(errs) > 0 {
		return apierrors.NewInvalid(
			contentremixerv1alpha1.GroupVersion.WithKind("Agent").GroupKind(),
			a.Name, errs)
	}
	return nil
}

func knownSkillKind(k contentremixerv1alpha1.SkillKind) bool {
	for _, known := range contentremixerv1alpha1.KnownSkillKinds {
		if k == known {
			return true
		}
	}
	return false
}

func skillKindStrings() []string {
	out := make([]string, len(contentremixerv1alpha1.KnownSkillKinds))
	for i, k := range contentremixerv1alpha1.KnownSkillKinds {
		out[i] = string(k)
	}
	return out
}

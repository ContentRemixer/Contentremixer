package store

import (
	"context"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
)

// verbValidate records the persona-validate action in the audit log (ISI-7359).
const verbValidate = "validate"

// PersonaConditioning is the §6.3-authorized projection of a DevRelPersona the
// content-test path conditions on (ISI-7359): the real owner (so a StyleProfile
// keyed by owner can be loaded even when an Admin tests another DevRel's
// persona), the deterministic self-description, and the target output medium.
type PersonaConditioning struct {
	OwnerSubject       string
	SelfDescription    string
	PrimaryContentKind string
}

// GetPersonaConditioning resolves a persona's conditioning inputs through the
// SAME §6.3 gate as every other CR read: a DevRel may only read their own
// persona (ErrForbidden otherwise), an Admin any. It returns ErrNotFound when
// the named persona does not exist. This is how the content-test store reaches
// persona spec fields without a second, ungated k8s path.
func (s *Store) GetPersonaConditioning(ctx context.Context, sess auth.Session, name string) (PersonaConditioning, error) {
	obj, err := s.Get(ctx, sess, DevRelPersonaKind, name)
	if err != nil {
		return PersonaConditioning{}, err
	}
	p, ok := obj.(*v1alpha1.DevRelPersona)
	if !ok {
		return PersonaConditioning{}, fmt.Errorf("store: persona %q is not a DevRelPersona", name)
	}
	return PersonaConditioning{
		OwnerSubject:       p.Spec.Owner.Subject,
		SelfDescription:    p.Spec.SelfDescription,
		PrimaryContentKind: string(p.Spec.PrimaryContentKind),
	}, nil
}

// SetPersonaValidated writes the DevRelPersona `Validated` status condition — the
// cheap declarative mirror of the StyleProfile.Validated Postgres flag the Booth
// validate action flips in the same operation (ISI-7356 §4). It evaluates §6.3
// against the persona's current owner (the same gate as a write) and records a
// verbValidate audit entry, then patches ONLY the status condition via the
// status subresource (apimeta.SetStatusCondition merges by type, so the
// ingestion reconciler's other conditions are untouched).
//
// validated=true → Validated=True / reason ProfileValidated; false →
// Validated=False / reason ProfileSynthesized (the owner withdrew acceptance,
// e.g. after re-synthesis).
func (s *Store) SetPersonaValidated(ctx context.Context, sess auth.Session, name string, validated bool) error {
	current, err := s.get(ctx, DevRelPersonaKind, name)
	if err != nil {
		return err
	}
	if !authorize(sess, DevRelPersonaKind.AdminOnly, DevRelPersonaKind.OwnerSubject(current)) {
		s.audit(ctx, sess, verbValidate, DevRelPersonaKind.Kind, name, false)
		return ErrForbidden
	}
	p, ok := current.(*v1alpha1.DevRelPersona)
	if !ok {
		s.audit(ctx, sess, verbValidate, DevRelPersonaKind.Kind, name, false)
		return fmt.Errorf("store: persona %q is not a DevRelPersona", name)
	}

	status := metav1.ConditionTrue
	reason := v1alpha1.PersonaReasonProfileValidated
	msg := "Owner validated the synthesized style profile via Booth."
	if !validated {
		status = metav1.ConditionFalse
		reason = v1alpha1.PersonaReasonProfileSynthesized
		msg = "Style profile awaits owner validation."
	}
	apimeta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.PersonaConditionValidated,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: p.GetGeneration(),
	})
	if err := s.client.Status().Update(ctx, p); err != nil {
		s.audit(ctx, sess, verbValidate, DevRelPersonaKind.Kind, name, false)
		return err
	}
	s.audit(ctx, sess, verbValidate, DevRelPersonaKind.Kind, name, true)
	return nil
}

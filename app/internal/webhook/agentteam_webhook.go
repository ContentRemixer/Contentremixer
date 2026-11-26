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

// AgentTeamCustomValidator enforces the AgentTeam invariants (ISI-7800 §4, the
// fail-closed admission half of the ISI-7804 contract; the engine loader
// re-checks every rule at run time):
//
//   - T1 node ids non-empty and unique;
//   - T2 entry is a declared node id;
//   - T3 every agentRef resolves to an existing Agent in the namespace;
//   - T4 every handoff endpoint is a declared node id;
//   - T5 no self-edge;
//   - T6 the handoff graph is acyclic (Kahn);
//   - T7 every non-entry node is reachable from entry (no orphans);
//   - the writer kind is the linear path: a writer node only as a single-node,
//     no-edge team;
//   - at most one AgentTeam in the namespace may be the default.
type AgentTeamCustomValidator struct {
	Client client.Client
}

var _ admission.CustomValidator = &AgentTeamCustomValidator{}

// SetupWithManager registers the validating webhook with the manager.
func (v *AgentTeamCustomValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&contentremixerv1alpha1.AgentTeam{}).
		WithValidator(v).
		Complete()
}

// +kubebuilder:webhook:path=/validate-contentremixer-io-v1alpha1-agentteam,mutating=false,failurePolicy=fail,sideEffects=None,groups=contentremixer.io,resources=agentteams,verbs=create;update,versions=v1alpha1,name=vagentteam-v1alpha1.contentremixer.io,admissionReviewVersions=v1

// ValidateCreate implements admission.CustomValidator.
func (v *AgentTeamCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	t, ok := obj.(*contentremixerv1alpha1.AgentTeam)
	if !ok {
		return nil, fmt.Errorf("expected an AgentTeam but got %T", obj)
	}
	logf.FromContext(ctx).Info("validation for AgentTeam create", "name", t.Name)
	return nil, v.validate(ctx, t)
}

// ValidateUpdate implements admission.CustomValidator.
func (v *AgentTeamCustomValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	t, ok := newObj.(*contentremixerv1alpha1.AgentTeam)
	if !ok {
		return nil, fmt.Errorf("expected an AgentTeam but got %T", newObj)
	}
	logf.FromContext(ctx).Info("validation for AgentTeam update", "name", t.Name)
	return nil, v.validate(ctx, t)
}

// ValidateDelete implements admission.CustomValidator. Delete is allowed — the
// Booth seeder re-creates a missing default team on next boot, and the engine
// fails closed on an unresolvable default (deleteGuard in the UI is the UX half).
func (v *AgentTeamCustomValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (v *AgentTeamCustomValidator) validate(ctx context.Context, t *contentremixerv1alpha1.AgentTeam) error {
	var errs field.ErrorList
	spec := t.Spec

	// T1 — node ids non-empty + unique.
	ids := make(map[string]int, len(spec.Nodes))
	for i, n := range spec.Nodes {
		p := field.NewPath("spec", "nodes").Index(i).Child("id")
		if n.ID == "" {
			errs = append(errs, field.Invalid(p, n.ID, "node id must be non-empty"))
			continue
		}
		if _, dup := ids[n.ID]; dup {
			errs = append(errs, field.Duplicate(p, n.ID))
			continue
		}
		ids[n.ID] = i
	}

	// T2 — entry is a declared node.
	if _, ok := ids[spec.Entry]; !ok {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "entry"), spec.Entry,
			"entry must be one of the declared node ids"))
	}

	// T4/T5 — edge endpoints declared, no self-edge; build the graph.
	adj := make(map[string][]string, len(spec.Nodes))
	indeg := make(map[string]int, len(spec.Nodes))
	for n := range ids {
		indeg[n] = 0
	}
	for i, e := range spec.Edges {
		p := field.NewPath("spec", "edges").Index(i)
		if _, ok := ids[e.From]; !ok {
			errs = append(errs, field.Invalid(p.Child("from"), e.From, "handoff starts at an undeclared node"))
		}
		if _, ok := ids[e.To]; !ok {
			errs = append(errs, field.Invalid(p.Child("to"), e.To, "handoff ends at an undeclared node"))
		}
		if e.From == e.To {
			errs = append(errs, field.Invalid(p, e.From, "a node cannot hand off to itself"))
		}
		if _, ok := ids[e.From]; ok {
			adj[e.From] = append(adj[e.From], e.To)
			if _, ok := ids[e.To]; ok {
				indeg[e.To]++
			}
		}
	}

	// T6 — acyclic (Kahn over the declared nodes).
	queue := make([]string, 0, len(ids))
	for n := range ids {
		if indeg[n] == 0 {
			queue = append(queue, n)
		}
	}
	seen := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		seen++
		for _, m := range adj[n] {
			if _, ok := ids[m]; !ok {
				continue
			}
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	if len(ids) > 0 && seen != len(ids) {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "edges"), spec.Edges,
			"the handoff graph contains a cycle; hand-offs must flow one way"))
	}

	// T7 — every non-entry node reachable from entry (orphans are hard errors).
	if _, ok := ids[spec.Entry]; ok {
		reached := map[string]bool{spec.Entry: true}
		bfs := []string{spec.Entry}
		for len(bfs) > 0 {
			n := bfs[0]
			bfs = bfs[1:]
			for _, m := range adj[n] {
				if _, ok := ids[m]; ok && !reached[m] {
					reached[m] = true
					bfs = append(bfs, m)
				}
			}
		}
		for n, i := range ids {
			if !reached[n] {
				errs = append(errs, field.Invalid(
					field.NewPath("spec", "nodes").Index(i).Child("id"), n,
					"node is never reached from the entry node"))
			}
		}
	}

	// T3 — every agentRef resolves to an existing Agent.
	var all contentremixerv1alpha1.AgentList
	if err := v.Client.List(ctx, &all, client.InNamespace(t.Namespace)); err != nil {
		return err
	}
	kindByRef := make(map[string]contentremixerv1alpha1.SkillKind, len(all.Items))
	for i := range all.Items {
		kindByRef[all.Items[i].Name] = all.Items[i].Spec.SkillKind
	}
	for i, n := range spec.Nodes {
		if n.AgentRef == "" {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "nodes").Index(i).Child("agentRef"), n.AgentRef,
				"agentRef must be non-empty"))
			continue
		}
		if _, ok := kindByRef[n.AgentRef]; !ok {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "nodes").Index(i).Child("agentRef"), n.AgentRef,
				fmt.Sprintf("no Agent named %q exists in this namespace; create it first", n.AgentRef)))
			continue
		}
		// The writer kind is the linear path, not a DAG skill.
		if kindByRef[n.AgentRef] == contentremixerv1alpha1.SkillKindWriter {
			if len(spec.Nodes) != 1 || len(spec.Edges) != 0 {
				errs = append(errs, field.Invalid(
					field.NewPath("spec", "nodes").Index(i).Child("agentRef"), n.AgentRef,
					"the writer kind is the linear path; a writer team is a single node with no handoffs"))
			}
		}
	}

	// At most one default AgentTeam per namespace.
	if spec.Default {
		var teams contentremixerv1alpha1.AgentTeamList
		if err := v.Client.List(ctx, &teams, client.InNamespace(t.Namespace)); err != nil {
			return err
		}
		for i := range teams.Items {
			other := teams.Items[i]
			if other.UID == t.UID || other.Name == t.Name {
				continue // self on update; name-collision impossible on create
			}
			if other.Spec.Default {
				errs = append(errs, field.Invalid(
					field.NewPath("spec", "default"), true,
					fmt.Sprintf("another AgentTeam (%s) is already the default; unset it first", other.Name)))
				break
			}
		}
	}

	if len(errs) > 0 {
		return apierrors.NewInvalid(
			contentremixerv1alpha1.GroupVersion.WithKind("AgentTeam").GroupKind(),
			t.Name, errs)
	}
	return nil
}

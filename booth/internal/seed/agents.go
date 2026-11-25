// Package seed creates the default Agent and AgentTeam CRs on Booth boot.
//
// Per the ISI-7800 contract §6, the seed reproduces today's hardcoded
// orchestrator.DefaultEngine() DAG as data: six specialist Agents and two teams
// (the architect fan-out "tutorial-default" and the linear-writer
// "content-default"). Seeding is idempotent and create-if-absent — an existing
// CR is never overwritten, so an admin's later edits are preserved across
// restarts (same stance as the DevRel user seed, ISI-7455).
//
// Seeding is best-effort: a failure to create a seed (e.g. the Booth
// ServiceAccount's RBAC for the new kinds not yet applied) is logged and does
// not abort boot — Booth still serves reads. Nothing reads AgentTeam until the
// P3 engine loader (ISI-7804), so a missing seed degrades to today's hardcoded
// path rather than breaking a run.
package seed

import (
	"context"
	"fmt"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// defaultAgents is the six-row seed set from ISI-7800 §6.1. Each is the canonical
// (default=true) Agent for its skill kind. "graphic" is the only media actor.
func defaultAgents(namespace string) []*v1alpha1.Agent {
	mk := func(name, role string, kind v1alpha1.SkillKind, actor string) *v1alpha1.Agent {
		return &v1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: v1alpha1.AgentSpec{
				Role:          role,
				SkillKind:     kind,
				ActorTemplate: actor,
				ModelProfile:  "default",
				Default:       true,
			},
		}
	}
	return []*v1alpha1.Agent{
		mk("architect", "System Architect", v1alpha1.SkillKindArchitect, v1alpha1.ActorTemplateContent),
		mk("research", "Researcher", v1alpha1.SkillKindResearch, v1alpha1.ActorTemplateContent),
		mk("coding", "Coding Specialist", v1alpha1.SkillKindCoding, v1alpha1.ActorTemplateContent),
		mk("observability", "Observability Specialist", v1alpha1.SkillKindObservability, v1alpha1.ActorTemplateContent),
		mk("graphic", "Graphic Designer", v1alpha1.SkillKindGraphic, v1alpha1.ActorTemplateMedia),
		mk("content-writer", "Content Writer", v1alpha1.SkillKindWriter, v1alpha1.ActorTemplateContent),
	}
}

// defaultTeams is the two-team seed set from ISI-7800 §6.2/§6.3.
//   - tutorial-default: the architect fan-out, byte-for-byte the DefaultEngine()
//     DAG (architect is entry; the other four each depend on architect).
//   - content-default: the linear writer as a single-node team.
func defaultTeams(namespace string) []*v1alpha1.AgentTeam {
	return []*v1alpha1.AgentTeam{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "tutorial-default", Namespace: namespace},
			Spec: v1alpha1.AgentTeamSpec{
				Default: true,
				Entry:   "architect",
				Nodes: []v1alpha1.AgentNode{
					{ID: "architect", AgentRef: "architect"},
					{ID: "research", AgentRef: "research"},
					{ID: "coding", AgentRef: "coding"},
					{ID: "observability", AgentRef: "observability"},
					{ID: "graphic", AgentRef: "graphic"},
				},
				Edges: []v1alpha1.AgentEdge{
					{From: "architect", To: "research"},
					{From: "architect", To: "coding"},
					{From: "architect", To: "observability"},
					{From: "architect", To: "graphic"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "content-default", Namespace: namespace},
			Spec: v1alpha1.AgentTeamSpec{
				Default: false,
				Entry:   "writer",
				Nodes:   []v1alpha1.AgentNode{{ID: "writer", AgentRef: "content-writer"}},
			},
		},
	}
}

// Defaults seeds the default Agents and AgentTeams into namespace, create-if-
// absent. It never overwrites an existing CR. Per-object failures are collected
// and returned so the caller can log them; the function always attempts every
// object (one RBAC gap does not skip the rest).
func Defaults(ctx context.Context, c client.Client, namespace string, log *slog.Logger) error {
	var errs []error
	create := func(obj client.Object, kind string) {
		name := obj.GetName()
		existing := obj.DeepCopyObject().(client.Object)
		err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existing)
		switch {
		case err == nil:
			log.Info("seed: present, left as-is", slog.String("kind", kind), slog.String("name", name))
			return
		case !apierrors.IsNotFound(err):
			errs = append(errs, fmt.Errorf("get %s/%s: %w", kind, name, err))
			return
		}
		if err := c.Create(ctx, obj); err != nil {
			if apierrors.IsAlreadyExists(err) { // raced with another replica — fine
				return
			}
			errs = append(errs, fmt.Errorf("create %s/%s: %w", kind, name, err))
			return
		}
		log.Info("seed: created default", slog.String("kind", kind), slog.String("name", name))
	}

	// Agents first — a team's agentRefs point at them (matters once the P3 engine
	// resolves teams; harmless ordering today).
	for _, a := range defaultAgents(namespace) {
		create(a, "Agent")
	}
	for _, t := range defaultTeams(namespace) {
		create(t, "AgentTeam")
	}

	if len(errs) > 0 {
		return fmt.Errorf("seed defaults: %d error(s); first: %w", len(errs), errs[0])
	}
	return nil
}

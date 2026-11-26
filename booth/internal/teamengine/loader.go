// Package teamengine is the ISI-7804 data-driven handoff/orchestration loader:
// it resolves an AgentTeam CR (nodes -> skills, edges -> DependsOn) into a fresh
// orchestrator.Engine, replacing the hardcoded orchestrator.DefaultEngine() at
// the tutorialrun seam. Fail-closed contract (ISI-7778 P3 / ISI-7800 §4):
// unknown agentRef, an undeclared edge endpoint, a cycle, or an orphan node are
// hard errors at admission (the AgentTeam webhook) AND here, at run time. Every
// Orchestrate call re-resolves the team from live CRs, so an admin's edit is
// picked up on the next run with no restart. Any skill failure still fails the
// whole run inside orchestrator.Orchestrate (no partial Mix, no PR).
package teamengine

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/internal/orchestrator"
)

// ErrLinearTeam reports that the resolved team is a single-node writer team
// (the content-default shape). The linear Phase-1 content writer
// (app/internal/pipeline/writer) runs that path, not this DAG engine: the loader
// special-cases it instead of looking the "writer" kind up in the skill-kind
// registry, which deliberately has no DAG entry (see orchestrator/registry.go).
var ErrLinearTeam = errors.New("teamengine: team is a linear-writer team; the linear content path applies, not the DAG engine")

// Loader resolves AgentTeam CRs into engines. It holds only a read client and
// re-reads the CRs on every call, so it is safe for concurrent use.
type Loader struct {
	c  client.Client
	ns string
}

// New builds a Loader reading AgentTeam and Agent CRs from namespace.
func New(c client.Client, namespace string) *Loader {
	return &Loader{c: c, ns: namespace}
}

// Orchestrate implements the tutorialrun.Engine interface: resolve the default
// team live, then run it. This is the live-pickup seam — an admin's CR edit
// applies to the next run without a restart.
func (l *Loader) Orchestrate(ctx context.Context, rc orchestrator.RunContext) (orchestrator.Mix, error) {
	eng, err := l.Engine(ctx)
	if err != nil {
		return orchestrator.Mix{}, err
	}
	return eng.Orchestrate(ctx, rc)
}

// Engine resolves the default AgentTeam and builds a fresh engine from it.
// Fail-closed rules (the run-time mirror of the admission webhook):
//
//   - no AgentTeam CRs at all (or listing is RBAC-denied): the seed never
//     landed, so degrade to today's DefaultEngine() rather than breaking runs
//     (the seed package's documented stance).
//   - teams exist but none is default: hard error — a run fails closed.
//   - more than one default team: hard error.
//   - any structural violation (empty/duplicate node ids, undeclared entry,
//     undeclared edge endpoints, cycle, orphan, unknown agentRef, writer-in-a-
//     DAG, unregistered skill kind): hard error naming the problem.
func (l *Loader) Engine(ctx context.Context) (*orchestrator.Engine, error) {
	var teams v1alpha1.AgentTeamList
	if err := l.c.List(ctx, &teams, client.InNamespace(l.ns)); err != nil {
		if apierrors.IsForbidden(err) {
			// RBAC for the kind not yet applied — same degraded stance as a
			// missing seed: today's hardcoded default keeps runs working.
			return orchestrator.DefaultEngine(), nil
		}
		return nil, fmt.Errorf("teamengine: list agentteams: %w", err)
	}
	if len(teams.Items) == 0 {
		return orchestrator.DefaultEngine(), nil
	}
	var def *v1alpha1.AgentTeam
	for i := range teams.Items {
		if teams.Items[i].Spec.Default {
			if def != nil {
				return nil, fmt.Errorf("teamengine: %q and %q are both default AgentTeams; exactly one team may be the default", def.Name, teams.Items[i].Name)
			}
			def = &teams.Items[i]
		}
	}
	if def == nil {
		return nil, errors.New("teamengine: no default AgentTeam (spec.default is unset on every team); a run fails closed until one team is the default")
	}
	return l.engineFor(ctx, def)
}

// engineFor validates one team and builds its engine.
func (l *Loader) engineFor(ctx context.Context, team *v1alpha1.AgentTeam) (*orchestrator.Engine, error) {
	spec := team.Spec
	name := team.Name

	if len(spec.Nodes) == 0 {
		return nil, fmt.Errorf("teamengine: team %q has no nodes", name)
	}

	// Resolve every node's agent once: an unknown agentRef is a hard error.
	var agents v1alpha1.AgentList
	if err := l.c.List(ctx, &agents, client.InNamespace(l.ns)); err != nil {
		return nil, fmt.Errorf("teamengine: list agents: %w", err)
	}
	agentByRef := make(map[string]v1alpha1.Agent, len(agents.Items))
	for i := range agents.Items {
		agentByRef[agents.Items[i].Name] = agents.Items[i]
	}

	ids := make(map[string]bool, len(spec.Nodes))
	kinds := make([]string, len(spec.Nodes))
	for i, n := range spec.Nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("teamengine: team %q node %d has an empty id", name, i)
		}
		if ids[n.ID] {
			return nil, fmt.Errorf("teamengine: team %q declares node %q more than once", name, n.ID)
		}
		ids[n.ID] = true
		a, ok := agentByRef[n.AgentRef]
		if !ok {
			return nil, fmt.Errorf("teamengine: team %q node %q references unknown agent %q", name, n.ID, n.AgentRef)
		}
		kinds[i] = string(a.Spec.SkillKind)
	}
	if !ids[spec.Entry] {
		return nil, fmt.Errorf("teamengine: team %q entry %q is not a declared node", name, spec.Entry)
	}

	// The writer kind is the linear path, not a DAG skill: either the team is
	// exactly the single-node writer team, or it mixes the writer into a DAG —
	// the latter is a hard error.
	for i, k := range kinds {
		if v1alpha1.SkillKind(k) != v1alpha1.SkillKindWriter {
			continue
		}
		if len(spec.Nodes) != 1 || len(spec.Edges) != 0 {
			return nil, fmt.Errorf("teamengine: team %q mixes the linear writer (node %q) with DAG skills; a writer team is a single node with no handoffs", name, spec.Nodes[i].ID)
		}
		return nil, fmt.Errorf("%w (team %q)", ErrLinearTeam, name)
	}

	// Edges: both endpoints declared. Build adjacency + in-degrees for the
	// cycle (Kahn) and reachability (BFS from entry) checks.
	adj := make(map[string][]string, len(spec.Nodes))
	indeg := make(map[string]int, len(spec.Nodes))
	for _, n := range spec.Nodes {
		indeg[n.ID] = 0
	}
	for _, e := range spec.Edges {
		if !ids[e.From] {
			return nil, fmt.Errorf("teamengine: team %q handoff %q -> %q starts at an undeclared node", name, e.From, e.To)
		}
		if !ids[e.To] {
			return nil, fmt.Errorf("teamengine: team %q handoff %q -> %q ends at an undeclared node", name, e.From, e.To)
		}
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
	}
	queue := make([]string, 0, len(spec.Nodes))
	for _, n := range spec.Nodes {
		if indeg[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}
	seen := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		seen++
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	if seen != len(spec.Nodes) {
		return nil, fmt.Errorf("teamengine: team %q handoffs contain a cycle", name)
	}
	reached := map[string]bool{spec.Entry: true}
	bfs := []string{spec.Entry}
	for len(bfs) > 0 {
		n := bfs[0]
		bfs = bfs[1:]
		for _, m := range adj[n] {
			if !reached[m] {
				reached[m] = true
				bfs = append(bfs, m)
			}
		}
	}
	for _, n := range spec.Nodes {
		if !reached[n.ID] {
			return nil, fmt.Errorf("teamengine: team %q node %q is never reached from entry %q", name, n.ID, spec.Entry)
		}
	}

	// Resolve each node to a FRESH skill from the skill-kind registry (an
	// unregistered kind is a hard error) and wrap it so Name() is the node id
	// and DependsOn() is the team's incoming edges: the engine's own topo-sort
	// then re-checks the team graph on every run (unknown dep / cycle stay hard
	// errors there — fail-closed defense in depth).
	deps := make(map[string][]string, len(spec.Nodes))
	for _, e := range spec.Edges {
		deps[e.To] = append(deps[e.To], e.From)
	}
	skills := make([]orchestrator.Skill, 0, len(spec.Nodes))
	for i, n := range spec.Nodes {
		inner, ok := orchestrator.SkillForKind(kinds[i])
		if !ok {
			return nil, fmt.Errorf("teamengine: team %q node %q runs agent %q with skill kind %q, which has no registered DAG implementation", name, n.ID, n.AgentRef, kinds[i])
		}
		depList := deps[n.ID]
		if depList == nil {
			depList = []string{}
		}
		skills = append(skills, teamSkill{id: n.ID, deps: depList, inner: inner})
	}
	return orchestrator.NewEngine(skills...), nil
}

// teamSkill adapts a registered skill impl to one team node: the node id
// becomes the skill name (the DAG node id) and the team's incoming edges become
// its DependsOn. Run delegates to the fresh impl from the skill-kind registry.
type teamSkill struct {
	id    string
	deps  []string
	inner orchestrator.Skill
}

func (s teamSkill) Name() string { return s.id }

func (s teamSkill) DependsOn() []string { return s.deps }

func (s teamSkill) Run(ctx context.Context, rc orchestrator.RunContext, in orchestrator.SkillInput) (orchestrator.SkillOutput, error) {
	return s.inner.Run(ctx, rc, in)
}

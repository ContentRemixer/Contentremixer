// Package orchestrator is the P3-4 multi-skill tutorial engine (ISI-7152). It
// turns one owner-tagged run request into a tutorial draft by fanning the run
// out across five specialist skills — architect, research, coding,
// observability, graphic — and recomposing their fragments into a single Mix,
// the same shape the linear Phase-1 writer emits (design §4). The Mix then
// rejoins the frozen tail (Checker → PR assembler, §4/§5) outside this package.
//
// Design invariants this package preserves:
//
//   - An orchestrated tutorial run is ONE run that carries RunContext.Owner
//     (§4). This package never consults identity or authz — ownership is
//     stamped and enforced by the booth/internal/tutorialrun gate (§6.3), which
//     builds the RunContext. The engine is a pure, side-effect-free transform
//     so it is table-testable with fixtures and introduces no new authz path.
//   - Fail-closed: if any skill errors the whole run fails and NO Mix is
//     produced (mirrors the §4 "Check fails ⇒ no PR" stance). A half-built
//     tutorial is never emitted.
//
// The default skills are deterministic scaffold generators: they compose a
// coherent tutorial from the RunContext without calling a model, so the
// orchestration contract (the DAG, the anchor/fragment model, the Mix
// assembly) is implemented and tested now. Real model-backed skills plug in
// behind the same Skill interface later without touching the engine or the gate.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DefaultModel is the LiteLLM alias recorded for scaffold skills; it matches the
// design §5 Sample-clearance example ("Model: default"). Real skills override
// per-call via SkillOutput.ModelUsed.
const DefaultModel = "default"

// Anchor kinds — the architect tags each outline node with the specialist that
// owns it, so every other skill is a pure "fill anchors of my kind" pass.
const (
	KindProse         = "prose"
	KindCode          = "code"
	KindObservability = "observability"
	KindVisual        = "visual"
)

// Well-known skill names (also the DAG node ids).
const (
	SkillArchitect     = "architect"
	SkillResearch      = "research"
	SkillCoding        = "coding"
	SkillObservability = "observability"
	SkillGraphic       = "graphic"
)

// RunContext is the owner-tagged carrier for one orchestration run, echoing the
// design §4 RunContext. Owner is the §6.3 tenancy key the gate stamps from the
// session; the engine only reads it (it is threaded into every skill call so a
// model-backed skill can scope its own calls, and so audit can attribute work).
type RunContext struct {
	Owner     string // resource.owner.subject (§6.2) — set by the gate, never the payload
	Topic     string
	Audience  string
	Notes     string
	SourceURL string
}

// Anchor is one node in the tutorial outline the architect produces.
type Anchor struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"` // one of the Kind* constants
}

// Outline is the architect's plan: the ordered spine every other skill writes
// fragments against.
type Outline struct {
	Anchors []Anchor `json:"anchors"`
}

// Fragment is one unit of generated content contributed to an anchor. Order
// disambiguates multiple fragments on the same anchor (stable sort).
type Fragment struct {
	Anchor string `json:"anchor"`
	Kind   string `json:"kind"`
	Body   string `json:"body"`
	Order  int    `json:"order"`
}

// SkillInput is what a skill receives: the RunContext, the current Outline (the
// architect's, empty until it has run), and the outputs of already-run skills.
type SkillInput struct {
	RC       RunContext
	Outline  Outline
	Upstream map[string]SkillOutput
}

// SkillOutput is what a skill returns. Outline is non-nil ONLY for the planning
// skill (architect); the engine adopts it as the run outline for downstream
// skills. Meta is merged into Mix.Meta (e.g. a code manifest, image refs).
type SkillOutput struct {
	Skill     string
	Fragments []Fragment
	Outline   *Outline
	Meta      map[string]any
	ModelUsed string
}

// Skill is one specialist stage. Implementations MUST be side-effect-free
// except I/O they own (mirrors the §4 pipeline-stage discipline).
type Skill interface {
	Name() string
	DependsOn() []string
	Run(ctx context.Context, rc RunContext, in SkillInput) (SkillOutput, error)
}

// Mix is the recomposed tutorial draft — the same hand-off shape the linear
// §4 Writer emits, so the Checker→PR tail is unchanged. ModelUsed is the single
// mandatory Sample-clearance model (§5); Models is the per-skill aggregate
// mirrored into recycled/<slug>/meta.yaml.
type Mix struct {
	Title     string            `json:"title"`
	Markdown  string            `json:"markdown"`
	Meta      map[string]any    `json:"meta"`
	ModelUsed string            `json:"modelUsed"`
	Models    map[string]string `json:"models"`
}

// Engine binds a fixed skill set so callers hold one Orchestrate entry point.
type Engine struct {
	skills []Skill
}

// NewEngine builds an engine over an explicit skill set (tests supply fakes).
func NewEngine(skills ...Skill) *Engine { return &Engine{skills: skills} }

// DefaultEngine wires the five P3-4 scaffold skills in DAG order.
func DefaultEngine() *Engine {
	return NewEngine(
		NewArchitectSkill(),
		NewResearchSkill(),
		NewCodingSkill(),
		NewObservabilitySkill(),
		NewGraphicSkill(),
	)
}

// Orchestrate runs this engine's skills for rc.
func (e *Engine) Orchestrate(ctx context.Context, rc RunContext) (Mix, error) {
	return Orchestrate(ctx, rc, e.skills)
}

// Orchestrate topologically orders the skills by their declared dependencies,
// runs them in order (adopting the architect's outline for downstream skills),
// and assembles the fragments into a Mix. Any skill error fails the whole run
// (fail-closed) — no partial Mix is returned.
func Orchestrate(ctx context.Context, rc RunContext, skills []Skill) (Mix, error) {
	order, err := topoSort(skills)
	if err != nil {
		return Mix{}, err
	}
	outputs := make(map[string]SkillOutput, len(order))
	var outline Outline
	for _, sk := range order {
		out, err := sk.Run(ctx, rc, SkillInput{RC: rc, Outline: outline, Upstream: outputs})
		if err != nil {
			return Mix{}, fmt.Errorf("skill %q failed: %w", sk.Name(), err)
		}
		if out.Skill == "" {
			out.Skill = sk.Name()
		}
		if out.Outline != nil {
			outline = *out.Outline
		}
		outputs[sk.Name()] = out
	}
	if len(outline.Anchors) == 0 {
		return Mix{}, errors.New("orchestrator: no outline produced (missing architect skill)")
	}
	return assemble(rc, outline, outputs), nil
}

// topoSort returns the skills in a dependency-respecting order using Kahn's
// algorithm. Input order is preserved among ready nodes so the result is
// deterministic. Unknown dependencies and cycles are hard errors.
func topoSort(skills []Skill) ([]Skill, error) {
	byName := make(map[string]Skill, len(skills))
	indeg := make(map[string]int, len(skills))
	for _, s := range skills {
		if _, dup := byName[s.Name()]; dup {
			return nil, fmt.Errorf("orchestrator: duplicate skill %q", s.Name())
		}
		byName[s.Name()] = s
		indeg[s.Name()] = 0
	}
	adj := make(map[string][]string, len(skills))
	for _, s := range skills {
		for _, dep := range s.DependsOn() {
			if _, ok := byName[dep]; !ok {
				return nil, fmt.Errorf("orchestrator: skill %q depends on unknown skill %q", s.Name(), dep)
			}
			adj[dep] = append(adj[dep], s.Name())
			indeg[s.Name()]++
		}
	}
	var queue []string
	for _, s := range skills {
		if indeg[s.Name()] == 0 {
			queue = append(queue, s.Name())
		}
	}
	order := make([]Skill, 0, len(skills))
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, byName[n])
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	if len(order) != len(skills) {
		return nil, errors.New("orchestrator: skill dependency cycle detected")
	}
	return order, nil
}

// assemble interleaves every skill's fragments into the outline spine and
// records the aggregated clearance models. The primary (mandatory) clearance
// model is the research/writer model when present, else any skill's model.
func assemble(rc RunContext, outline Outline, outputs map[string]SkillOutput) Mix {
	byAnchor := make(map[string][]Fragment)
	models := make(map[string]string)
	meta := make(map[string]any)
	for name, out := range outputs {
		if out.ModelUsed != "" {
			models[name] = out.ModelUsed
		}
		for k, v := range out.Meta {
			meta[k] = v
		}
		for _, f := range out.Fragments {
			byAnchor[f.Anchor] = append(byAnchor[f.Anchor], f)
		}
	}
	title := rc.Topic
	if title == "" {
		title = "Tutorial"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	for _, a := range outline.Anchors {
		fmt.Fprintf(&b, "## %s\n\n", a.Title)
		frags := byAnchor[a.ID]
		sort.SliceStable(frags, func(i, j int) bool { return frags[i].Order < frags[j].Order })
		for _, f := range frags {
			b.WriteString(f.Body)
			b.WriteString("\n\n")
		}
	}
	primary := models[SkillResearch]
	if primary == "" {
		for _, m := range models {
			primary = m
			break
		}
	}
	meta["owner"] = rc.Owner // clearance/tenancy trace mirrored into meta.yaml
	return Mix{
		Title:     title,
		Markdown:  strings.TrimRight(b.String(), "\n") + "\n",
		Meta:      meta,
		ModelUsed: primary,
		Models:    models,
	}
}

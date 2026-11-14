// Package dispatch is the runtime seam for the recycle pipeline (ISI-7393,
// parent ISI-7389 ADR-1/§4). One WorkOrder goes in; the Dispatcher selects a
// Strategy by the route's actor template and runs it on a concrete runtime
// (inline | k8s-Job | substrate-actor). The operator holds only this
// boundary — it never knows or branches on which runtime executed the order
// (ISI-7387 lesson: port the seam, not the machinery). Swapping a media Job
// for a substrate actor later is a Strategy change behind this interface with
// zero control-plane diff.
package dispatch

import (
	"context"
	"fmt"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// Runtime names the execution backend a Strategy ran a WorkOrder on. It is
// recorded for observability; the operator never branches on it.
type Runtime string

const (
	// RuntimeInline is the in-process run worker (content template, Phase-1).
	RuntimeInline Runtime = "inline"
	// RuntimeK8sJob is an ephemeral batch Job (media template, P0).
	RuntimeK8sJob Runtime = "k8s-Job"
	// RuntimeSubstrate is a snapshot/resume substrate actor (media, P1).
	RuntimeSubstrate Runtime = "substrate-actor"
)

// Inputs is everything a strategy needs to execute one order, resolved and
// verified at enqueue time (route formats, the per-repo PAT, PR target, and
// the source path / notes parsed from the issue form).
type Inputs struct {
	// SourcePath is the content/ path or external URL from the issue form.
	SourcePath string
	// Notes is the free-text angle/audience guidance from the issue form.
	Notes string
	// InputFormat is the resolved analyzer input (may be "auto").
	InputFormat string
	// OutputFormat is the resolved writer/media style.
	OutputFormat string
	// Token is the per-repo PAT (already resolved from the Secret).
	Token string
	// PRTarget mirrors the ReferenceRepo spec.prTarget.
	PRTarget contentremixerv1alpha1.PRTargetSpec
}

// MemoryRef points a strategy at the shared external style layer (ADR-4:
// pgvector exemplars + style profiles + DevRelPersona). It is a reference,
// never the memory itself — actors read it live and are never snapshotted
// with it. Empty => platform-owned, no persona (the Phase-1 style-only path).
type MemoryRef struct {
	// Owner is the tenancy subject (empty => platform-owned).
	Owner contentremixerv1alpha1.OwnerRef
	// Persona is the DevRelPersona scoping voice conditioning, if any.
	Persona string
}

// WorkOrder is the single runtime-agnostic unit the Dispatcher routes
// (ISI-7389 §4 seam): { repo, issueNumber, inputs, skill, actorTemplate,
// memoryRef }.
type WorkOrder struct {
	// Repo is the GitHub "owner/name".
	Repo string
	// IssueNumber is the originating recycle issue.
	IssueNumber int
	// Inputs is the pipeline payload (source, formats, PAT, PR target).
	Inputs Inputs
	// Skill is the resolved skill pack (route.EffectiveSkill()).
	Skill string
	// ActorTemplate is the resolved runtime template the Dispatcher selects a
	// Strategy by (route.EffectiveActorTemplate()): "content" | "media".
	ActorTemplate string
	// MemoryRef points at the shared external style layer (ADR-4).
	MemoryRef MemoryRef
}

// Strategy executes one WorkOrder on a concrete runtime. ContentStrategy
// (inline) and MediaStrategy (k8s-Job) implement it; a substrate strategy
// slots in later behind the same interface.
type Strategy interface {
	// Runtime reports the backend this strategy runs on (for logging/metrics).
	Runtime() Runtime
	// Run executes the order, returning any run error.
	Run(ctx context.Context, wo WorkOrder) error
}

// Dispatcher selects a Strategy by actor template and runs the WorkOrder. It
// is the whole boundary the operator sees.
type Dispatcher struct {
	byTemplate map[string]Strategy
}

// New builds a Dispatcher from an actorTemplate => Strategy map. Templates
// not present are rejected at dispatch time with a clear error.
func New(strategies map[string]Strategy) *Dispatcher {
	m := make(map[string]Strategy, len(strategies))
	for tmpl, s := range strategies {
		m[tmpl] = s
	}
	return &Dispatcher{byTemplate: m}
}

// Dispatch routes wo to the strategy registered for its actor template and
// runs it. It returns the runtime the order ran on (for the caller to log)
// and any run error. An unregistered template is a config/wiring error, not a
// run failure — returned verbatim so the operator surfaces it.
func (d *Dispatcher) Dispatch(ctx context.Context, wo WorkOrder) (Runtime, error) {
	s, ok := d.byTemplate[wo.ActorTemplate]
	if !ok {
		return "", fmt.Errorf("dispatch: no strategy registered for actor template %q (skill %q, %s#%d)",
			wo.ActorTemplate, wo.Skill, wo.Repo, wo.IssueNumber)
	}
	return s.Runtime(), s.Run(ctx, wo)
}

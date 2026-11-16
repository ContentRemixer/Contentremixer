// Skill implementations for the P3-4 tutorial engine. Each is a deterministic
// scaffold generator: the architect lays out the outline spine; the other four
// are pure "fill every anchor of my kind" passes that depend on the architect.
// They compose a coherent tutorial from the RunContext without a model call, so
// the orchestration contract is real and testable today; model-backed skills
// replace these behind the same Skill interface without changing the engine.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
)

// --- architect ---------------------------------------------------------------

// ArchitectSkill turns the RunContext into the tutorial outline the whole run
// is built on. It has no dependencies and runs first.
type ArchitectSkill struct{}

func NewArchitectSkill() *ArchitectSkill { return &ArchitectSkill{} }

func (ArchitectSkill) Name() string        { return SkillArchitect }
func (ArchitectSkill) DependsOn() []string { return nil }

func (ArchitectSkill) Run(_ context.Context, rc RunContext, _ SkillInput) (SkillOutput, error) {
	topic := rc.Topic
	if topic == "" {
		return SkillOutput{}, fmt.Errorf("architect: topic is required")
	}
	outline := Outline{Anchors: []Anchor{
		{ID: "intro", Title: "Introduction", Kind: KindProse},
		{ID: "prerequisites", Title: "Prerequisites", Kind: KindProse},
		{ID: "setup", Title: "Setup", Kind: KindCode},
		{ID: "instrument", Title: "Instrument the Service", Kind: KindObservability},
		{ID: "architecture", Title: "Architecture Diagram", Kind: KindVisual},
		{ID: "walkthrough", Title: "Walkthrough", Kind: KindCode},
		{ID: "verify", Title: "Verify Telemetry", Kind: KindObservability},
		{ID: "wrapup", Title: "Wrap-up", Kind: KindProse},
	}}
	return SkillOutput{
		Skill:     SkillArchitect,
		Outline:   &outline,
		ModelUsed: DefaultModel,
		Meta:      map[string]any{"outlineAnchors": len(outline.Anchors)},
	}, nil
}

// fillByKind is the shared "write one fragment per matching anchor" body used by
// the four non-planning skills.
func fillByKind(skill, kind string, outline Outline, body func(a Anchor) string) SkillOutput {
	out := SkillOutput{Skill: skill, ModelUsed: DefaultModel}
	for _, a := range outline.Anchors {
		if a.Kind != kind {
			continue
		}
		out.Fragments = append(out.Fragments, Fragment{
			Anchor: a.ID,
			Kind:   kind,
			Body:   body(a),
		})
	}
	return out
}

// --- research ----------------------------------------------------------------

// ResearchSkill fills the prose anchors with audience-framed narrative.
type ResearchSkill struct{}

func NewResearchSkill() *ResearchSkill { return &ResearchSkill{} }

func (ResearchSkill) Name() string        { return SkillResearch }
func (ResearchSkill) DependsOn() []string { return []string{SkillArchitect} }

func (ResearchSkill) Run(_ context.Context, rc RunContext, in SkillInput) (SkillOutput, error) {
	audience := rc.Audience
	if audience == "" {
		audience = "developers"
	}
	return fillByKind(SkillResearch, KindProse, in.Outline, func(a Anchor) string {
		switch a.ID {
		case "intro":
			return fmt.Sprintf("This tutorial shows %s how to %s. We keep each step runnable and explain the why, not just the how.", audience, rc.Topic)
		case "prerequisites":
			return "Before you start you need: a working toolchain, access to the target environment, and the sample project checked out."
		default:
			return fmt.Sprintf("You now have a working %s. Next, extend it to your own services and wire the signals into your existing dashboards.", rc.Topic)
		}
	}), nil
}

// --- coding ------------------------------------------------------------------

// CodingSkill fills code anchors with runnable snippets and records a code
// manifest in Meta so the §5 PR assembler can drop sources into recycled/.
type CodingSkill struct{}

func NewCodingSkill() *CodingSkill { return &CodingSkill{} }

func (CodingSkill) Name() string        { return SkillCoding }
func (CodingSkill) DependsOn() []string { return []string{SkillArchitect} }

func (CodingSkill) Run(_ context.Context, rc RunContext, in SkillInput) (SkillOutput, error) {
	var files []string
	out := fillByKind(SkillCoding, KindCode, in.Outline, func(a Anchor) string {
		file := a.ID + ".sh"
		files = append(files, file)
		switch a.ID {
		case "setup":
			return "```bash\n# setup\nmake bootstrap\n```"
		default:
			return fmt.Sprintf("```bash\n# %s\nmake run\n```", a.Title)
		}
	})
	out.Meta = map[string]any{"codeManifest": files}
	return out, nil
}

// --- observability -----------------------------------------------------------

// ObservabilitySkill fills observability anchors with instrumentation and
// verification steps — the house specialty (OTel / k8s telemetry).
type ObservabilitySkill struct{}

func NewObservabilitySkill() *ObservabilitySkill { return &ObservabilitySkill{} }

func (ObservabilitySkill) Name() string        { return SkillObservability }
func (ObservabilitySkill) DependsOn() []string { return []string{SkillArchitect} }

func (ObservabilitySkill) Run(_ context.Context, _ RunContext, in SkillInput) (SkillOutput, error) {
	return fillByKind(SkillObservability, KindObservability, in.Outline, func(a Anchor) string {
		switch a.ID {
		case "instrument":
			return "Add the OpenTelemetry SDK and export OTLP to your collector. Set `OTEL_EXPORTER_OTLP_ENDPOINT` and a meaningful `OTEL_SERVICE_NAME`."
		default:
			return "Confirm traces and metrics arrive: check the collector's debug exporter, then your backend for the service's spans."
		}
	}), nil
}

// --- graphic -----------------------------------------------------------------

// GraphicSkill fills visual anchors with image references (bytes are produced
// out-of-band by the Graphic Designer work queue; fragments carry refs only).
type GraphicSkill struct{}

func NewGraphicSkill() *GraphicSkill { return &GraphicSkill{} }

func (GraphicSkill) Name() string        { return SkillGraphic }
func (GraphicSkill) DependsOn() []string { return []string{SkillArchitect} }

func (GraphicSkill) Run(_ context.Context, rc RunContext, in SkillInput) (SkillOutput, error) {
	var refs []string
	out := fillByKind(SkillGraphic, KindVisual, in.Outline, func(a Anchor) string {
		ref := "images/" + a.ID + ".svg"
		refs = append(refs, ref)
		alt := strings.ToLower(a.Title) + " for " + rc.Topic
		return fmt.Sprintf("![%s](%s)", alt, ref)
	})
	out.Meta = map[string]any{"imageRefs": refs}
	return out, nil
}

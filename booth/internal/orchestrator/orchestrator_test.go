package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDefaultEngineProducesTutorial(t *testing.T) {
	rc := RunContext{Owner: "alice", Topic: "trace a Go service", Audience: "SREs"}
	mix, err := DefaultEngine().Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if mix.Title != "trace a Go service" {
		t.Errorf("title = %q", mix.Title)
	}
	// Every outline section heading must be present, in order.
	for _, want := range []string{"# trace a Go service", "## Introduction", "## Setup", "## Instrument the Service", "## Architecture Diagram", "## Verify Telemetry", "## Wrap-up"} {
		if !strings.Contains(mix.Markdown, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	// Each specialist contributed its kind of fragment.
	for _, want := range []string{"```bash", "OpenTelemetry", "![", "SREs"} {
		if !strings.Contains(mix.Markdown, want) {
			t.Errorf("markdown missing specialist output %q", want)
		}
	}
	// Clearance: mandatory single model plus the per-skill aggregate (§5).
	if mix.ModelUsed != DefaultModel {
		t.Errorf("ModelUsed = %q, want %q", mix.ModelUsed, DefaultModel)
	}
	for _, s := range []string{SkillArchitect, SkillResearch, SkillCoding, SkillObservability, SkillGraphic} {
		if mix.Models[s] != DefaultModel {
			t.Errorf("Models[%q] = %q, want %q", s, mix.Models[s], DefaultModel)
		}
	}
	// Owner is carried into meta for clearance/tenancy trace.
	if mix.Meta["owner"] != "alice" {
		t.Errorf("meta owner = %v, want alice", mix.Meta["owner"])
	}
	// Code manifest + image refs surfaced for the §5 assembler.
	if _, ok := mix.Meta["codeManifest"]; !ok {
		t.Error("meta missing codeManifest")
	}
	if _, ok := mix.Meta["imageRefs"]; !ok {
		t.Error("meta missing imageRefs")
	}
}

func TestOrchestrateIsDeterministic(t *testing.T) {
	rc := RunContext{Owner: "bob", Topic: "x", Audience: "devs"}
	a, err := DefaultEngine().Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DefaultEngine().Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if a.Markdown != b.Markdown {
		t.Error("orchestration is not deterministic")
	}
}

// failSkill errors on Run to exercise the fail-closed path.
type failSkill struct{ deps []string }

func (failSkill) Name() string          { return "boom" }
func (f failSkill) DependsOn() []string { return f.deps }
func (failSkill) Run(context.Context, RunContext, SkillInput) (SkillOutput, error) {
	return SkillOutput{}, errors.New("kaboom")
}

func TestFailClosedOnSkillError(t *testing.T) {
	skills := []Skill{NewArchitectSkill(), failSkill{deps: []string{SkillArchitect}}}
	_, err := Orchestrate(context.Background(), RunContext{Topic: "t"}, skills)
	if err == nil {
		t.Fatal("expected error when a skill fails")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to name the failing skill", err)
	}
}

func TestMissingArchitectIsError(t *testing.T) {
	_, err := Orchestrate(context.Background(), RunContext{Topic: "t"}, []Skill{NewResearchSkill()})
	// research depends on architect, which is absent → unknown-dependency error.
	if err == nil {
		t.Fatal("expected error for missing architect dependency")
	}
}

func TestCycleDetected(t *testing.T) {
	_, err := topoSort([]Skill{cyc{"a", []string{"b"}}, cyc{"b", []string{"a"}}})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

type cyc struct {
	name string
	deps []string
}

func (c cyc) Name() string        { return c.name }
func (c cyc) DependsOn() []string { return c.deps }
func (cyc) Run(context.Context, RunContext, SkillInput) (SkillOutput, error) {
	return SkillOutput{}, nil
}

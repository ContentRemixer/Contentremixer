package webhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// teamValidator builds the validator over a fake cluster seeded with the
// default specialists (the P1 seed set) plus any extra objects.
func teamValidator(t *testing.T, existing ...client.Object) *AgentTeamCustomValidator {
	t.Helper()
	objs := []client.Object{
		agent("architect", contentremixerv1alpha1.SkillKindArchitect, true),
		agent("research", contentremixerv1alpha1.SkillKindResearch, true),
		agent("coding", contentremixerv1alpha1.SkillKindCoding, true),
		agent("observability", contentremixerv1alpha1.SkillKindObservability, true),
		agent("graphic", contentremixerv1alpha1.SkillKindGraphic, true),
		agent("content-writer", contentremixerv1alpha1.SkillKindWriter, true),
	}
	objs = append(objs, existing...)
	cl := fake.NewClientBuilder().WithScheme(agentScheme(t)).WithObjects(objs...).Build()
	return &AgentTeamCustomValidator{Client: cl}
}

// team is the seeded tutorial-default shape: entry architect, four fan-out
// handoffs — the data form of DefaultEngine().
func team(name string, def bool) *contentremixerv1alpha1.AgentTeam {
	return &contentremixerv1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "contentremixer"},
		Spec: contentremixerv1alpha1.AgentTeamSpec{
			Default: def,
			Entry:   "architect",
			Nodes: []contentremixerv1alpha1.AgentNode{
				{ID: "architect", AgentRef: "architect"},
				{ID: "research", AgentRef: "research"},
				{ID: "coding", AgentRef: "coding"},
				{ID: "observability", AgentRef: "observability"},
				{ID: "graphic", AgentRef: "graphic"},
			},
			Edges: []contentremixerv1alpha1.AgentEdge{
				{From: "architect", To: "research"},
				{From: "architect", To: "coding"},
				{From: "architect", To: "observability"},
				{From: "architect", To: "graphic"},
			},
		},
	}
}

func linearTeam() *contentremixerv1alpha1.AgentTeam {
	return &contentremixerv1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "content-default", Namespace: "contentremixer"},
		Spec: contentremixerv1alpha1.AgentTeamSpec{
			Entry: "writer",
			Nodes: []contentremixerv1alpha1.AgentNode{{ID: "writer", AgentRef: "content-writer"}},
		},
	}
}

func TestAgentTeamValidate_ValidFanOutAndLinear(t *testing.T) {
	v := teamValidator(t)
	if _, err := v.ValidateCreate(context.Background(), team("tutorial-default", true)); err != nil {
		t.Fatalf("the seeded fan-out team must validate: %v", err)
	}
	if _, err := v.ValidateCreate(context.Background(), linearTeam()); err != nil {
		t.Fatalf("the seeded linear-writer team must validate: %v", err)
	}
}

func TestAgentTeamValidate_GraphFailClosed(t *testing.T) {
	cycle := team("cycle-team", false)
	cycle.Spec.Edges = append(cycle.Spec.Edges, contentremixerv1alpha1.AgentEdge{From: "coding", To: "architect"})

	orphan := team("orphan-team", false)
	orphan.Spec.Edges = orphan.Spec.Edges[:3]

	self := team("self-team", false)
	self.Spec.Edges = append(self.Spec.Edges, contentremixerv1alpha1.AgentEdge{From: "coding", To: "coding"})

	badEntry := team("entry-team", false)
	badEntry.Spec.Entry = "ghost"

	dup := team("dup-team", false)
	dup.Spec.Nodes = append(dup.Spec.Nodes, contentremixerv1alpha1.AgentNode{ID: "research", AgentRef: "research"})

	badEdge := team("edge-team", false)
	badEdge.Spec.Edges = append(badEdge.Spec.Edges, contentremixerv1alpha1.AgentEdge{From: "architect", To: "ghost"})

	cases := []struct {
		name string
		team *contentremixerv1alpha1.AgentTeam
		want string
	}{
		{"cycle", cycle, "cycle"},
		{"orphan", orphan, "never reached from the entry node"},
		{"self-edge", self, "cannot hand off to itself"},
		{"undeclared entry", badEntry, "entry must be one of the declared node ids"},
		{"duplicate node id", dup, "Duplicate"},
		{"undeclared edge endpoint", badEdge, "undeclared node"},
	}
	for _, tc := range cases {
		v := teamValidator(t)
		_, err := v.ValidateCreate(context.Background(), tc.team)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}

func TestAgentTeamValidate_AgentRefAndWriterRules(t *testing.T) {
	unknown := team("unknown-team", false)
	unknown.Spec.Nodes[4].AgentRef = "ghost-agent"

	mixed := team("mixed-team", false)
	mixed.Spec.Nodes = append(mixed.Spec.Nodes, contentremixerv1alpha1.AgentNode{ID: "writer", AgentRef: "content-writer"})

	v := teamValidator(t)
	if _, err := v.ValidateCreate(context.Background(), unknown); err == nil || !strings.Contains(err.Error(), "no Agent named") {
		t.Fatalf("unknown agentRef must fail closed naming the agent, got %v", err)
	}
	if _, err := v.ValidateCreate(context.Background(), mixed); err == nil || !strings.Contains(err.Error(), "linear path") {
		t.Fatalf("a writer mixed into a DAG must fail closed, got %v", err)
	}
}

func TestAgentTeamValidate_AtMostOneDefault(t *testing.T) {
	existing := team("tutorial-default", true)
	existing.UID = "tutorial-uid"
	v := teamValidator(t, existing)

	second := team("other-team", true)
	if _, err := v.ValidateCreate(context.Background(), second); err == nil || !strings.Contains(err.Error(), "already the default") {
		t.Fatalf("a second default team must fail, got %v", err)
	}
	extra := team("other-team", false)
	if _, err := v.ValidateCreate(context.Background(), extra); err != nil {
		t.Fatalf("a non-default extra team is fine: %v", err)
	}
}

func TestAgentTeamValidate_UpdateRevalidates(t *testing.T) {
	v := teamValidator(t)
	broken := team("tutorial-default", false)
	broken.Spec.Edges = append(broken.Spec.Edges, contentremixerv1alpha1.AgentEdge{From: "research", To: "architect"})
	if _, err := v.ValidateUpdate(context.Background(), nil, broken); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("an update introducing a cycle must fail, got %v", err)
	}
}

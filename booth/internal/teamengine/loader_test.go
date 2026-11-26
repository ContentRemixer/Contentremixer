package teamengine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/internal/orchestrator"
)

const ns = "contentremixer"

func teamScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func mkAgent(name string, kind v1alpha1.SkillKind) *v1alpha1.Agent {
	return &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.AgentSpec{Role: "Role " + name, SkillKind: kind, ActorTemplate: v1alpha1.ActorTemplateContent},
	}
}

// seedAgents mirrors booth/internal/seed.defaultAgents.
func seedAgents() []client.Object {
	return []client.Object{
		mkAgent("architect", v1alpha1.SkillKindArchitect),
		mkAgent("research", v1alpha1.SkillKindResearch),
		mkAgent("coding", v1alpha1.SkillKindCoding),
		mkAgent("observability", v1alpha1.SkillKindObservability),
		mkAgent("graphic", v1alpha1.SkillKindGraphic),
		mkAgent("content-writer", v1alpha1.SkillKindWriter),
	}
}

// tutorialTeam mirrors the seeded tutorial-default: byte-for-byte the
// DefaultEngine() DAG.
func tutorialTeam() *v1alpha1.AgentTeam {
	return &v1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "tutorial-default", Namespace: ns},
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
	}
}

func contentTeam() *v1alpha1.AgentTeam {
	return &v1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{Name: "content-default", Namespace: ns},
		Spec: v1alpha1.AgentTeamSpec{
			Entry: "writer",
			Nodes: []v1alpha1.AgentNode{{ID: "writer", AgentRef: "content-writer"}},
		},
	}
}

func loader(t *testing.T, objs ...client.Object) *Loader {
	t.Helper()
	cl := fake.NewClientBuilder().WithScheme(teamScheme(t)).WithObjects(objs...).Build()
	return New(cl, ns)
}

var rc = orchestrator.RunContext{Owner: "user:henrik", Topic: "OTel Arrow columns", Audience: "k8s admins", Notes: "deep dive", SourceURL: "https://example.com/src"}

// The load-bearing P3 invariant: the seeded tutorial-default team, resolved
// through the loader, reproduces DefaultEngine() byte-for-byte.
func TestLoader_TutorialDefaultMatchesDefaultEngine(t *testing.T) {
	objs := append(seedAgents(), tutorialTeam(), contentTeam())
	l := loader(t, objs...)

	want, err := orchestrator.DefaultEngine().Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatalf("DefaultEngine: %v", err)
	}
	got, err := l.Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatalf("loader Orchestrate: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		wj, _ := json.Marshal(want)
		gj, _ := json.Marshal(got)
		t.Fatalf("loader engine diverged from DefaultEngine:\n want: %s\n got: %s", wj, gj)
	}
}

// Live pickup: an edit to the CR is reflected on the next Orchestrate call
// without any restart — the loader re-resolves the team every run.
func TestLoader_LivePickupAfterEdit(t *testing.T) {
	team := tutorialTeam()
	l := loader(t, append(seedAgents(), team)...)
	cl := l.c

	first, err := l.Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Admin edits: the graphic node and its handoff leave the team — the next
	// run must reflect the smaller DAG (live pickup), not a boot-time engine.
	team.Spec.Nodes = team.Spec.Nodes[:4]
	team.Spec.Edges = team.Spec.Edges[:3]
	if err := cl.Update(context.Background(), team); err != nil {
		t.Fatalf("update team: %v", err)
	}

	second, err := l.Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatalf("second run after edit: %v", err)
	}
	if first.Markdown == second.Markdown {
		t.Fatal("a CR edit changed the DAG; the next run must reflect it (live pickup)")
	}
}

// Fail-closed at run time: every violation class the admission webhook rejects
// is also rejected here if it slips past admission (direct kubectl edit).
func TestLoader_FailClosed(t *testing.T) {
	cycle := tutorialTeam()
	cycle.Spec.Edges = append(cycle.Spec.Edges, v1alpha1.AgentEdge{From: "coding", To: "architect"})

	orphan := tutorialTeam()
	orphan.Spec.Edges = orphan.Spec.Edges[:3] // graphic is now unreachable

	unknownRef := tutorialTeam()
	unknownRef.Spec.Nodes[4].AgentRef = "ghost-agent"

	badEndpoint := tutorialTeam()
	badEndpoint.Spec.Edges = append(badEdgeList(), v1alpha1.AgentEdge{From: "architect", To: "ghost"})

	dup := tutorialTeam()
	dup.Spec.Nodes = append(dup.Spec.Nodes, v1alpha1.AgentNode{ID: "research", AgentRef: "research"})

	noDefault := tutorialTeam()
	noDefault.Spec.Default = false

	twoDefaults := tutorialTeam()
	other := tutorialTeam()
	other.Name = "other-team"
	other.Spec.Default = true

	badEntry := tutorialTeam()
	badEntry.Spec.Entry = "ghost"

	cases := []struct {
		name string
		objs []client.Object
		want string
	}{
		{"cycle", []client.Object{cycle}, "cycle"},
		{"orphan", []client.Object{orphan}, "never reached"},
		{"unknown agentRef", []client.Object{unknownRef}, "unknown agent"},
		{"undeclared edge endpoint", []client.Object{badEndpoint}, "undeclared node"},
		{"duplicate node id", []client.Object{dup}, "more than once"},
		{"no default team", []client.Object{noDefault}, "no default AgentTeam"},
		{"two default teams", []client.Object{twoDefaults, other}, "both default"},
		{"undeclared entry", []client.Object{badEntry}, "not a declared node"},
	}
	agents := seedAgents()
	for _, tc := range cases {
		objs := append(append([]client.Object{}, agents...), tc.objs...)
		l := loader(t, objs...)
		_, err := l.Orchestrate(context.Background(), rc)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}

// The linear-writer team is special-cased, not looked up: it runs the app's
// linear content path, so the DAG loader reports it distinctly.
func TestLoader_LinearWriterTeam(t *testing.T) {
	ct := contentTeam()
	ct.Spec.Default = true
	l := loader(t, append(seedAgents(), ct)...)
	_, err := l.Engine(context.Background())
	if !errors.Is(err, ErrLinearTeam) {
		t.Fatalf("want ErrLinearTeam, got %v", err)
	}
}

// A writer node mixed into a DAG team is a hard error.
func TestLoader_WriterMixedIntoDAG(t *testing.T) {
	team := tutorialTeam()
	team.Spec.Nodes = append(team.Spec.Nodes, v1alpha1.AgentNode{ID: "writer", AgentRef: "content-writer"})
	l := loader(t, append(seedAgents(), team)...)
	_, err := l.Engine(context.Background())
	if err == nil || !strings.Contains(err.Error(), "mixes the linear writer") {
		t.Fatalf("want mixed-writer error, got %v", err)
	}
}

// Seed-missing degrade: with no AgentTeam CRs at all (the RBAC-gap case the
// seed package documents), the loader falls back to today's DefaultEngine().
func TestLoader_NoTeamsDegradesToDefaultEngine(t *testing.T) {
	l := loader(t, seedAgents()...)
	mix, err := l.Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatalf("degraded run: %v", err)
	}
	want, err := orchestrator.DefaultEngine().Orchestrate(context.Background(), rc)
	if err != nil {
		t.Fatalf("DefaultEngine: %v", err)
	}
	if !reflect.DeepEqual(want, mix) {
		t.Fatal("no-team fallback must reproduce DefaultEngine() exactly")
	}
}

func badEdgeList() []v1alpha1.AgentEdge {
	return []v1alpha1.AgentEdge{
		{From: "architect", To: "research"},
		{From: "architect", To: "coding"},
		{From: "architect", To: "observability"},
		{From: "architect", To: "graphic"},
	}
}

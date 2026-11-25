package seed

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

const ns = "content-remixer"

func newClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Defaults creates the full §6 seed set when the namespace is empty: six Agents
// and two AgentTeams reproducing today's DefaultEngine() DAG.
func TestDefaults_CreatesFullSeedSet(t *testing.T) {
	c := newClient(t)
	if err := Defaults(context.Background(), c, ns, quietLog()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var agents v1alpha1.AgentList
	if err := c.List(context.Background(), &agents); err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents.Items) != 6 {
		t.Fatalf("want 6 seeded agents, got %d", len(agents.Items))
	}

	var teams v1alpha1.AgentTeamList
	if err := c.List(context.Background(), &teams); err != nil {
		t.Fatalf("list teams: %v", err)
	}
	if len(teams.Items) != 2 {
		t.Fatalf("want 2 seeded teams, got %d", len(teams.Items))
	}

	// The default team must be the architect fan-out reproducing DefaultEngine().
	var def *v1alpha1.AgentTeam
	for i := range teams.Items {
		if teams.Items[i].Spec.Default {
			def = &teams.Items[i]
		}
	}
	if def == nil {
		t.Fatal("no default team seeded")
	}
	if def.Name != "tutorial-default" || def.Spec.Entry != "architect" || len(def.Spec.Edges) != 4 {
		t.Fatalf("default team is not the architect fan-out: %+v", def.Spec)
	}
}

// Defaults never clobbers an operator's edit: a pre-existing Agent is left
// exactly as the admin set it (create-if-absent, idempotent across restarts).
func TestDefaults_PreservesExistingEdits(t *testing.T) {
	edited := &v1alpha1.Agent{}
	edited.Name = "architect"
	edited.Namespace = ns
	edited.Spec.Role = "Edited Role"
	edited.Spec.SkillKind = v1alpha1.SkillKindArchitect
	edited.Spec.ModelProfile = "premium" // admin picked a non-default model
	c := newClient(t, edited)

	if err := Defaults(context.Background(), c, ns, quietLog()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var got v1alpha1.Agent
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "architect"}, &got); err != nil {
		t.Fatalf("get architect: %v", err)
	}
	if got.Spec.Role != "Edited Role" || got.Spec.ModelProfile != "premium" {
		t.Fatalf("seed clobbered an existing edit: %+v", got.Spec)
	}

	// Re-running is a no-op (idempotent): still exactly the full set, one copy each.
	if err := Defaults(context.Background(), c, ns, quietLog()); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	var agents v1alpha1.AgentList
	if err := c.List(context.Background(), &agents); err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents.Items) != 6 {
		t.Fatalf("want 6 agents after re-seed, got %d", len(agents.Items))
	}
}

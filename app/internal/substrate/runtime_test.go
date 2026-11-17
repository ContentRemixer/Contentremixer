package substrate

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testActor() *Actor {
	return &Actor{
		ID:             "act_abc",
		ActorTemplate:  "media",
		Image:          "ghcr.io/x/media-actor:v1",
		Skill:          "video-designer",
		WorkOrder:      WorkOrder{Repo: "o/r", IssueNumber: 7, Inputs: Inputs{OutputFormat: "demo"}},
		MemoryRef:      MemoryRef{Owner: "o", Persona: "p"},
		WorkerSelector: map[string]string{"pool": "media"},
		TokenSecretRef: &SecretRef{Name: "repo-pat", Key: "token"},
	}
}

func TestK8sRuntimeSpawnCreatesHardenedJob(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	rt := &K8sRuntime{Client: cl, Namespace: "contentremixer", ServiceAccount: "actor-sa"}
	a := testActor()

	name, err := rt.Spawn(context.Background(), a)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if name != "actor-act-abc" {
		t.Fatalf("job name = %q", name)
	}

	var job batchv1.Job
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "contentremixer", Name: name}, &job); err != nil {
		t.Fatalf("job not created: %v", err)
	}
	pod := job.Spec.Template.Spec
	if pod.ServiceAccountName != "actor-sa" {
		t.Fatalf("sa = %q", pod.ServiceAccountName)
	}
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Fatalf("pod not hardened runAsNonRoot")
	}
	c := pod.Containers[0]
	if c.SecurityContext == nil || c.SecurityContext.ReadOnlyRootFilesystem == nil || !*c.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatalf("container not hardened readOnlyRootFilesystem")
	}
	if c.Image != a.Image {
		t.Fatalf("image = %q", c.Image)
	}
	if pod.NodeSelector["pool"] != "media" {
		t.Fatalf("worker selector not applied: %v", pod.NodeSelector)
	}
	// Token must ride as a SecretRef, never a literal (contract §3.1).
	var sawTokenRef bool
	for _, e := range c.Env {
		if e.Name == "GITHUB_TOKEN" {
			if e.Value != "" || e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
				t.Fatalf("GITHUB_TOKEN must be a SecretRef, got %+v", e)
			}
			sawTokenRef = true
		}
	}
	if !sawTokenRef {
		t.Fatalf("GITHUB_TOKEN env not projected")
	}
}

func TestK8sRuntimeSpawnIdempotent(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	rt := &K8sRuntime{Client: cl, Namespace: "contentremixer"}
	a := testActor()
	if _, err := rt.Spawn(context.Background(), a); err != nil {
		t.Fatalf("spawn 1: %v", err)
	}
	// Second spawn with the same id collides on the deterministic name → no error.
	if _, err := rt.Spawn(context.Background(), a); err != nil {
		t.Fatalf("spawn 2 (idempotent): %v", err)
	}
}

func TestK8sRuntimeObserveStates(t *testing.T) {
	a := testActor()
	a.JobName = jobNameFor(a.ID)
	cases := []struct {
		name   string
		status batchv1.JobStatus
		want   ActorState
	}{
		{"active", batchv1.JobStatus{Active: 1}, StateRunning},
		{"succeeded", batchv1.JobStatus{Succeeded: 1}, StateSucceeded},
		{"failed", batchv1.JobStatus{Failed: 1}, StateFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: a.JobName, Namespace: "contentremixer"},
				Status:     tc.status,
			}
			cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(job).Build()
			rt := &K8sRuntime{Client: cl, Namespace: "contentremixer"}
			st, _, _, _, err := rt.Observe(context.Background(), a)
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if st != tc.want {
				t.Fatalf("state = %q, want %q", st, tc.want)
			}
		})
	}
}

func TestK8sRuntimeTeardownIdempotent(t *testing.T) {
	a := testActor()
	a.JobName = jobNameFor(a.ID)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: a.JobName, Namespace: "contentremixer"}}
	cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(job).Build()
	rt := &K8sRuntime{Client: cl, Namespace: "contentremixer"}
	if err := rt.Teardown(context.Background(), a); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	// Deleting an already-gone Job is not an error.
	if err := rt.Teardown(context.Background(), a); err != nil {
		t.Fatalf("teardown idempotent: %v", err)
	}
	// Prove it's gone.
	var got batchv1.Job
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "contentremixer", Name: a.JobName}, &got); err == nil {
		t.Fatalf("job should be deleted")
	}
}

package scrapejobs

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeClient(t *testing.T) client.Client {
	t.Helper()
	sch := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(sch); err != nil {
		t.Fatal(err)
	}
	return fakeclient.NewClientBuilder().WithScheme(sch).Build()
}

func TestK8sLauncher_CreatesHardenedJob(t *testing.T) {
	c := newFakeClient(t)
	l := &K8sLauncher{
		Client: c, Namespace: "content-remixer", Image: "ghcr.io/x/scrape-job:abc",
		ServiceAccount: "booth-scraper", DSNSecretName: "booth-review-dsn", DSNSecretKey: "dsn",
	}
	name, err := l.Launch(context.Background(), JobSpec{
		JobID: "abcdef1234567890", OwnerSubject: "alice", PersonaName: "p",
		SourceURL: "https://x/a", SourceKind: "article", ReferenceRepo: "repo",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if name != "scrape-abcdef123456" {
		t.Errorf("job name = %q", name)
	}

	var job batchv1.Job
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "content-remixer", Name: name}, &job); err != nil {
		t.Fatalf("get created job: %v", err)
	}
	ctr := job.Spec.Template.Spec.Containers[0]
	if ctr.Image != "ghcr.io/x/scrape-job:abc" {
		t.Errorf("image = %q", ctr.Image)
	}
	// Spec env carries the scrape parameters as literals, DSN as a secret ref.
	env := map[string]string{}
	var dsnFromSecret bool
	for _, e := range ctr.Env {
		env[e.Name] = e.Value
		if e.Name == "REVIEW_DSN" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			dsnFromSecret = e.ValueFrom.SecretKeyRef.Name == "booth-review-dsn"
		}
	}
	if env["SCRAPE_SOURCE_URL"] != "https://x/a" || env["SCRAPE_OWNER_SUBJECT"] != "alice" {
		t.Errorf("env missing scrape params: %+v", env)
	}
	if !dsnFromSecret {
		t.Error("REVIEW_DSN should come from the configured secret, not a literal")
	}
	// Hardened posture (ISI-7161 security parity): non-root, read-only rootfs, drop ALL.
	if ctr.SecurityContext == nil || ctr.SecurityContext.ReadOnlyRootFilesystem == nil || !*ctr.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("container should run read-only rootfs")
	}
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
}

func TestK8sLauncher_ConfigGuards(t *testing.T) {
	c := newFakeClient(t)
	// No image → error.
	if _, err := (&K8sLauncher{Client: c, DSNSecretName: "s", DSNSecretKey: "k"}).
		Launch(context.Background(), JobSpec{JobID: "j"}); err == nil {
		t.Error("want error when image unset")
	}
	// No DSN secret → error.
	if _, err := (&K8sLauncher{Client: c, Image: "img"}).
		Launch(context.Background(), JobSpec{JobID: "j"}); err == nil {
		t.Error("want error when DSN secret unset")
	}
}

func TestJobName(t *testing.T) {
	cases := map[string]string{
		"abcdef1234567890": "scrape-abcdef123456",
		"short":            "scrape-short",
		"_leading":         "scrape-leading",
	}
	for in, want := range cases {
		if got := jobName(in); got != want {
			t.Errorf("jobName(%q) = %q, want %q", in, got, want)
		}
	}
}

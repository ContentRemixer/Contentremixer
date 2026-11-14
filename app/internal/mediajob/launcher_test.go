package mediajob

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

func getJob(t *testing.T, c client.Client, ns, name string) batchv1.Job {
	t.Helper()
	var job batchv1.Job
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &job); err != nil {
		t.Fatalf("get created job: %v", err)
	}
	return job
}

func TestLaunch_CreatesHardenedMediaJob(t *testing.T) {
	c := newFakeClient(t)
	l := &K8sLauncher{
		Client: c, Namespace: "content-remixer",
		Image:           "ghcr.io/contentremixer/media-actor:sha-abc",
		ServiceAccount:  "media-actor",
		TokenSecretName: "repo-pat", TokenSecretKey: "token",
	}
	name, err := l.Launch(context.Background(), JobSpec{
		JobID: "abcdef1234567890", OwnerSubject: "alice",
		Repo: "henrikrexed/demo", IssueNumber: 42,
		Skill: SkillDemoRecorder, Scenario: "demo-otelt.mjs",
		Orientation: "landscape", SourceURL: "https://play.grafana.org",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if name != "media-abcdef123456" {
		t.Errorf("job name = %q, want media-abcdef123456", name)
	}

	job := getJob(t, c, "content-remixer", name)

	// Job-level hardening.
	if got := *job.Spec.BackoffLimit; got != 1 {
		t.Errorf("BackoffLimit = %d, want 1", got)
	}
	if got := *job.Spec.TTLSecondsAfterFinished; got != 3600 {
		t.Errorf("TTL = %d, want 3600", got)
	}
	if got := *job.Spec.ActiveDeadlineSeconds; got != 900 {
		t.Errorf("ActiveDeadlineSeconds = %d, want 900", got)
	}
	pod := job.Spec.Template.Spec
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", pod.RestartPolicy)
	}
	if pod.ServiceAccountName != "media-actor" {
		t.Errorf("ServiceAccount = %q", pod.ServiceAccountName)
	}

	// Pod securityContext: non-root as uid 1000 (pwuser), seccomp runtime/default.
	psc := pod.SecurityContext
	if psc == nil || psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Fatalf("pod not RunAsNonRoot: %+v", psc)
	}
	if psc.RunAsUser == nil || *psc.RunAsUser != 1000 {
		t.Errorf("pod RunAsUser = %v, want 1000", psc.RunAsUser)
	}
	if psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("pod seccomp = %+v, want RuntimeDefault", psc.SeccompProfile)
	}

	ctr := pod.Containers[0]
	if ctr.Image != "ghcr.io/contentremixer/media-actor:sha-abc" {
		t.Errorf("image = %q", ctr.Image)
	}
	if ctr.WorkingDir != mediaRoot {
		t.Errorf("workingDir = %q, want %q", ctr.WorkingDir, mediaRoot)
	}

	// Container securityContext: non-root, no priv-esc, read-only rootfs, drop-ALL.
	csc := ctr.SecurityContext
	if csc == nil {
		t.Fatal("nil container securityContext")
	}
	if csc.RunAsUser == nil || *csc.RunAsUser != 1000 {
		t.Errorf("ctr RunAsUser = %v, want 1000", csc.RunAsUser)
	}
	if csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation {
		t.Error("AllowPrivilegeEscalation should be false")
	}
	if csc.ReadOnlyRootFilesystem == nil || !*csc.ReadOnlyRootFilesystem {
		t.Error("ReadOnlyRootFilesystem should be true")
	}
	if csc.Capabilities == nil || len(csc.Capabilities.Drop) != 1 || csc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("capabilities drop = %+v, want [ALL]", csc.Capabilities)
	}

	// Command: record.mjs against the scenario, delivering into /artifacts.
	wantCmd := []string{
		"node", "demo-recorder/record.mjs",
		"demo-recorder/scenarios/demo-otelt.mjs",
		"--slug", "demo-otelt",
		"--content-root", "/artifacts",
		"--orientation", "landscape",
	}
	if len(ctr.Command) != len(wantCmd) {
		t.Fatalf("command = %v, want %v", ctr.Command, wantCmd)
	}
	for i := range wantCmd {
		if ctr.Command[i] != wantCmd[i] {
			t.Errorf("command[%d] = %q, want %q", i, ctr.Command[i], wantCmd[i])
		}
	}

	// Env literals + the PAT as a secret ref (never a literal).
	env := map[string]string{}
	var tokenFromSecret bool
	for _, e := range ctr.Env {
		env[e.Name] = e.Value
		if e.Name == "GITHUB_TOKEN" {
			if e.Value != "" {
				t.Error("GITHUB_TOKEN must not be a literal")
			}
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				tokenFromSecret = e.ValueFrom.SecretKeyRef.Name == "repo-pat" &&
					e.ValueFrom.SecretKeyRef.Key == "token"
			}
		}
	}
	if !tokenFromSecret {
		t.Error("GITHUB_TOKEN not injected from the configured secret")
	}
	if env["MEDIA_SKILL"] != "demo-recorder" || env["MEDIA_SCENARIO"] != "demo-otelt.mjs" {
		t.Errorf("media env wrong: %+v", env)
	}
	if env["MEDIA_REPO"] != "henrikrexed/demo" || env["MEDIA_ISSUE_NUMBER"] != "42" {
		t.Errorf("repo/issue env wrong: %+v", env)
	}
	if env["MEDIA_CONTENT_ROOT"] != "/artifacts" || env["HOME"] != "/tmp" {
		t.Errorf("content-root/HOME env wrong: %+v", env)
	}
	if env["MEDIA_SOURCE_URL"] != "https://play.grafana.org" || env["MEDIA_ORIENTATION"] != "landscape" {
		t.Errorf("optional env wrong: %+v", env)
	}

	// Writable emptyDir volumes under the read-only rootfs: /artifacts + /tmp.
	mounts := map[string]string{}
	for _, m := range ctr.VolumeMounts {
		mounts[m.Name] = m.MountPath
	}
	if mounts["artifacts"] != "/artifacts" || mounts["scratch"] != "/tmp" {
		t.Errorf("volume mounts wrong: %+v", mounts)
	}
	vols := map[string]bool{}
	for _, v := range pod.Volumes {
		if v.EmptyDir == nil {
			t.Errorf("volume %q is not an emptyDir", v.Name)
		}
		vols[v.Name] = true
	}
	if !vols["artifacts"] || !vols["scratch"] {
		t.Errorf("missing emptyDir volumes: %+v", vols)
	}

	// Labels carry job-id + skill for correlation.
	if job.Labels["contentremixer.io/job-id"] != "abcdef1234567890" ||
		job.Labels["contentremixer.io/skill"] != "demo-recorder" {
		t.Errorf("labels wrong: %+v", job.Labels)
	}
}

func TestLaunch_NoTokenWhenUnconfigured(t *testing.T) {
	c := newFakeClient(t)
	l := &K8sLauncher{
		Client: c, Namespace: "ns", Image: "img", ServiceAccount: "sa",
	}
	name, err := l.Launch(context.Background(), JobSpec{
		JobID: "run1", Skill: SkillDemoRecorder, Scenario: "example.mjs",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	job := getJob(t, c, "ns", name)
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "GITHUB_TOKEN" {
			t.Error("GITHUB_TOKEN injected without a configured secret")
		}
	}
	// Slug defaults to the scenario basename.
	env := map[string]string{}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["MEDIA_SLUG"] != "example" {
		t.Errorf("default slug = %q, want example", env["MEDIA_SLUG"])
	}
}

func TestLaunch_Errors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		l    *K8sLauncher
		spec JobSpec
	}{
		{
			name: "no image",
			l:    &K8sLauncher{Client: newFakeClient(t), Namespace: "ns"},
			spec: JobSpec{JobID: "x", Skill: SkillDemoRecorder, Scenario: "s.mjs"},
		},
		{
			name: "unsupported skill",
			l:    &K8sLauncher{Client: newFakeClient(t), Namespace: "ns", Image: "img"},
			spec: JobSpec{JobID: "x", Skill: Skill("video-designer"), Scenario: "s.mjs"},
		},
		{
			name: "missing scenario",
			l:    &K8sLauncher{Client: newFakeClient(t), Namespace: "ns", Image: "img"},
			spec: JobSpec{JobID: "x", Skill: SkillDemoRecorder},
		},
		{
			name: "bad size limit",
			l:    &K8sLauncher{Client: newFakeClient(t), Namespace: "ns", Image: "img", ArtifactSizeLimit: "notaqty"},
			spec: JobSpec{JobID: "x", Skill: SkillDemoRecorder, Scenario: "s.mjs"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.l.Launch(ctx, tc.spec); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestJobName(t *testing.T) {
	cases := map[string]string{
		"abcdef1234567890": "media-abcdef123456",
		"SHORT":            "media-short",
		"_leading":         "media-leading",
		"":                 "media-run",
	}
	for in, want := range cases {
		if got := jobName(in); got != want {
			t.Errorf("jobName(%q) = %q, want %q", in, got, want)
		}
	}
}

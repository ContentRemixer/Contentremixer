// Package mediajob launches the media actor template (ISI-7395, parent
// ISI-7389 ADR-1/§7) as an ephemeral, hardened Kubernetes batch Job. It is the
// k8s-Job runtime behind dispatch.MediaStrategy: a `template=media` WorkOrder
// (recycle:demo today, recycle:video next) becomes one Job running the
// media-actor image (ghcr.io/contentremixer/media-actor) with the requested
// media skill.
//
// It deliberately mirrors the proven scrape-Job launcher
// (booth/internal/scrapejobs.K8sLauncher): RestartPolicyNever, BackoffLimit,
// TTLSecondsAfterFinished, ActiveDeadlineSeconds, and a drop-ALL / non-root /
// read-only-rootfs securityContext so the same Trivy KSV checks pass. It
// differs in three media-specific ways:
//
//   - uid 1000 (pwuser), not 65532: the media-actor image bakes Playwright's
//     browsers + the skill tree owned by pwuser, so the Job must run as that
//     user to read them (scrape-job's distroless runs as 65532).
//   - two writable emptyDir volumes under the read-only rootfs: /tmp (Chromium
//     profile + HOME + the recorder's mkdtemp scratch) and /artifacts (the
//     "Crate" the recorder delivers demo.mp4 + meta.yaml into).
//   - the flush-before-suspend invariant (ADR-4): because an emptyDir dies with
//     the pod, the operator never reads the MP4 back from a finished Job. The
//     container's own success gate must include the durable flush — the
//     recorder writes into /artifacts and then commits + pushes the MP4 to the
//     PR branch (git + the per-repo PAT are baked / injected) BEFORE it exits 0.
//     Job completion therefore *implies* the artifact is durably stored. The
//     launcher encodes this by injecting git credentials and mounting no
//     host-visible path the operator would otherwise read.
package mediajob

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Skill names the media skill a Job runs. Only demo-recorder is wired today
// (ISI-7388); video-designer slots in behind the same launcher next.
type Skill string

const (
	// SkillDemoRecorder records a Playwright scenario and transcodes it to MP4.
	SkillDemoRecorder Skill = "demo-recorder"
)

// JobSpec is the fully-resolved media request the launcher turns into a Job.
// Owner is already §6.3-authorized upstream; formats/source are resolved from
// the recycle issue form (dispatch.WorkOrder).
type JobSpec struct {
	// JobID is the originating recycle run id (uuid); it derives the Job name.
	JobID string
	// OwnerSubject is the tenancy subject the run belongs to.
	OwnerSubject string
	// Repo is the GitHub "owner/name" the PR targets.
	Repo string
	// IssueNumber is the originating recycle issue.
	IssueNumber int
	// Skill selects the media skill (demo-recorder). Required.
	Skill Skill
	// Scenario is the skill's scenario selector. For demo-recorder it is the
	// scenario module baked under the image's demo-recorder/scenarios/ (e.g.
	// "demo-otelt.mjs"). Required for demo-recorder.
	Scenario string
	// Slug names the delivered artifact dir inside the Crate (content-root).
	// Defaults to a value derived from the scenario when empty.
	Slug string
	// Orientation is "landscape" (default) or "shorts"; passed through verbatim.
	Orientation string
	// SourceURL is the demo target URL recorded against (observability only;
	// the scenario module itself drives navigation).
	SourceURL string
}

// Launcher enqueues a media run as an out-of-process unit. The real impl
// creates a Kubernetes batch Job; tests inject a fake so MediaStrategy is
// unit-testable without a cluster.
type Launcher interface {
	// Launch starts the media Job and returns the created Job's name. A non-nil
	// error leaves the run for the caller to mark failed.
	Launch(ctx context.Context, spec JobSpec) (jobName string, err error)
}

// K8sLauncher creates batch Jobs running the media-actor image. It reuses the
// operator's controller-runtime client (clientgoscheme already registers
// batch/v1), so no extra wiring is needed.
type K8sLauncher struct {
	Client    client.Client
	Namespace string
	// Image is the media-actor container image (ghcr.io/contentremixer/media-actor).
	Image string
	// ServiceAccount the Job pod runs as. It needs no cluster API access; it
	// only reaches the demo target + GitHub (for the flush push).
	ServiceAccount string
	// TokenSecretName / TokenSecretKey point at the Secret holding the per-repo
	// PAT, injected as GITHUB_TOKEN so the recorder can commit + push the MP4
	// to the PR branch (the flush-before-suspend step). The token never rides
	// in the Job manifest. Optional: when empty, no token is injected (the Job
	// records but cannot push — used by image/launcher smoke paths).
	TokenSecretName string
	TokenSecretKey  string
	// RunAsUser overrides the Job's uid. Defaults to 1000 (pwuser) to match the
	// media-actor image's baked ownership.
	RunAsUser int64
	// BackoffLimit bounds retries (default 1 — a recording retry is cheap to
	// re-trigger but expensive to run, so retry conservatively).
	BackoffLimit int32
	// TTLSecondsAfterFinished garbage-collects finished Jobs (default 3600).
	TTLSecondsAfterFinished int32
	// ActiveDeadlineSeconds caps a single run (default 900 — a record + ffmpeg
	// transcode + git push is longer than a scrape; media is heavier).
	ActiveDeadlineSeconds int64
	// ArtifactSizeLimit bounds the /artifacts + /tmp emptyDirs (default 2Gi —
	// a WebM + its MP4 + Chromium profile scratch).
	ArtifactSizeLimit string
}

var _ Launcher = (*K8sLauncher)(nil)

const (
	// mediaRoot is the WORKDIR baked into the media-actor image.
	mediaRoot = "/opt/media"
	// artifactDir is the content-root ("Crate") the recorder delivers into.
	// It is a writable emptyDir under the read-only rootfs.
	artifactDir = "/artifacts"
	// scratchDir is the writable HOME + mkdtemp scratch emptyDir.
	scratchDir = "/tmp"
)

func (l *K8sLauncher) Launch(ctx context.Context, spec JobSpec) (string, error) {
	if l.Image == "" {
		return "", fmt.Errorf("mediajob: launcher has no media-actor image configured")
	}
	if spec.Skill != SkillDemoRecorder {
		return "", fmt.Errorf("mediajob: unsupported skill %q (only %q is wired)", spec.Skill, SkillDemoRecorder)
	}
	cmd, err := skillCommand(spec)
	if err != nil {
		return "", err
	}

	name := jobName(spec.JobID)
	backoff := l.BackoffLimit
	if backoff == 0 {
		backoff = 1
	}
	ttl := l.TTLSecondsAfterFinished
	if ttl == 0 {
		ttl = 3600
	}
	deadline := l.ActiveDeadlineSeconds
	if deadline == 0 {
		deadline = 900
	}
	uid := l.RunAsUser
	if uid == 0 {
		uid = 1000
	}
	sizeLimit := l.ArtifactSizeLimit
	if sizeLimit == "" {
		sizeLimit = "2Gi"
	}
	qty, err := resource.ParseQuantity(sizeLimit)
	if err != nil {
		return "", fmt.Errorf("mediajob: invalid ArtifactSizeLimit %q: %w", sizeLimit, err)
	}

	env := []corev1.EnvVar{
		{Name: "MEDIA_JOB_ID", Value: spec.JobID},
		{Name: "MEDIA_OWNER_SUBJECT", Value: spec.OwnerSubject},
		{Name: "MEDIA_REPO", Value: spec.Repo},
		{Name: "MEDIA_ISSUE_NUMBER", Value: strconv.Itoa(spec.IssueNumber)},
		{Name: "MEDIA_SKILL", Value: string(spec.Skill)},
		{Name: "MEDIA_SCENARIO", Value: spec.Scenario},
		{Name: "MEDIA_SLUG", Value: slugOf(spec)},
		{Name: "MEDIA_CONTENT_ROOT", Value: artifactDir},
		// HOME is baked to /tmp in the image, but set it explicitly so the
		// contract survives a base-image change (Chromium needs a writable HOME
		// under the read-only rootfs).
		{Name: "HOME", Value: scratchDir},
	}
	if spec.Orientation != "" {
		env = append(env, corev1.EnvVar{Name: "MEDIA_ORIENTATION", Value: spec.Orientation})
	}
	if spec.SourceURL != "" {
		env = append(env, corev1.EnvVar{Name: "MEDIA_SOURCE_URL", Value: spec.SourceURL})
	}
	// The per-repo PAT powers the flush-before-suspend push. Injected from a
	// Secret so it never rides in the manifest; omitted when unconfigured.
	if l.TokenSecretName != "" && l.TokenSecretKey != "" {
		env = append(env, corev1.EnvVar{Name: "GITHUB_TOKEN", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: l.TokenSecretName},
				Key:                  l.TokenSecretKey,
			},
		}})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: l.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "media-actor",
				"app.kubernetes.io/component": "media",
				"contentremixer.io/job-id":    spec.JobID,
				"contentremixer.io/skill":     string(spec.Skill),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"contentremixer.io/job-id": spec.JobID,
						"contentremixer.io/skill":  string(spec.Skill),
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: l.ServiceAccount,
					SecurityContext:    hardenedPodSecurity(uid),
					Volumes: []corev1.Volume{
						emptyDirVolume("artifacts", qty),
						emptyDirVolume("scratch", qty),
					},
					Containers: []corev1.Container{{
						Name:            "media-actor",
						Image:           l.Image,
						WorkingDir:      mediaRoot,
						Command:         cmd,
						Env:             env,
						SecurityContext: hardenedContainerSecurity(uid),
						VolumeMounts: []corev1.VolumeMount{
							{Name: "artifacts", MountPath: artifactDir},
							{Name: "scratch", MountPath: scratchDir},
						},
					}},
				},
			},
		},
	}
	if err := l.Client.Create(ctx, job); err != nil {
		return "", fmt.Errorf("mediajob: create k8s job %s: %w", name, err)
	}
	return name, nil
}

// skillCommand builds the container command for the requested skill. For
// demo-recorder it invokes the baked record.mjs against the scenario, writing
// the delivered MP4 into the /artifacts content-root (the Crate).
func skillCommand(spec JobSpec) ([]string, error) {
	switch spec.Skill {
	case SkillDemoRecorder:
		if spec.Scenario == "" {
			return nil, fmt.Errorf("mediajob: demo-recorder requires a scenario")
		}
		cmd := []string{
			"node", "demo-recorder/record.mjs",
			"demo-recorder/scenarios/" + spec.Scenario,
			"--slug", slugOf(spec),
			"--content-root", artifactDir,
		}
		if spec.Orientation != "" {
			cmd = append(cmd, "--orientation", spec.Orientation)
		}
		return cmd, nil
	default:
		return nil, fmt.Errorf("mediajob: unsupported skill %q", spec.Skill)
	}
}

// slugOf resolves the artifact slug: the explicit Slug, else the scenario
// basename with its extension stripped.
func slugOf(spec JobSpec) string {
	if spec.Slug != "" {
		return spec.Slug
	}
	s := spec.Scenario
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".mjs")
	if s == "" {
		return "demo"
	}
	return s
}

func emptyDirVolume(name string, sizeLimit resource.Quantity) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &sizeLimit},
		},
	}
}

// jobName derives a DNS-1123 Job name from the job id (uuid). Deterministic, so
// a retried create of the same id collides rather than duplicating.
func jobName(jobID string) string {
	id := strings.ToLower(strings.ReplaceAll(jobID, "_", "-"))
	id = strings.TrimPrefix(id, "-")
	if len(id) > 12 {
		id = id[:12]
	}
	if id == "" {
		id = "run"
	}
	return "media-" + id
}

// hardenedPodSecurity / hardenedContainerSecurity match the non-root, seccomp,
// drop-ALL posture the scrape-Job launcher and operator overlay enforce
// (ISI-7161 security pass), so Trivy KSV checks pass on this Job the same way.
// uid is the image's baked runtime user (1000 / pwuser for media-actor).
func hardenedPodSecurity(uid int64) *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsNonRoot:   boolp(true),
		RunAsUser:      &uid,
		RunAsGroup:     &uid,
		FSGroup:        &uid,
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

func hardenedContainerSecurity(uid int64) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsNonRoot:             boolp(true),
		RunAsUser:                &uid,
		AllowPrivilegeEscalation: boolp(false),
		// readOnlyRootFilesystem is safe because the only writes go to the
		// /artifacts + /tmp emptyDir mounts (Chromium profile, recorder scratch,
		// delivered MP4). The baked /opt/media + /ms-playwright stay read-only.
		ReadOnlyRootFilesystem: boolp(true),
		Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

func boolp(b bool) *bool { return &b }

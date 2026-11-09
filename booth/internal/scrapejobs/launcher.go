package scrapejobs

import (
	"context"
	"fmt"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// JobSpec is the fully-resolved scrape request the launcher turns into a
// Kubernetes batch Job. Owner is already §6.3-authorized by the gating store.
type JobSpec struct {
	JobID         string
	OwnerSubject  string
	ReferenceRepo string
	PersonaName   string
	SourceURL     string
	SourceKind    string
}

// Launcher enqueues a scrape as an out-of-process unit. The real impl creates a
// Kubernetes batch Job (plan §0.2); tests inject a fake so the gating store is
// unit-testable without a cluster.
type Launcher interface {
	// Launch starts the scrape and returns the created Job's name. A non-nil
	// error leaves the job row for the caller to mark failed.
	Launch(ctx context.Context, spec JobSpec) (jobName string, err error)
}

// K8sLauncher creates batch Jobs running the app-module scrape-job image. It
// reuses the Booth ServiceAccount's controller-runtime client (clientgoscheme
// already registers batch/v1), so no extra client wiring is needed.
type K8sLauncher struct {
	Client    client.Client
	Namespace string
	// Image is the scrape-job container image (the app/cmd/scrape-job binary).
	Image string
	// ServiceAccount the Job pod runs as (needs no cluster API access itself —
	// it only reaches Postgres and the public internet).
	ServiceAccount string
	// DSNSecretName / DSNSecretKey point at the Secret holding the review/pgvector
	// DSN, injected as REVIEW_DSN so the DSN never rides in the Job manifest.
	DSNSecretName string
	DSNSecretKey  string
	// BackoffLimit bounds retries (default 2 when 0).
	BackoffLimit int32
	// TTLSecondsAfterFinished garbage-collects finished Jobs (default 3600).
	TTLSecondsAfterFinished int32
	// ActiveDeadlineSeconds caps a single run (default 600).
	ActiveDeadlineSeconds int64
}

var _ Launcher = (*K8sLauncher)(nil)

func (l *K8sLauncher) Launch(ctx context.Context, spec JobSpec) (string, error) {
	if l.Image == "" {
		return "", fmt.Errorf("scrapejobs: launcher has no scrape-job image configured")
	}
	if l.DSNSecretName == "" || l.DSNSecretKey == "" {
		return "", fmt.Errorf("scrapejobs: launcher has no REVIEW_DSN secret configured")
	}
	name := jobName(spec.JobID)
	backoff := l.BackoffLimit
	if backoff == 0 {
		backoff = 2
	}
	ttl := l.TTLSecondsAfterFinished
	if ttl == 0 {
		ttl = 3600
	}
	deadline := l.ActiveDeadlineSeconds
	if deadline == 0 {
		deadline = 600
	}

	env := []corev1.EnvVar{
		{Name: "SCRAPE_JOB_ID", Value: spec.JobID},
		{Name: "SCRAPE_OWNER_SUBJECT", Value: spec.OwnerSubject},
		{Name: "SCRAPE_REFERENCE_REPO", Value: spec.ReferenceRepo},
		{Name: "SCRAPE_PERSONA_NAME", Value: spec.PersonaName},
		{Name: "SCRAPE_SOURCE_URL", Value: spec.SourceURL},
		{Name: "SCRAPE_SOURCE_KIND", Value: spec.SourceKind},
		{Name: "REVIEW_DSN", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: l.DSNSecretName},
				Key:                  l.DSNSecretKey,
			},
		}},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: l.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "scrape-job",
				"app.kubernetes.io/component": "scraper",
				"contentremixer.io/job-id":    spec.JobID,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"contentremixer.io/job-id": spec.JobID},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: l.ServiceAccount,
					SecurityContext:    hardenedPodSecurity(),
					Containers: []corev1.Container{{
						Name:            "scrape-job",
						Image:           l.Image,
						Env:             env,
						SecurityContext: hardenedContainerSecurity(),
					}},
				},
			},
		},
	}
	if err := l.Client.Create(ctx, job); err != nil {
		return "", fmt.Errorf("scrapejobs: create k8s job %s: %w", name, err)
	}
	return name, nil
}

// jobName derives a DNS-1123 Job name from the job id (uuid). Deterministic, so
// a retried create of the same id collides rather than duplicating.
func jobName(jobID string) string {
	id := strings.ToLower(strings.ReplaceAll(jobID, "_", "-"))
	id = strings.TrimPrefix(id, "-")
	if len(id) > 12 {
		id = id[:12]
	}
	return "scrape-" + id
}

// hardenedPodSecurity / hardenedContainerSecurity match the non-root, seccomp,
// drop-ALL posture the operator overlay enforces (ISI-7161 security pass), so
// Trivy KSV checks pass on this Job the same way they do on the Deployment.
func hardenedPodSecurity() *corev1.PodSecurityContext {
	run := int64(65532)
	return &corev1.PodSecurityContext{
		RunAsNonRoot:   boolp(true),
		RunAsUser:      &run,
		RunAsGroup:     &run,
		FSGroup:        &run,
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}

func hardenedContainerSecurity() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: boolp(false),
		ReadOnlyRootFilesystem:   boolp(true),
		RunAsNonRoot:             boolp(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

func boolp(b bool) *bool { return &b }

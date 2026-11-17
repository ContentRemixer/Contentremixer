package substrate

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Runtime is the execution backend an actor runs on. The K8s implementation
// runs each actor as a Job of its template image; fakeRuntime backs the tests.
//
// Walking-skeleton semantics (ADR-4, contract §2): Spawn/Resume create the Job;
// Suspend tears the Job down while the Store keeps the record (a true
// CRIU/checkpoint snapshot of live execution state is a v2 — the skeleton
// persists the WorkOrder and re-creates the Job on resume, which is correct for
// an idempotent actor and exercises the full §3 contract end to end). Memory is
// never touched here — the actor flushes/re-grounds against the external style
// layer using the carried MemoryRef.
type Runtime interface {
	// Spawn starts the actor's Job and returns its name.
	Spawn(ctx context.Context, a *Actor) (jobName string, err error)
	// Observe reports the actor's current execution state from its Job.
	Observe(ctx context.Context, a *Actor) (ActorState, Phase, *Result, string, error)
	// Teardown deletes the actor's Job (suspend/terminate). Idempotent.
	Teardown(ctx context.Context, a *Actor) error
}

// K8sRuntime runs actors as batch Jobs via a controller-runtime client.
type K8sRuntime struct {
	Client         client.Client
	Namespace      string
	ServiceAccount string
	// BackoffLimit / TTL / deadline bound a run (sane defaults applied when 0).
	BackoffLimit            int32
	TTLSecondsAfterFinished int32
	ActiveDeadlineSeconds   int64
}

var _ Runtime = (*K8sRuntime)(nil)

func (r *K8sRuntime) Spawn(ctx context.Context, a *Actor) (string, error) {
	if a.Image == "" {
		return "", fmt.Errorf("substrate: actor %s has no image", a.ID)
	}
	name := jobNameFor(a.ID)
	backoff := r.BackoffLimit
	if backoff == 0 {
		backoff = 2
	}
	ttl := r.TTLSecondsAfterFinished
	if ttl == 0 {
		ttl = 3600
	}
	deadline := r.ActiveDeadlineSeconds
	if deadline == 0 {
		deadline = 1800
	}

	env := []corev1.EnvVar{
		{Name: "ACTOR_ID", Value: a.ID},
		{Name: "ACTOR_TEMPLATE", Value: a.ActorTemplate},
		{Name: "ACTOR_SKILL", Value: a.Skill},
		{Name: "WORKORDER_REPO", Value: a.WorkOrder.Repo},
		{Name: "WORKORDER_ISSUE", Value: strconv.Itoa(a.WorkOrder.IssueNumber)},
		{Name: "WORKORDER_SOURCE_PATH", Value: a.WorkOrder.Inputs.SourcePath},
		{Name: "WORKORDER_NOTES", Value: a.WorkOrder.Inputs.Notes},
		{Name: "WORKORDER_INPUT_FORMAT", Value: a.WorkOrder.Inputs.InputFormat},
		{Name: "WORKORDER_OUTPUT_FORMAT", Value: a.WorkOrder.Inputs.OutputFormat},
		// MemoryRef is a reference only (ADR-4) — the actor re-grounds itself.
		{Name: "MEMORY_OWNER", Value: a.MemoryRef.Owner},
		{Name: "MEMORY_PERSONA", Value: a.MemoryRef.Persona},
	}
	// The per-repo PAT is projected from a Secret, never a literal (contract §3.1).
	if a.TokenSecretRef != nil {
		env = append(env, corev1.EnvVar{Name: "GITHUB_TOKEN", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: a.TokenSecretRef.Name},
				Key:                  a.TokenSecretRef.Key,
			},
		}})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: r.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "substrate-actor",
				"app.kubernetes.io/component": a.ActorTemplate,
				"app.kubernetes.io/part-of":   "contentremixer",
				"contentremixer.io/actor-id":  a.ID,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"contentremixer.io/actor-id": a.ID},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: r.ServiceAccount,
					NodeSelector:       a.WorkerSelector,
					SecurityContext:    hardenedPodSecurity(),
					Containers: []corev1.Container{{
						Name:            "actor",
						Image:           a.Image,
						Env:             env,
						SecurityContext: hardenedContainerSecurity(),
					}},
				},
			},
		},
	}
	if err := r.Client.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Deterministic name → a retried spawn is idempotent.
			return name, nil
		}
		return "", fmt.Errorf("substrate: create job %s: %w", name, err)
	}
	return name, nil
}

func (r *K8sRuntime) Observe(ctx context.Context, a *Actor) (ActorState, Phase, *Result, string, error) {
	if a.JobName == "" {
		return StateUnknown, a.Phase, nil, "", nil
	}
	var job batchv1.Job
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: a.JobName}, &job)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Job GC'd or torn down — keep the record's last known state.
			return a.State, a.Phase, a.Result, a.Error, nil
		}
		return StateUnknown, a.Phase, nil, "", fmt.Errorf("substrate: get job %s: %w", a.JobName, err)
	}
	switch {
	case job.Status.Succeeded > 0:
		return StateSucceeded, PhaseDone, a.Result, "", nil
	case job.Status.Failed > 0:
		return StateFailed, PhaseDone, nil, "actor job failed", nil
	case job.Status.Active > 0:
		return StateRunning, PhaseWorking, nil, "", nil
	default:
		return StateRunning, PhaseSpawning, nil, "", nil
	}
}

func (r *K8sRuntime) Teardown(ctx context.Context, a *Actor) error {
	if a.JobName == "" {
		return nil
	}
	policy := metav1.DeletePropagationBackground
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: a.JobName, Namespace: r.Namespace}}
	err := r.Client.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &policy})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("substrate: delete job %s: %w", a.JobName, err)
	}
	return nil
}

// jobNameFor derives a DNS-1123 Job name from an actor id. Deterministic so a
// retried spawn collides (AlreadyExists) rather than duplicating.
func jobNameFor(actorID string) string {
	id := strings.ToLower(strings.ReplaceAll(actorID, "_", "-"))
	id = strings.TrimLeft(id, "-")
	if len(id) > 20 {
		id = id[:20]
	}
	return "actor-" + id
}

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

// Package substrate is the in-repo substrate control plane (ISI-7507, board
// fork option B). It exposes the HTTP actor-lifecycle contract that the
// ISI-7397 `substrate-actor` Dispatcher strategy targets — spawn / status /
// suspend / resume / terminate — over a durable store, and runs each actor as
// a Kubernetes Job of the per-template actor image (e.g. the ISI-7395
// media-actor). It is the deployable backend the agentgateway front door in
// deploy/base/substrate fronts.
//
// The wire contract here is the authority documented in
// docs/substrate/control-plane-contract.md §3; the Dispatcher's SubstrateStrategy
// client (ISI-7397) must match it verbatim.
//
// ADR-4 split (ISI-7390 correction): suspend/resume here operate on the actor's
// *execution* (its Job), never on memory. The AgentMesh memory flush/re-ground
// is the actor's own op against the external style layer; this control plane
// only carries a MemoryRef so a resumed actor can re-ground itself. The
// walking-skeleton suspend persists the WorkOrder and tears the Job down;
// resume recreates it (a true CRIU/checkpoint snapshot is a v2 — see runtime.go).
package substrate

import "time"

// ActorState is the coarse lifecycle state reported to callers (contract §3.2).
type ActorState string

const (
	StateRunning   ActorState = "running"
	StateSuspended ActorState = "suspended"
	StateSucceeded ActorState = "succeeded"
	StateFailed    ActorState = "failed"
	StateUnknown   ActorState = "unknown"
)

// Phase is the finer-grained progress marker (contract §3.2). The control plane
// owns spawning/suspended; the actor advances working→flushing / regrounding→done
// via its own status (the skeleton derives it from the Job state).
type Phase string

const (
	PhaseSpawning    Phase = "spawning"
	PhaseWorking     Phase = "working"
	PhaseFlushing    Phase = "flushing"
	PhaseSuspended   Phase = "suspended"
	PhaseRegrounding Phase = "regrounding"
	PhaseDone        Phase = "done"
)

// SecretRef projects a Kubernetes Secret key into the actor pod. The per-repo
// PAT rides as a SecretRef, never as a literal over the wire (contract §3.1).
type SecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// MemoryRef points the actor at the shared external style layer (ADR-4). It is
// a reference only — the control plane never reads or snapshots the memory.
type MemoryRef struct {
	Owner   string `json:"owner,omitempty"`
	Persona string `json:"persona,omitempty"`
}

// Inputs mirrors dispatch.Inputs minus the secret Token (which rides as a
// TokenSecretRef on the spawn request instead).
type Inputs struct {
	SourcePath   string `json:"sourcePath,omitempty"`
	Notes        string `json:"notes,omitempty"`
	InputFormat  string `json:"inputFormat,omitempty"`
	OutputFormat string `json:"outputFormat,omitempty"`
}

// WorkOrder is the runtime-agnostic unit (dispatch.WorkOrder), carried on spawn.
type WorkOrder struct {
	Repo        string `json:"repo"`
	IssueNumber int    `json:"issueNumber"`
	Inputs      Inputs `json:"inputs"`
}

// SpawnRequest is the POST /v1/actors body (contract §3.1).
type SpawnRequest struct {
	ActorTemplate  string            `json:"actorTemplate"`
	Image          string            `json:"image"`
	Skill          string            `json:"skill"`
	WorkOrder      WorkOrder         `json:"workOrder"`
	MemoryRef      MemoryRef         `json:"memoryRef,omitempty"`
	WorkerSelector map[string]string `json:"workerSelector,omitempty"`
	TokenSecretRef *SecretRef        `json:"tokenSecretRef,omitempty"`
}

// Result is the terminal payload an actor reports (contract §3.2).
type Result struct {
	PRURL string `json:"prUrl,omitempty"`
}

// Actor is the persisted record (durable store) for one actor across its
// lifecycle. The *Spec fields are set at spawn and are immutable; the status
// fields are updated as the Job progresses and across suspend/resume.
type Actor struct {
	ID             string            `json:"id"`
	ActorTemplate  string            `json:"actorTemplate"`
	Image          string            `json:"image"`
	Skill          string            `json:"skill"`
	WorkOrder      WorkOrder         `json:"workOrder"`
	MemoryRef      MemoryRef         `json:"memoryRef"`
	WorkerSelector map[string]string `json:"workerSelector,omitempty"`
	TokenSecretRef *SecretRef        `json:"tokenSecretRef,omitempty"`

	State      ActorState `json:"state"`
	Phase      Phase      `json:"phase"`
	JobName    string     `json:"jobName,omitempty"`
	SnapshotID string     `json:"snapshotId,omitempty"`
	Result     *Result    `json:"result,omitempty"`
	Error      string     `json:"error,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// --- response envelopes (contract §3) ---

// SpawnResponse is the 201 body for POST /v1/actors.
type SpawnResponse struct {
	ActorID string     `json:"actorId"`
	State   ActorState `json:"state"`
}

// StatusResponse is the GET /v1/actors/{id} body.
type StatusResponse struct {
	ActorID string     `json:"actorId"`
	State   ActorState `json:"state"`
	Phase   Phase      `json:"phase"`
	Result  *Result    `json:"result,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// SuspendResponse is the POST /v1/actors/{id}/suspend body.
type SuspendResponse struct {
	ActorID    string     `json:"actorId"`
	State      ActorState `json:"state"`
	SnapshotID string     `json:"snapshotId"`
}

// ResumeRequest is the POST /v1/actors/{id}/resume body (re-ground source).
type ResumeRequest struct {
	MemoryRef MemoryRef `json:"memoryRef,omitempty"`
}

// ResumeResponse is the POST /v1/actors/{id}/resume body.
type ResumeResponse struct {
	ActorID string     `json:"actorId"`
	State   ActorState `json:"state"`
}

// errorBody is the uniform 4xx/5xx envelope (contract §3).
type errorBody struct {
	Error string `json:"error"`
}

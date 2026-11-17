package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SubstrateStrategy runs the "media" actor template on a snapshot/resume
// substrate actor (ISI-7397, parent ISI-7389 ADR-1 P1/ADR-4). It replaces the
// k8s-Job MediaStrategy behind the same dispatch.Strategy interface, so flipping
// media from a Job to a substrate actor is a wiring change in main() with zero
// control-plane diff — the operator never branches on which backend ran the
// order (ISI-7393 seam held).
//
// Run spawns a media actor on the substrate control plane (contract
// docs/substrate/control-plane-contract.md §3), then polls its status as the
// wakeup probe until the actor reaches a terminal state — parity with the Job
// path's completion watch.
//
// ADR-4 invariants (ISI-7390 correction) are enforced by the substrate + the
// actor, never by this client: the actor flushes its mesh memory to the external
// style layer *before* the substrate snapshots execution state on suspend, and
// re-grounds from the carried MemoryRef on resume; the snapshot carries no memory
// bytes. This Strategy's role is to pass MemoryRef through as a *reference* (never
// the memory, never the PAT literal) and to await terminal status.
type SubstrateStrategy struct {
	// Client is the substrate control-plane client (agentgateway front door).
	// Required.
	Client *SubstrateClient
	// Image is the pinned media-actor workload image the actor runs
	// (ghcr.io/contentremixer/media-actor@sha256:…). Required.
	Image string
	// WorkerSelector optionally pins actors to media-class workers (node selector
	// labels), passed through verbatim on spawn.
	WorkerSelector map[string]string
	// TokenSecretRef projects the per-repo PAT into the actor pod. The token never
	// crosses the HTTP boundary as a literal (contract §3.1): the spawn carries
	// this {name,key} Secret reference, resolved the same way the Job launcher
	// resolves it today. Optional (nil => no token projected; smoke paths).
	TokenSecretRef *SecretRef
	// PollInterval is the wakeup-probe cadence. Defaults to 5s.
	PollInterval time.Duration
}

// SecretRef names a Kubernetes Secret key projected into the actor pod (§3.1).
type SecretRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// Runtime reports the substrate-actor backend.
func (SubstrateStrategy) Runtime() Runtime { return RuntimeSubstrate }

// Run spawns a media actor for wo and awaits its terminal status. A failed
// actor's error is surfaced verbatim; a succeeded actor returns nil. Context
// cancellation abandons the wait (the substrate reaps the actor on its own TTL).
func (s SubstrateStrategy) Run(ctx context.Context, wo WorkOrder) error {
	if s.Client == nil || strings.TrimSpace(s.Client.BaseURL) == "" {
		return fmt.Errorf("substrate: no control-plane client configured (skill %q, %s#%d)",
			wo.Skill, wo.Repo, wo.IssueNumber)
	}
	if strings.TrimSpace(s.Image) == "" {
		return fmt.Errorf("substrate: no media-actor image configured (skill %q, %s#%d)",
			wo.Skill, wo.Repo, wo.IssueNumber)
	}

	req := spawnRequest{
		ActorTemplate: wo.ActorTemplate,
		Image:         s.Image,
		Skill:         wo.Skill,
		WorkOrder: workOrder{
			Repo:        wo.Repo,
			IssueNumber: wo.IssueNumber,
			Inputs: inputs{
				SourcePath:   wo.Inputs.SourcePath,
				Notes:        wo.Inputs.Notes,
				InputFormat:  wo.Inputs.InputFormat,
				OutputFormat: wo.Inputs.OutputFormat,
			},
		},
		MemoryRef:      memoryRef{Owner: wo.MemoryRef.Owner.Subject, Persona: wo.MemoryRef.Persona},
		WorkerSelector: s.WorkerSelector,
		TokenSecretRef: s.TokenSecretRef,
	}

	spawned, err := s.Client.Spawn(ctx, req)
	if err != nil {
		return fmt.Errorf("substrate: spawn media actor (skill %q, %s#%d): %w",
			wo.Skill, wo.Repo, wo.IssueNumber, err)
	}

	interval := s.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		st, err := s.Client.Status(ctx, spawned.ActorID)
		if err != nil {
			return fmt.Errorf("substrate: status actor %s (skill %q, %s#%d): %w",
				spawned.ActorID, wo.Skill, wo.Repo, wo.IssueNumber, err)
		}
		switch st.State {
		case stateSucceeded:
			return nil
		case stateFailed:
			msg := st.Error
			if msg == "" {
				msg = "actor reported failed with no detail"
			}
			return fmt.Errorf("substrate: media actor %s failed (skill %q, %s#%d): %s",
				spawned.ActorID, wo.Skill, wo.Repo, wo.IssueNumber, msg)
		}
		// running | suspended | unknown => keep probing. The substrate drives
		// suspend/resume itself; the Strategy only awaits a terminal state.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// --- substrate control-plane client (contract §3, verbatim) ---

// SubstrateClient calls the substrate control-plane HTTP contract
// (docs/substrate/control-plane-contract.md §3). Its request/response types must
// match §3 and the substrate server (app/internal/substrate) exactly — the
// substrate_test.go fixtures guard the drift.
type SubstrateClient struct {
	// BaseURL is the agentgateway front door, e.g.
	// "http://substrate.contentremixer.svc:8080". Required, no trailing slash.
	BaseURL string
	// HTTP is the underlying client; defaults to a 30s-timeout client when nil.
	// Long waits are bounded by the Run context, not this timeout.
	HTTP *http.Client
}

// NewSubstrateClient builds a client for the given base URL.
func NewSubstrateClient(baseURL string) *SubstrateClient {
	return &SubstrateClient{BaseURL: strings.TrimRight(baseURL, "/")}
}

func (c *SubstrateClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Spawn POSTs /v1/actors (§3.1).
func (c *SubstrateClient) Spawn(ctx context.Context, req spawnRequest) (spawnResponse, error) {
	var out spawnResponse
	err := c.do(ctx, http.MethodPost, "/v1/actors", req, &out)
	return out, err
}

// Status GETs /v1/actors/{id} — the wakeup probe (§3.2).
func (c *SubstrateClient) Status(ctx context.Context, actorID string) (statusResponse, error) {
	var out statusResponse
	err := c.do(ctx, http.MethodGet, "/v1/actors/"+actorID, nil, &out)
	return out, err
}

// Suspend POSTs /v1/actors/{id}/suspend — snapshots execution state only, after
// the actor has flushed mesh memory (§3.3, ADR-4 flush-before-suspend).
func (c *SubstrateClient) Suspend(ctx context.Context, actorID string) (suspendResponse, error) {
	var out suspendResponse
	err := c.do(ctx, http.MethodPost, "/v1/actors/"+actorID+"/suspend", nil, &out)
	return out, err
}

// Resume POSTs /v1/actors/{id}/resume with the re-ground source (§3.4). The
// substrate passes memoryRef to the actor; it never reads it.
func (c *SubstrateClient) Resume(ctx context.Context, actorID string, req resumeRequest) (resumeResponse, error) {
	var out resumeResponse
	err := c.do(ctx, http.MethodPost, "/v1/actors/"+actorID+"/resume", req, &out)
	return out, err
}

// Terminate DELETEs /v1/actors/{id} — reaps actor + snapshot, idempotent (§3.5).
func (c *SubstrateClient) Terminate(ctx context.Context, actorID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/actors/"+actorID, nil, nil)
}

// do issues one request and decodes the JSON response into out (nil out => no
// body expected, e.g. 204). Non-2xx is surfaced via the §3 {"error":…} envelope.
func (c *SubstrateClient) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("substrate %s: %s", resp.Status, readError(resp.Body))
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}

// readError extracts a human message from a non-2xx body, preferring the uniform
// {"error":…} envelope (contract §3) and falling back to the raw trimmed body.
func readError(rc io.ReadCloser) string {
	raw, _ := io.ReadAll(io.LimitReader(rc, 1<<16))
	var env struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error != "" {
		return env.Error
	}
	if msg := strings.TrimSpace(string(raw)); msg != "" {
		return msg
	}
	return "unknown error"
}

// --- wire types (contract §3; mirror app/internal/substrate) ---

type actorState string

const (
	stateRunning   actorState = "running"
	stateSuspended actorState = "suspended"
	stateSucceeded actorState = "succeeded"
	stateFailed    actorState = "failed"
	stateUnknown   actorState = "unknown"
)

type memoryRef struct {
	Owner   string `json:"owner,omitempty"`
	Persona string `json:"persona,omitempty"`
}

type inputs struct {
	SourcePath   string `json:"sourcePath,omitempty"`
	Notes        string `json:"notes,omitempty"`
	InputFormat  string `json:"inputFormat,omitempty"`
	OutputFormat string `json:"outputFormat,omitempty"`
}

type workOrder struct {
	Repo        string `json:"repo"`
	IssueNumber int    `json:"issueNumber"`
	Inputs      inputs `json:"inputs"`
}

type spawnRequest struct {
	ActorTemplate  string            `json:"actorTemplate"`
	Image          string            `json:"image"`
	Skill          string            `json:"skill"`
	WorkOrder      workOrder         `json:"workOrder"`
	MemoryRef      memoryRef         `json:"memoryRef,omitempty"`
	WorkerSelector map[string]string `json:"workerSelector,omitempty"`
	TokenSecretRef *SecretRef        `json:"tokenSecretRef,omitempty"`
}

type spawnResponse struct {
	ActorID string     `json:"actorId"`
	State   actorState `json:"state"`
}

type result struct {
	PRURL string `json:"prUrl,omitempty"`
}

type statusResponse struct {
	ActorID string     `json:"actorId"`
	State   actorState `json:"state"`
	Phase   string     `json:"phase"`
	Result  *result    `json:"result,omitempty"`
	Error   string     `json:"error,omitempty"`
}

type suspendResponse struct {
	ActorID    string     `json:"actorId"`
	State      actorState `json:"state"`
	SnapshotID string     `json:"snapshotId"`
}

type resumeRequest struct {
	MemoryRef memoryRef `json:"memoryRef,omitempty"`
}

type resumeResponse struct {
	ActorID string     `json:"actorId"`
	State   actorState `json:"state"`
}

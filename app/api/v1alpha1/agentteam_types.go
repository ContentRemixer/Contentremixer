package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentNode binds a stable node id in the DAG to a specific Agent CR.
type AgentNode struct {
	// ID is the node identifier, unique within the team. Referenced by Entry and
	// by Edges. Also the orchestrator anchor-owner id.
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`

	// AgentRef is the metadata.name of the Agent this node runs. MUST resolve to
	// an existing Agent (webhook + engine hard error otherwise — fail closed).
	// +kubebuilder:validation:MinLength=1
	AgentRef string `json:"agentRef"`
}

// AgentEdge is a directed hand-off: the From node must complete before To runs.
type AgentEdge struct {
	// +kubebuilder:validation:MinLength=1
	From string `json:"from"`
	// +kubebuilder:validation:MinLength=1
	To string `json:"to"`
}

// AgentTeamSpec is one orchestration DAG: an entry node, a set of nodes each
// bound to an Agent, and the hand-off edges. This is the data form of the
// hardcoded orchestrator.DefaultEngine() wiring on main. Admin-owned.
type AgentTeamSpec struct {
	// Entry is the node id that starts the run (the planner/architect). MUST be a
	// declared node id (webhook). The engine runs it first; its outline is adopted
	// by downstream nodes (mirrors orchestrator: architect produces the Outline).
	// +kubebuilder:validation:MinLength=1
	Entry string `json:"entry"`

	// Nodes are the specialists in this team. Node ids must be unique.
	// +kubebuilder:validation:MinItems=1
	// +listType=atomic
	Nodes []AgentNode `json:"nodes"`

	// Edges are the directed hand-offs. Together with Nodes they MUST form a DAG
	// rooted at Entry: no cycles, every endpoint a declared node, every non-entry
	// node reachable from Entry (webhook + engine, ISI-7800 §4). Empty edges = a
	// single-node team (the linear-writer case).
	// +optional
	// +listType=atomic
	Edges []AgentEdge `json:"edges,omitempty"`

	// Default marks the team the engine uses when a run names no team. AT MOST ONE
	// AgentTeam may set this true (webhook). Zero is allowed (degraded; engine
	// fails closed if a run arrives with no resolvable team).
	// +optional
	Default bool `json:"default,omitempty"`

	// Owner — platform-owned (empty) in Phase 1; present for registry uniformity.
	// +optional
	Owner OwnerRef `json:"owner,omitempty"`
}

// AgentTeamStatus defines the observed state of an AgentTeam.
type AgentTeamStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" listType:"map"`

	// NodeCount mirrors len(spec.nodes) for the list print column.
	// +optional
	NodeCount int `json:"nodeCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=agentteam;team
// +kubebuilder:printcolumn:name="Entry",type=string,JSONPath=`.spec.entry`
// +kubebuilder:printcolumn:name="Nodes",type=integer,JSONPath=`.status.nodeCount`
// +kubebuilder:printcolumn:name="Default",type=boolean,JSONPath=`.spec.default`

// AgentTeam is the Schema for the agentteams API: one orchestration DAG.
type AgentTeam struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTeamSpec   `json:"spec,omitempty"`
	Status AgentTeamStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTeamList contains a list of AgentTeam.
type AgentTeamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTeam `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTeam{}, &AgentTeamList{})
}

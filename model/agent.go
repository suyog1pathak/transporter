package model

import (
	"time"
)

// AgentStatus represents the current status of an agent
type AgentStatus string

const (
	AgentStatusConnected    AgentStatus = "connected"
	AgentStatusDisconnected AgentStatus = "disconnected"
	AgentStatusUnhealthy    AgentStatus = "unhealthy"
)

// Agent represents a data plane agent running in a Kubernetes cluster
type Agent struct {
	// Core Identity
	ID   string `json:"id"`   // Unique agent ID (should be stable across restarts)
	Name string `json:"name"` // Human-friendly name

	// Cluster Information
	ClusterName     string `json:"cluster_name"`               // Name of the K8s cluster
	ClusterProvider string `json:"cluster_provider"`           // eks, gke, aks, etc.
	Region          string `json:"region"`                     // Cloud region
	Version         string `json:"version"`                    // Agent version
	Labels          map[string]string `json:"labels,omitempty"` // Custom labels for filtering

	// Connection State
	ConnectionID  string      `json:"connection_id"`  // WebSocket connection ID
	Status        AgentStatus `json:"status"`         // Current agent status
	LastHeartbeat time.Time   `json:"last_heartbeat"` // Last heartbeat timestamp
	ConnectedAt   time.Time   `json:"connected_at"`   // When agent connected
	DisconnectedAt *time.Time `json:"disconnected_at,omitempty"` // When agent disconnected (nil if connected)

	// Capabilities
	Capabilities []string `json:"capabilities"` // Supported operations (k8s_crud, script_exec, policy)

	// Metadata
	Hostname    string            `json:"hostname,omitempty"`     // Agent pod hostname
	Namespace   string            `json:"namespace,omitempty"`    // K8s namespace where agent runs
	Metadata    map[string]string `json:"metadata,omitempty"`     // Additional metadata
}

// AgentRegistration is sent by an agent when it first connects to the control plane
type AgentRegistration struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	ClusterName     string            `json:"cluster_name"`
	ClusterProvider string            `json:"cluster_provider"`
	Region          string            `json:"region"`
	Version         string            `json:"version"`
	Labels          map[string]string `json:"labels,omitempty"`
	Capabilities    []string          `json:"capabilities"`
	Hostname        string            `json:"hostname,omitempty"`
	Namespace       string            `json:"namespace,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// ToAgent converts an AgentRegistration to an Agent with initial connection state
func (ar *AgentRegistration) ToAgent(connectionID string) *Agent {
	now := time.Now()
	return &Agent{
		ID:              ar.ID,
		Name:            ar.Name,
		ClusterName:     ar.ClusterName,
		ClusterProvider: ar.ClusterProvider,
		Region:          ar.Region,
		Version:         ar.Version,
		Labels:          ar.Labels,
		ConnectionID:    connectionID,
		Status:          AgentStatusConnected,
		LastHeartbeat:   now,
		ConnectedAt:     now,
		DisconnectedAt:  nil,
		Capabilities:    ar.Capabilities,
		Hostname:        ar.Hostname,
		Namespace:       ar.Namespace,
		Metadata:        ar.Metadata,
	}
}

// Validate performs basic validation on agent registration
func (ar *AgentRegistration) Validate() error {
	if ar.ID == "" {
		return ErrMissingAgentID
	}
	if ar.Name == "" {
		return ErrMissingAgentName
	}
	if ar.ClusterName == "" {
		return ErrMissingClusterName
	}
	if ar.Version == "" {
		return ErrMissingAgentVersion
	}
	if len(ar.Capabilities) == 0 {
		return ErrMissingCapabilities
	}
	return nil
}

// IsHealthy checks if the agent is healthy based on last heartbeat
func (a *Agent) IsHealthy(heartbeatTimeout time.Duration) bool {
	if a.Status != AgentStatusConnected {
		return false
	}
	return time.Since(a.LastHeartbeat) <= heartbeatTimeout
}

// UpdateHeartbeat updates the agent's last heartbeat timestamp
func (a *Agent) UpdateHeartbeat() {
	a.LastHeartbeat = time.Now()
	if a.Status == AgentStatusUnhealthy {
		a.Status = AgentStatusConnected
	}
}

// MarkDisconnected marks the agent as disconnected
func (a *Agent) MarkDisconnected() {
	now := time.Now()
	a.Status = AgentStatusDisconnected
	a.DisconnectedAt = &now
}

// MarkUnhealthy marks the agent as unhealthy (connected but not responding)
func (a *Agent) MarkUnhealthy() {
	a.Status = AgentStatusUnhealthy
}

// HasCapability checks if the agent supports a specific capability
func (a *Agent) HasCapability(capability string) bool {
	for _, cap := range a.Capabilities {
		if cap == capability {
			return true
		}
	}
	return false
}

// Heartbeat represents a heartbeat message from an agent
type Heartbeat struct {
	AgentID   string                 `json:"agent_id"`
	Timestamp time.Time              `json:"timestamp"`
	Metrics   map[string]interface{} `json:"metrics,omitempty"` // Optional health metrics
}

// Custom errors for agent validation
var (
	ErrMissingAgentID       = &AgentError{Code: "MISSING_AGENT_ID", Message: "agent ID is required"}
	ErrMissingAgentName     = &AgentError{Code: "MISSING_AGENT_NAME", Message: "agent name is required"}
	ErrMissingClusterName   = &AgentError{Code: "MISSING_CLUSTER_NAME", Message: "cluster name is required"}
	ErrMissingAgentVersion  = &AgentError{Code: "MISSING_AGENT_VERSION", Message: "agent version is required"}
	ErrMissingCapabilities  = &AgentError{Code: "MISSING_CAPABILITIES", Message: "at least one capability is required"}
	ErrAgentNotFound        = &AgentError{Code: "AGENT_NOT_FOUND", Message: "agent not found"}
	ErrAgentAlreadyExists   = &AgentError{Code: "AGENT_ALREADY_EXISTS", Message: "agent already exists"}
)

// AgentError represents an agent-related error
type AgentError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *AgentError) Error() string {
	return e.Message
}

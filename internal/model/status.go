package model

import (
	"time"
)

// ExecutionState represents the current state of event execution
type ExecutionState string

const (
	StateCreated    ExecutionState = "created"
	StateQueued     ExecutionState = "queued"
	StateAssigned   ExecutionState = "assigned"
	StateInProgress ExecutionState = "in_progress"
	StateCompleted  ExecutionState = "completed"
	StateFailed     ExecutionState = "failed"
	StateExpired    ExecutionState = "expired"
)

// ExecutionPhase represents granular execution phases within InProgress state
type ExecutionPhase string

const (
	PhaseReceived   ExecutionPhase = "received"   // Agent received the event
	PhaseValidating ExecutionPhase = "validating" // Validating manifests/scripts
	PhaseApplying   ExecutionPhase = "applying"   // Applying changes to cluster
	PhaseVerifying  ExecutionPhase = "verifying"  // Verifying the changes
	PhaseCompleted  ExecutionPhase = "completed"  // Execution completed
	PhaseFailed     ExecutionPhase = "failed"     // Execution failed
)

// EventStatus represents the execution status of an event
type EventStatus struct {
	EventID      string         `json:"event_id"`
	AgentID      string         `json:"agent_id"`
	State        ExecutionState `json:"state"`
	Phase        ExecutionPhase `json:"phase,omitempty"`         // Current execution phase
	Message      string         `json:"message,omitempty"`       // Human-readable status message
	UpdatedAt    time.Time      `json:"updated_at"`
	ExecutionLog []LogEntry     `json:"execution_log,omitempty"` // Detailed execution log
	Result       *EventResult   `json:"result,omitempty"`        // Final result (populated when completed/failed)
}

// LogEntry represents a single log entry during event execution
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Phase     ExecutionPhase         `json:"phase"`
	Level     LogLevel               `json:"level"` // info, warning, error
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"` // Additional structured data
}

// LogLevel represents the severity of a log entry
type LogLevel string

const (
	LogLevelInfo    LogLevel = "info"
	LogLevelWarning LogLevel = "warning"
	LogLevelError   LogLevel = "error"
	LogLevelDebug   LogLevel = "debug"
)

// EventResult contains the final outcome of event execution
type EventResult struct {
	Success        bool             `json:"success"`
	ResourceStatus []ResourceStatus `json:"resource_status,omitempty"` // Status of individual resources
	ErrorMessage   string           `json:"error_message,omitempty"`
	CompletedAt    time.Time        `json:"completed_at"`
	Duration       time.Duration    `json:"duration"` // Total execution time
}

// ResourceStatus represents the status of a single Kubernetes resource
type ResourceStatus struct {
	Kind       string `json:"kind"`                  // Resource kind (Namespace, Deployment, etc.)
	Name       string `json:"name"`                  // Resource name
	Namespace  string `json:"namespace,omitempty"`   // Resource namespace (if applicable)
	APIVersion string `json:"api_version,omitempty"` // API version
	Status     string `json:"status"`                // created, updated, failed, unchanged
	Message    string `json:"message,omitempty"`     // Additional details
}

// NewEventStatus creates a new event status with initial state
func NewEventStatus(eventID, agentID string) *EventStatus {
	return &EventStatus{
		EventID:      eventID,
		AgentID:      agentID,
		State:        StateAssigned,
		Phase:        PhaseReceived,
		Message:      "Event assigned to agent",
		UpdatedAt:    time.Now(),
		ExecutionLog: []LogEntry{},
	}
}

// UpdateState updates the event state and logs the change
func (es *EventStatus) UpdateState(state ExecutionState, message string) {
	es.State = state
	es.Message = message
	es.UpdatedAt = time.Now()
	es.AddLog(LogLevelInfo, "", message, nil)
}

// UpdatePhase updates the execution phase and logs the change
func (es *EventStatus) UpdatePhase(phase ExecutionPhase, message string) {
	es.Phase = phase
	es.Message = message
	es.UpdatedAt = time.Now()
	es.AddLog(LogLevelInfo, phase, message, nil)
}

// AddLog adds a log entry to the execution log
func (es *EventStatus) AddLog(level LogLevel, phase ExecutionPhase, message string, details map[string]interface{}) {
	entry := LogEntry{
		Timestamp: time.Now(),
		Phase:     phase,
		Level:     level,
		Message:   message,
		Details:   details,
	}
	es.ExecutionLog = append(es.ExecutionLog, entry)
}

// MarkCompleted marks the event as completed with results
func (es *EventStatus) MarkCompleted(result *EventResult) {
	es.State = StateCompleted
	es.Phase = PhaseCompleted
	es.Message = "Event execution completed successfully"
	es.Result = result
	es.UpdatedAt = time.Now()
	es.AddLog(LogLevelInfo, PhaseCompleted, "Execution completed", nil)
}

// MarkFailed marks the event as failed with error details
func (es *EventStatus) MarkFailed(errorMessage string) {
	es.State = StateFailed
	es.Phase = PhaseFailed
	es.Message = errorMessage
	es.Result = &EventResult{
		Success:      false,
		ErrorMessage: errorMessage,
		CompletedAt:  time.Now(),
	}
	es.UpdatedAt = time.Now()
	es.AddLog(LogLevelError, PhaseFailed, errorMessage, nil)
}

// MarkExpired marks the event as expired
func (es *EventStatus) MarkExpired() {
	es.State = StateExpired
	es.Message = "Event expired (TTL exceeded)"
	es.UpdatedAt = time.Now()
	es.AddLog(LogLevelWarning, "", "Event expired", nil)
}

// IsTerminal returns true if the event is in a terminal state
func (es *EventStatus) IsTerminal() bool {
	return es.State == StateCompleted || es.State == StateFailed || es.State == StateExpired
}

// StatusUpdate is sent by an agent to update event status
type StatusUpdate struct {
	EventID   string                 `json:"event_id"`
	AgentID   string                 `json:"agent_id"`
	State     ExecutionState         `json:"state,omitempty"`
	Phase     ExecutionPhase         `json:"phase,omitempty"`
	Message   string                 `json:"message,omitempty"`
	LogLevel  LogLevel               `json:"log_level,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Result    *EventResult           `json:"result,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// Custom errors for status operations
var (
	ErrStatusNotFound         = &StatusError{Code: "STATUS_NOT_FOUND", Message: "event status not found"}
	ErrInvalidStateTransition = &StatusError{Code: "INVALID_STATE_TRANSITION", Message: "invalid state transition"}
)

// StatusError represents a status-related error
type StatusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *StatusError) Error() string {
	return e.Message
}

package model

import (
	"time"

	"github.com/google/uuid"
)

// EventType defines the type of operation the event represents
type EventType string

const (
	EventTypeK8sResource EventType = "k8s_resource"
	EventTypeScript      EventType = "script"
	EventTypePolicy      EventType = "policy"
)

// Event represents a task to be executed by a data plane agent
type Event struct {
	// Core Identity
	ID          string    `json:"id"`           // Unique event ID (UUID)
	Type        EventType `json:"type"`         // Type of event (k8s_resource, script, policy)
	TargetAgent string    `json:"target_agent"` // Explicit agent ID to execute this event

	// Payload
	Payload EventPayload `json:"payload"`

	// Metadata
	CreatedAt time.Time         `json:"created_at"`
	CreatedBy string            `json:"created_by"`          // User/system that created the event
	TTL       time.Duration     `json:"ttl"`                 // Time-to-live for event expiration
	Priority  int               `json:"priority"`            // Priority for future use (higher = more urgent)
	Labels    map[string]string `json:"labels,omitempty"`    // Optional labels for filtering/grouping
}

// EventPayload contains the actual data/instructions for the event
type EventPayload struct {
	// K8s Resource Payload (for EventTypeK8sResource)
	Manifests []string `json:"manifests,omitempty"` // Raw K8s YAML manifests to apply

	// Script Payload (for EventTypeScript)
	Script string   `json:"script,omitempty"` // Script content to execute
	Args   []string `json:"args,omitempty"`   // Arguments for script execution

	// Policy Payload (for EventTypePolicy)
	PolicyRules []PolicyRule `json:"policy_rules,omitempty"` // Policy validation rules
}

// PolicyRule represents a validation rule to enforce
type PolicyRule struct {
	Name        string            `json:"name"`                  // Rule name/identifier
	Type        string            `json:"type"`                  // Rule type (e.g., "required_label", "resource_limit")
	Parameters  map[string]string `json:"parameters,omitempty"`  // Rule-specific parameters
	Severity    string            `json:"severity"`              // "error", "warning", "info"
	Description string            `json:"description,omitempty"` // Human-readable description
}

// NewEvent creates a new event with sensible defaults
func NewEvent(eventType EventType, targetAgent string, payload EventPayload, createdBy string) *Event {
	return &Event{
		ID:          uuid.New().String(),
		Type:        eventType,
		TargetAgent: targetAgent,
		Payload:     payload,
		CreatedAt:   time.Now(),
		CreatedBy:   createdBy,
		TTL:         24 * time.Hour, // Default 24 hour TTL
		Priority:    0,              // Default priority
		Labels:      make(map[string]string),
	}
}

// IsExpired checks if the event has exceeded its TTL
func (e *Event) IsExpired() bool {
	return time.Since(e.CreatedAt) > e.TTL
}

// Validate performs basic validation on the event
func (e *Event) Validate() error {
	if e.ID == "" {
		return ErrMissingEventID
	}
	if e.TargetAgent == "" {
		return ErrMissingTargetAgent
	}
	if e.Type == "" {
		return ErrMissingEventType
	}

	// Validate payload based on event type
	switch e.Type {
	case EventTypeK8sResource:
		if len(e.Payload.Manifests) == 0 {
			return ErrEmptyManifests
		}
	case EventTypeScript:
		if e.Payload.Script == "" {
			return ErrEmptyScript
		}
	case EventTypePolicy:
		if len(e.Payload.PolicyRules) == 0 {
			return ErrEmptyPolicyRules
		}
	default:
		return ErrUnknownEventType
	}

	return nil
}

// Custom errors for event validation
var (
	ErrMissingEventID     = &EventError{Code: "MISSING_EVENT_ID", Message: "event ID is required"}
	ErrMissingTargetAgent = &EventError{Code: "MISSING_TARGET_AGENT", Message: "target agent is required"}
	ErrMissingEventType   = &EventError{Code: "MISSING_EVENT_TYPE", Message: "event type is required"}
	ErrEmptyManifests     = &EventError{Code: "EMPTY_MANIFESTS", Message: "k8s_resource event must have at least one manifest"}
	ErrEmptyScript        = &EventError{Code: "EMPTY_SCRIPT", Message: "script event must have script content"}
	ErrEmptyPolicyRules   = &EventError{Code: "EMPTY_POLICY_RULES", Message: "policy event must have at least one rule"}
	ErrUnknownEventType   = &EventError{Code: "UNKNOWN_EVENT_TYPE", Message: "unknown event type"}
)

// EventError represents an event-related error
type EventError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *EventError) Error() string {
	return e.Message
}

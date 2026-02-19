package router

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/suyog1pathak/transporter/internal/model"
	"github.com/suyog1pathak/transporter/pkg/registry"
)

// EventMessage represents a message containing an event to be sent to an agent
type EventMessage struct {
	Type    string       `json:"type"` // "event", "status_request", "heartbeat_request", etc.
	Event   *model.Event `json:"event,omitempty"`
	EventID string       `json:"event_id,omitempty"`
}

// PendingEvent represents an event waiting for an agent to reconnect
type PendingEvent struct {
	Event     *model.Event
	QueuedAt  time.Time
	Retries   int
	ExpiresAt time.Time
}

// EventRouter handles routing events to agents
type EventRouter struct {
	registry      *registry.AgentRegistry
	pendingEvents map[string][]*PendingEvent // agentID -> pending events
	mu            sync.RWMutex
	maxRetries    int
	retryInterval time.Duration

	// Callbacks
	onEventRouted func(*model.Event, string) // event, agentID
	onEventQueued func(*model.Event, string) // event, agentID
	onEventExpired func(*model.Event)         // event
	onEventFailed  func(*model.Event, error)  // event, error
}

// Config holds configuration for the event router
type Config struct {
	Registry      *registry.AgentRegistry
	MaxRetries    int
	RetryInterval time.Duration

	// Optional callbacks
	OnEventRouted func(*model.Event, string)
	OnEventQueued func(*model.Event, string)
	OnEventExpired func(*model.Event)
	OnEventFailed  func(*model.Event, error)
}

// NewEventRouter creates a new event router
func NewEventRouter(config Config) *EventRouter {
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryInterval == 0 {
		config.RetryInterval = 30 * time.Second
	}

	router := &EventRouter{
		registry:      config.Registry,
		pendingEvents: make(map[string][]*PendingEvent),
		maxRetries:    config.MaxRetries,
		retryInterval: config.RetryInterval,
		onEventRouted: config.OnEventRouted,
		onEventQueued: config.OnEventQueued,
		onEventExpired: config.OnEventExpired,
		onEventFailed:  config.OnEventFailed,
	}

	// Start background worker to retry pending events
	go router.pendingEventsWorker()

	return router
}

// RouteEvent routes an event to its target agent
func (er *EventRouter) RouteEvent(event *model.Event) error {
	// Validate event
	if err := event.Validate(); err != nil {
		if er.onEventFailed != nil {
			er.onEventFailed(event, err)
		}
		return fmt.Errorf("event validation failed: %w", err)
	}

	// Check if event is expired
	if event.IsExpired() {
		if er.onEventExpired != nil {
			er.onEventExpired(event)
		}
		return fmt.Errorf("event %s is expired", event.ID)
	}

	// Get target agent
	agent, err := er.registry.GetAgent(event.TargetAgent)
	if err != nil {
		// Agent not found or disconnected - queue the event
		return er.queueEvent(event)
	}

	// Check if agent is connected and healthy
	if agent.Status != model.AgentStatusConnected {
		// Queue for later delivery
		return er.queueEvent(event)
	}

	// Send event to agent
	return er.sendEventToAgent(event, event.TargetAgent)
}

// sendEventToAgent sends an event to a specific agent
func (er *EventRouter) sendEventToAgent(event *model.Event, agentID string) error {
	// Create event message
	msg := EventMessage{
		Type:  "event",
		Event: event,
	}

	// Serialize to JSON
	data, err := json.Marshal(msg)
	if err != nil {
		if er.onEventFailed != nil {
			er.onEventFailed(event, err)
		}
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Send to agent via registry
	if err := er.registry.SendToAgent(agentID, data); err != nil {
		// Failed to send - queue it
		return er.queueEvent(event)
	}

	// Trigger callback
	if er.onEventRouted != nil {
		er.onEventRouted(event, agentID)
	}

	return nil
}

// queueEvent queues an event for later delivery when agent reconnects
func (er *EventRouter) queueEvent(event *model.Event) error {
	er.mu.Lock()
	defer er.mu.Unlock()

	agentID := event.TargetAgent

	// Check if event is already expired
	if event.IsExpired() {
		if er.onEventExpired != nil {
			er.onEventExpired(event)
		}
		return fmt.Errorf("event %s is expired, cannot queue", event.ID)
	}

	// Create pending event
	pending := &PendingEvent{
		Event:     event,
		QueuedAt:  time.Now(),
		Retries:   0,
		ExpiresAt: event.CreatedAt.Add(event.TTL),
	}

	// Add to pending queue
	er.pendingEvents[agentID] = append(er.pendingEvents[agentID], pending)

	// Trigger callback
	if er.onEventQueued != nil {
		er.onEventQueued(event, agentID)
	}

	return nil
}

// pendingEventsWorker periodically tries to deliver pending events
func (er *EventRouter) pendingEventsWorker() {
	ticker := time.NewTicker(er.retryInterval)
	defer ticker.Stop()

	for range ticker.C {
		er.processPendingEvents()
	}
}

// processPendingEvents attempts to deliver all pending events
func (er *EventRouter) processPendingEvents() {
	er.mu.Lock()
	defer er.mu.Unlock()

	now := time.Now()

	for agentID, events := range er.pendingEvents {
		remainingEvents := make([]*PendingEvent, 0)

		for _, pending := range events {
			// Check if expired
			if now.After(pending.ExpiresAt) {
				if er.onEventExpired != nil {
					er.onEventExpired(pending.Event)
				}
				continue
			}

			// Check if max retries exceeded
			if pending.Retries >= er.maxRetries {
				if er.onEventFailed != nil {
					er.onEventFailed(pending.Event, fmt.Errorf("max retries exceeded"))
				}
				continue
			}

			// Try to get agent
			agent, err := er.registry.GetAgent(agentID)
			if err != nil || agent.Status != model.AgentStatusConnected {
				// Agent still not available, keep in queue
				remainingEvents = append(remainingEvents, pending)
				continue
			}

			// Try to send
			if err := er.sendEventToAgent(pending.Event, agentID); err != nil {
				// Failed to send, increment retry and keep in queue
				pending.Retries++
				remainingEvents = append(remainingEvents, pending)
				continue
			}

			// Successfully sent - remove from pending
		}

		// Update pending events for this agent
		if len(remainingEvents) > 0 {
			er.pendingEvents[agentID] = remainingEvents
		} else {
			delete(er.pendingEvents, agentID)
		}
	}
}

// GetPendingEventsCount returns the number of pending events for an agent
func (er *EventRouter) GetPendingEventsCount(agentID string) int {
	er.mu.RLock()
	defer er.mu.RUnlock()

	return len(er.pendingEvents[agentID])
}

// GetTotalPendingEvents returns the total number of pending events across all agents
func (er *EventRouter) GetTotalPendingEvents() int {
	er.mu.RLock()
	defer er.mu.RUnlock()

	total := 0
	for _, events := range er.pendingEvents {
		total += len(events)
	}
	return total
}

// ClearPendingEvents clears all pending events for an agent (useful for cleanup)
func (er *EventRouter) ClearPendingEvents(agentID string) {
	er.mu.Lock()
	defer er.mu.Unlock()

	delete(er.pendingEvents, agentID)
}

// GetPendingEvents returns all pending events for an agent
func (er *EventRouter) GetPendingEvents(agentID string) []*model.Event {
	er.mu.RLock()
	defer er.mu.RUnlock()

	pending := er.pendingEvents[agentID]
	events := make([]*model.Event, len(pending))
	for i, p := range pending {
		events[i] = p.Event
	}
	return events
}

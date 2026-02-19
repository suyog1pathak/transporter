package registry

import (
	"fmt"
	"sync"
	"time"

	"github.com/suyog1pathak/transporter/internal/model"
	"github.com/gorilla/websocket"
)

// AgentConnection wraps a websocket connection with an agent
type AgentConnection struct {
	Agent      *model.Agent
	Conn       *websocket.Conn
	SendChan   chan []byte // Channel for sending messages to agent
	mu         sync.Mutex
}

// Send sends a message to the agent (thread-safe)
func (ac *AgentConnection) Send(message []byte) error {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	select {
	case ac.SendChan <- message:
		return nil
	default:
		return fmt.Errorf("send channel full for agent %s", ac.Agent.ID)
	}
}

// Close closes the agent connection
func (ac *AgentConnection) Close() error {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	close(ac.SendChan)
	return ac.Conn.Close()
}

// AgentRegistry manages all connected agents
type AgentRegistry struct {
	agents              map[string]*AgentConnection // agentID -> connection
	mu                  sync.RWMutex
	heartbeatTimeout    time.Duration
	heartbeatCheckInterval time.Duration
	onAgentConnected    func(*model.Agent)
	onAgentDisconnected func(*model.Agent)
}

// Config holds configuration for the agent registry
type Config struct {
	HeartbeatTimeout       time.Duration
	HeartbeatCheckInterval time.Duration
	OnAgentConnected       func(*model.Agent)
	OnAgentDisconnected    func(*model.Agent)
}

// NewAgentRegistry creates a new agent registry
func NewAgentRegistry(config Config) *AgentRegistry {
	if config.HeartbeatTimeout == 0 {
		config.HeartbeatTimeout = 30 * time.Second
	}
	if config.HeartbeatCheckInterval == 0 {
		config.HeartbeatCheckInterval = 10 * time.Second
	}

	registry := &AgentRegistry{
		agents:              make(map[string]*AgentConnection),
		heartbeatTimeout:    config.HeartbeatTimeout,
		heartbeatCheckInterval: config.HeartbeatCheckInterval,
		onAgentConnected:    config.OnAgentConnected,
		onAgentDisconnected: config.OnAgentDisconnected,
	}

	// Start background health checker
	go registry.healthChecker()

	return registry
}

// Register registers a new agent connection
func (ar *AgentRegistry) Register(registration *model.AgentRegistration, conn *websocket.Conn, connectionID string) (*model.Agent, error) {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	// Validate registration
	if err := registration.Validate(); err != nil {
		return nil, err
	}

	// Check if agent already exists
	if existing, exists := ar.agents[registration.ID]; exists {
		// Close old connection
		existing.Close()
		existing.Agent.MarkDisconnected()
		if ar.onAgentDisconnected != nil {
			ar.onAgentDisconnected(existing.Agent)
		}
	}

	// Create agent from registration
	agent := registration.ToAgent(connectionID)

	// Create agent connection
	agentConn := &AgentConnection{
		Agent:    agent,
		Conn:     conn,
		SendChan: make(chan []byte, 100), // Buffered channel for messages
	}

	// Store in registry
	ar.agents[agent.ID] = agentConn

	// Trigger callback
	if ar.onAgentConnected != nil {
		ar.onAgentConnected(agent)
	}

	return agent, nil
}

// Unregister removes an agent from the registry
func (ar *AgentRegistry) Unregister(agentID string) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	agentConn, exists := ar.agents[agentID]
	if !exists {
		return model.ErrAgentNotFound
	}

	// Mark as disconnected
	agentConn.Agent.MarkDisconnected()

	// Close connection
	agentConn.Close()

	// Remove from registry
	delete(ar.agents, agentID)

	// Trigger callback
	if ar.onAgentDisconnected != nil {
		ar.onAgentDisconnected(agentConn.Agent)
	}

	return nil
}

// Get retrieves an agent connection by ID
func (ar *AgentRegistry) Get(agentID string) (*AgentConnection, error) {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	agentConn, exists := ar.agents[agentID]
	if !exists {
		return nil, model.ErrAgentNotFound
	}

	return agentConn, nil
}

// GetAgent retrieves just the agent metadata
func (ar *AgentRegistry) GetAgent(agentID string) (*model.Agent, error) {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	agentConn, exists := ar.agents[agentID]
	if !exists {
		return nil, model.ErrAgentNotFound
	}

	return agentConn.Agent, nil
}

// List returns all registered agents
func (ar *AgentRegistry) List() []*model.Agent {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	agents := make([]*model.Agent, 0, len(ar.agents))
	for _, agentConn := range ar.agents {
		agents = append(agents, agentConn.Agent)
	}

	return agents
}

// ListConnected returns only connected agents
func (ar *AgentRegistry) ListConnected() []*model.Agent {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	agents := make([]*model.Agent, 0, len(ar.agents))
	for _, agentConn := range ar.agents {
		if agentConn.Agent.Status == model.AgentStatusConnected {
			agents = append(agents, agentConn.Agent)
		}
	}

	return agents
}

// Count returns the total number of registered agents
func (ar *AgentRegistry) Count() int {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	return len(ar.agents)
}

// UpdateHeartbeat updates the heartbeat timestamp for an agent
func (ar *AgentRegistry) UpdateHeartbeat(agentID string) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	agentConn, exists := ar.agents[agentID]
	if !exists {
		return model.ErrAgentNotFound
	}

	agentConn.Agent.UpdateHeartbeat()
	return nil
}

// healthChecker periodically checks agent health based on heartbeat
func (ar *AgentRegistry) healthChecker() {
	ticker := time.NewTicker(ar.heartbeatCheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		ar.checkAgentHealth()
	}
}

// checkAgentHealth checks all agents for health based on heartbeat
func (ar *AgentRegistry) checkAgentHealth() {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	now := time.Now()
	for agentID, agentConn := range ar.agents {
		agent := agentConn.Agent

		if agent.Status == model.AgentStatusConnected {
			timeSinceHeartbeat := now.Sub(agent.LastHeartbeat)
			if timeSinceHeartbeat > ar.heartbeatTimeout {
				// Mark as unhealthy
				agent.MarkUnhealthy()
				fmt.Printf("Agent %s marked unhealthy (no heartbeat for %v)\n", agentID, timeSinceHeartbeat)
			}
		}
	}
}

// SendToAgent sends a message to a specific agent
func (ar *AgentRegistry) SendToAgent(agentID string, message []byte) error {
	agentConn, err := ar.Get(agentID)
	if err != nil {
		return err
	}

	return agentConn.Send(message)
}

// BroadcastToAll sends a message to all connected agents
func (ar *AgentRegistry) BroadcastToAll(message []byte) {
	ar.mu.RLock()
	defer ar.mu.RUnlock()

	for _, agentConn := range ar.agents {
		if agentConn.Agent.Status == model.AgentStatusConnected {
			// Non-blocking send
			go agentConn.Send(message)
		}
	}
}

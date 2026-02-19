package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/suyog1pathak/transporter/internal/model"
)

// RedisStorage implements persistent storage using Redis
type RedisStorage struct {
	client *redis.Client
	ctx    context.Context
}

// Config holds Redis configuration
type Config struct {
	Addr     string // Redis server address (host:port)
	Password string // Redis password (empty for no password)
	DB       int    // Redis database number (0-15)
}

// NewRedisStorage creates a new Redis storage instance
func NewRedisStorage(config Config) (*RedisStorage, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     config.Addr,
		Password: config.Password,
		DB:       config.DB,
	})

	ctx := context.Background()

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisStorage{
		client: client,
		ctx:    ctx,
	}, nil
}

// Close closes the Redis connection
func (rs *RedisStorage) Close() error {
	return rs.client.Close()
}

// Event Status Operations

// SaveEventStatus saves event status to Redis
func (rs *RedisStorage) SaveEventStatus(status *model.EventStatus) error {
	key := fmt.Sprintf("event:status:%s", status.EventID)

	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal event status: %w", err)
	}

	// Save with TTL of 7 days (configurable)
	ttl := 7 * 24 * time.Hour
	if err := rs.client.Set(rs.ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("failed to save event status: %w", err)
	}

	// Add to event list for the agent
	agentEventsKey := fmt.Sprintf("agent:events:%s", status.AgentID)
	if err := rs.client.ZAdd(rs.ctx, agentEventsKey, redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: status.EventID,
	}).Err(); err != nil {
		return fmt.Errorf("failed to add event to agent list: %w", err)
	}

	// Add to events by state index
	stateKey := fmt.Sprintf("events:state:%s", status.State)
	if err := rs.client.SAdd(rs.ctx, stateKey, status.EventID).Err(); err != nil {
		return fmt.Errorf("failed to add event to state index: %w", err)
	}

	return nil
}

// GetEventStatus retrieves event status from Redis
func (rs *RedisStorage) GetEventStatus(eventID string) (*model.EventStatus, error) {
	key := fmt.Sprintf("event:status:%s", eventID)

	data, err := rs.client.Get(rs.ctx, key).Bytes()
	if err == redis.Nil {
		return nil, model.ErrStatusNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get event status: %w", err)
	}

	var status model.EventStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event status: %w", err)
	}

	return &status, nil
}

// ListEventsByAgent lists all events for a specific agent
func (rs *RedisStorage) ListEventsByAgent(agentID string, limit int) ([]string, error) {
	key := fmt.Sprintf("agent:events:%s", agentID)

	// Get most recent events (sorted by timestamp, descending)
	eventIDs, err := rs.client.ZRevRange(rs.ctx, key, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list events for agent: %w", err)
	}

	return eventIDs, nil
}

// ListEventsByState lists all events in a specific state
func (rs *RedisStorage) ListEventsByState(state model.ExecutionState) ([]string, error) {
	key := fmt.Sprintf("events:state:%s", state)

	eventIDs, err := rs.client.SMembers(rs.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list events by state: %w", err)
	}

	return eventIDs, nil
}

// Agent State Operations

// SaveAgent saves agent state to Redis
func (rs *RedisStorage) SaveAgent(agent *model.Agent) error {
	key := fmt.Sprintf("agent:%s", agent.ID)

	data, err := json.Marshal(agent)
	if err != nil {
		return fmt.Errorf("failed to marshal agent: %w", err)
	}

	if err := rs.client.Set(rs.ctx, key, data, 0).Err(); err != nil {
		return fmt.Errorf("failed to save agent: %w", err)
	}

	// Add to agent list
	if err := rs.client.SAdd(rs.ctx, "agents:all", agent.ID).Err(); err != nil {
		return fmt.Errorf("failed to add agent to list: %w", err)
	}

	// Index by cluster
	clusterKey := fmt.Sprintf("agents:cluster:%s", agent.ClusterName)
	if err := rs.client.SAdd(rs.ctx, clusterKey, agent.ID).Err(); err != nil {
		return fmt.Errorf("failed to add agent to cluster index: %w", err)
	}

	return nil
}

// GetAgent retrieves agent state from Redis
func (rs *RedisStorage) GetAgent(agentID string) (*model.Agent, error) {
	key := fmt.Sprintf("agent:%s", agentID)

	data, err := rs.client.Get(rs.ctx, key).Bytes()
	if err == redis.Nil {
		return nil, model.ErrAgentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent: %w", err)
	}

	var agent model.Agent
	if err := json.Unmarshal(data, &agent); err != nil {
		return nil, fmt.Errorf("failed to unmarshal agent: %w", err)
	}

	return &agent, nil
}

// ListAllAgents lists all registered agents
func (rs *RedisStorage) ListAllAgents() ([]string, error) {
	agentIDs, err := rs.client.SMembers(rs.ctx, "agents:all").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	return agentIDs, nil
}

// ListAgentsByCluster lists all agents in a specific cluster
func (rs *RedisStorage) ListAgentsByCluster(clusterName string) ([]string, error) {
	key := fmt.Sprintf("agents:cluster:%s", clusterName)

	agentIDs, err := rs.client.SMembers(rs.ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list agents by cluster: %w", err)
	}

	return agentIDs, nil
}

// DeleteAgent removes an agent from Redis
func (rs *RedisStorage) DeleteAgent(agentID string) error {
	// Get agent first to remove from indexes
	agent, err := rs.GetAgent(agentID)
	if err != nil {
		return err
	}

	// Delete agent data
	key := fmt.Sprintf("agent:%s", agentID)
	if err := rs.client.Del(rs.ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete agent: %w", err)
	}

	// Remove from agent list
	if err := rs.client.SRem(rs.ctx, "agents:all", agentID).Err(); err != nil {
		return fmt.Errorf("failed to remove agent from list: %w", err)
	}

	// Remove from cluster index
	clusterKey := fmt.Sprintf("agents:cluster:%s", agent.ClusterName)
	if err := rs.client.SRem(rs.ctx, clusterKey, agentID).Err(); err != nil {
		return fmt.Errorf("failed to remove agent from cluster index: %w", err)
	}

	return nil
}

// Audit Log Operations

// AuditLogEntry represents an audit log entry
type AuditLogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	EventID   string                 `json:"event_id"`
	AgentID   string                 `json:"agent_id"`
	Action    string                 `json:"action"` // event_created, event_routed, event_completed, etc.
	User      string                 `json:"user,omitempty"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// SaveAuditLog saves an audit log entry
func (rs *RedisStorage) SaveAuditLog(entry *AuditLogEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal audit log: %w", err)
	}

	// Add to audit log stream
	if err := rs.client.XAdd(rs.ctx, &redis.XAddArgs{
		Stream: "audit:log",
		Values: map[string]interface{}{
			"data": string(data),
		},
	}).Err(); err != nil {
		return fmt.Errorf("failed to save audit log: %w", err)
	}

	return nil
}

// GetRecentAuditLogs retrieves recent audit log entries
func (rs *RedisStorage) GetRecentAuditLogs(count int) ([]*AuditLogEntry, error) {
	// Read from stream (most recent entries)
	messages, err := rs.client.XRevRangeN(rs.ctx, "audit:log", "+", "-", int64(count)).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get audit logs: %w", err)
	}

	entries := make([]*AuditLogEntry, 0, len(messages))
	for _, msg := range messages {
		dataStr, ok := msg.Values["data"].(string)
		if !ok {
			continue
		}

		var entry AuditLogEntry
		if err := json.Unmarshal([]byte(dataStr), &entry); err != nil {
			continue
		}

		entries = append(entries, &entry)
	}

	return entries, nil
}

// Statistics Operations

// IncrementEventCount increments the total event count
func (rs *RedisStorage) IncrementEventCount() error {
	return rs.client.Incr(rs.ctx, "stats:events:total").Err()
}

// IncrementEventStateCount increments the count for a specific event state
func (rs *RedisStorage) IncrementEventStateCount(state model.ExecutionState) error {
	key := fmt.Sprintf("stats:events:state:%s", state)
	return rs.client.Incr(rs.ctx, key).Err()
}

// GetEventStats retrieves event statistics
func (rs *RedisStorage) GetEventStats() (map[string]int64, error) {
	stats := make(map[string]int64)

	// Get total events
	total, err := rs.client.Get(rs.ctx, "stats:events:total").Int64()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	stats["total"] = total

	// Get counts by state
	states := []model.ExecutionState{
		model.StateCreated,
		model.StateQueued,
		model.StateAssigned,
		model.StateInProgress,
		model.StateCompleted,
		model.StateFailed,
		model.StateExpired,
	}

	for _, state := range states {
		key := fmt.Sprintf("stats:events:state:%s", state)
		count, err := rs.client.Get(rs.ctx, key).Int64()
		if err != nil && err != redis.Nil {
			return nil, err
		}
		stats[string(state)] = count
	}

	return stats, nil
}

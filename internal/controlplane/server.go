package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/suyog1pathak/transporter/internal/model"
	"github.com/suyog1pathak/transporter/pkg/logger"
	"github.com/suyog1pathak/transporter/pkg/queue"
	"github.com/suyog1pathak/transporter/pkg/registry"
	"github.com/suyog1pathak/transporter/pkg/router"
	"github.com/suyog1pathak/transporter/pkg/storage"
)

// Config holds all configuration for the control plane server.
type Config struct {
	// WebSocket Server
	WSAddr string
	WSPort int

	// Memphis Config
	MemphisEnabled         bool
	MemphisHost            string
	MemphisUsername        string
	MemphisPassword        string
	MemphisConnectionToken string
	MemphisStation         string
	MemphisAccountID       int

	// Redis Config
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// Health & Timeouts
	HeartbeatTimeout time.Duration
	EventRetryMax    int

	Debug bool
}

// Run starts the control plane server and blocks until shutdown.
func Run(cfg Config) error {
	logger.InitLogger(cfg.Debug)
	logger.Info("Starting Transporter Control Plane")

	// Initialize Redis storage
	logger.Info("Connecting to Redis", "addr", cfg.RedisAddr)
	redisStorage, err := storage.NewRedisStorage(storage.Config{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to Redis: %w", err)
	}
	defer redisStorage.Close()
	logger.Info("Redis connected")

	// Initialize Memphis queue (optional)
	var memphisQueue *queue.MemphisQueue
	if cfg.MemphisEnabled {
		logger.Info("Connecting to Memphis", "host", cfg.MemphisHost)
		memphisQueue, err = queue.NewMemphisQueue(queue.Config{
			Host:            cfg.MemphisHost,
			Username:        cfg.MemphisUsername,
			Password:        cfg.MemphisPassword,
			ConnectionToken: cfg.MemphisConnectionToken,
			StationName:     cfg.MemphisStation,
			AccountID:       cfg.MemphisAccountID,
		})
		if err != nil {
			return fmt.Errorf("failed to connect to Memphis: %w", err)
		}
		defer memphisQueue.Close()
		logger.Info("Memphis connected")
	} else {
		logger.Info("Memphis disabled, skipping event consumption")
	}

	// Initialize agent registry
	logger.Info("Initializing agent registry")
	agentRegistry := registry.NewAgentRegistry(registry.Config{
		HeartbeatTimeout:       cfg.HeartbeatTimeout,
		HeartbeatCheckInterval: 10 * time.Second,
		OnAgentConnected: func(agent *model.Agent) {
			logger.Info("Agent connected", "agent_id", agent.ID, "cluster", agent.ClusterName, "region", agent.Region)
			if err := redisStorage.SaveAgent(agent); err != nil {
				logger.Warn("Failed to save agent state", "error", err)
			}
			redisStorage.SaveAuditLog(&storage.AuditLogEntry{
				Timestamp: time.Now(),
				AgentID:   agent.ID,
				Action:    "agent_connected",
			})
		},
		OnAgentDisconnected: func(agent *model.Agent) {
			logger.Info("Agent disconnected", "agent_id", agent.ID)
			agent.MarkDisconnected()
			redisStorage.SaveAgent(agent)
			redisStorage.SaveAuditLog(&storage.AuditLogEntry{
				Timestamp: time.Now(),
				AgentID:   agent.ID,
				Action:    "agent_disconnected",
			})
		},
	})
	logger.Info("Agent registry initialized")

	// Initialize event router
	logger.Info("Initializing event router")
	eventRouter := router.NewEventRouter(router.Config{
		Registry:      agentRegistry,
		MaxRetries:    cfg.EventRetryMax,
		RetryInterval: 30 * time.Second,
		OnEventRouted: func(event *model.Event, agentID string) {
			logger.Info("Event routed to agent", "event_id", event.ID, "agent_id", agentID)
			status := model.NewEventStatus(event.ID, agentID)
			status.UpdateState(model.StateAssigned, "Event routed to agent")
			redisStorage.SaveEventStatus(status)
			redisStorage.IncrementEventStateCount(model.StateAssigned)
		},
		OnEventQueued: func(event *model.Event, agentID string) {
			logger.Info("Event queued for offline agent", "event_id", event.ID, "agent_id", agentID)
			status := model.NewEventStatus(event.ID, agentID)
			status.UpdateState(model.StateQueued, "Agent offline, event queued")
			redisStorage.SaveEventStatus(status)
			redisStorage.IncrementEventStateCount(model.StateQueued)
		},
		OnEventExpired: func(event *model.Event) {
			logger.Warn("Event expired", "event_id", event.ID)
			status := model.NewEventStatus(event.ID, event.TargetAgent)
			status.MarkExpired()
			redisStorage.SaveEventStatus(status)
			redisStorage.IncrementEventStateCount(model.StateExpired)
		},
		OnEventFailed: func(event *model.Event, err error) {
			logger.Error("Event failed", "event_id", event.ID, "error", err)
			status := model.NewEventStatus(event.ID, event.TargetAgent)
			status.MarkFailed(err.Error())
			redisStorage.SaveEventStatus(status)
			redisStorage.IncrementEventStateCount(model.StateFailed)
		},
	})
	logger.Info("Event router initialized")

	// Start Memphis event consumer (if enabled)
	if cfg.MemphisEnabled && memphisQueue != nil {
		logger.Info("Starting event consumer")
		go func() {
			err := memphisQueue.ConsumeEvents("transporter-cp-consumer", func(event *model.Event) error {
				logger.Info("Received event", "event_id", event.ID, "type", event.Type, "target_agent", event.TargetAgent)
				redisStorage.IncrementEventCount()
				redisStorage.IncrementEventStateCount(model.StateCreated)
				redisStorage.SaveAuditLog(&storage.AuditLogEntry{
					Timestamp: time.Now(),
					EventID:   event.ID,
					AgentID:   event.TargetAgent,
					Action:    "event_received",
					User:      event.CreatedBy,
				})
				return eventRouter.RouteEvent(event)
			})
			if err != nil {
				logger.Error("Event consumer error", "error", err)
			}
		}()
		logger.Info("Event consumer started")
	}

	// Set up HTTP handlers
	mux := http.NewServeMux()

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // TODO: Add proper origin checking
		},
	}

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleAgentConnection(w, r, &upgrader, agentRegistry, redisStorage, eventRouter)
	})

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var event model.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			http.Error(w, fmt.Sprintf("Invalid event: %v", err), http.StatusBadRequest)
			return
		}

		if err := event.Validate(); err != nil {
			http.Error(w, fmt.Sprintf("Event validation failed: %v", err), http.StatusBadRequest)
			return
		}

		logger.Info("Received event via HTTP", "event_id", event.ID, "type", event.Type, "target_agent", event.TargetAgent)
		redisStorage.IncrementEventCount()
		redisStorage.IncrementEventStateCount(model.StateCreated)
		redisStorage.SaveAuditLog(&storage.AuditLogEntry{
			Timestamp: time.Now(),
			EventID:   event.ID,
			AgentID:   event.TargetAgent,
			Action:    "event_received_http",
			User:      event.CreatedBy,
		})

		if err := eventRouter.RouteEvent(&event); err != nil {
			http.Error(w, fmt.Sprintf("Failed to route event: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "accepted",
			"event_id": event.ID,
			"message":  "Event routed to agent",
		})
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "healthy",
			"agent_count": agentRegistry.Count(),
			"version":     "0.1.0",
		})
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		stats, _ := redisStorage.GetEventStats()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"agents": map[string]interface{}{
				"total":     agentRegistry.Count(),
				"connected": len(agentRegistry.ListConnected()),
			},
			"events": stats,
		})
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.WSAddr, cfg.WSPort),
		Handler: mux,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		logger.Info("Shutting down Control Plane")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	logger.Info("Control Plane started successfully!")
	logger.Info("WebSocket endpoint", "url", fmt.Sprintf("ws://%s:%d/ws", cfg.WSAddr, cfg.WSPort))
	logger.Info("Health endpoint", "url", fmt.Sprintf("http://%s:%d/health", cfg.WSAddr, cfg.WSPort))
	logger.Info("Metrics endpoint", "url", fmt.Sprintf("http://%s:%d/metrics", cfg.WSAddr, cfg.WSPort))

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func handleAgentConnection(w http.ResponseWriter, r *http.Request, upgrader *websocket.Upgrader,
	agentRegistry *registry.AgentRegistry, redisStorage *storage.RedisStorage, eventRouter *router.EventRouter) {

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("Failed to upgrade connection", "error", err)
		return
	}

	var registration model.AgentRegistration
	if err := conn.ReadJSON(&registration); err != nil {
		logger.Error("Failed to read registration", "error", err)
		conn.Close()
		return
	}

	if err := registration.Validate(); err != nil {
		logger.Error("Invalid registration", "error", err)
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}

	agent, err := agentRegistry.Register(&registration, conn, r.RemoteAddr)
	if err != nil {
		logger.Error("Failed to register agent", "error", err)
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}

	conn.WriteJSON(map[string]string{
		"status":  "registered",
		"message": fmt.Sprintf("Agent %s registered successfully", agent.ID),
	})

	go handleAgentReads(conn, agent, agentRegistry, redisStorage)
	go handleAgentWrites(conn, agent, agentRegistry)
}

func handleAgentReads(conn *websocket.Conn, agent *model.Agent,
	agentRegistry *registry.AgentRegistry, redisStorage *storage.RedisStorage) {

	defer func() {
		agentRegistry.Unregister(agent.ID)
		conn.Close()
	}()

	for {
		var message map[string]interface{}
		if err := conn.ReadJSON(&message); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logger.Info("Agent closed connection", "agent_id", agent.ID)
			} else {
				logger.Error("Error reading from agent", "agent_id", agent.ID, "error", err)
			}
			return
		}

		msgType, ok := message["type"].(string)
		if !ok {
			continue
		}

		switch msgType {
		case "heartbeat":
			agentRegistry.UpdateHeartbeat(agent.ID)

		case "status_update":
			var statusUpdate model.StatusUpdate
			data, _ := json.Marshal(message)
			if err := json.Unmarshal(data, &statusUpdate); err != nil {
				logger.Error("Failed to unmarshal status update", "error", err)
				continue
			}

			status, err := redisStorage.GetEventStatus(statusUpdate.EventID)
			if err != nil {
				status = model.NewEventStatus(statusUpdate.EventID, agent.ID)
			}

			if statusUpdate.State != "" {
				status.State = statusUpdate.State
			}
			if statusUpdate.Phase != "" {
				status.Phase = statusUpdate.Phase
			}
			if statusUpdate.Message != "" {
				status.Message = statusUpdate.Message
			}
			if statusUpdate.Result != nil {
				status.Result = statusUpdate.Result
			}
			if statusUpdate.LogLevel != "" {
				status.AddLog(statusUpdate.LogLevel, statusUpdate.Phase, statusUpdate.Message, statusUpdate.Details)
			}

			status.UpdatedAt = time.Now()
			redisStorage.SaveEventStatus(status)
			logger.Info("Status update", "event_id", statusUpdate.EventID, "state", status.State, "phase", status.Phase)
		}
	}
}

func handleAgentWrites(conn *websocket.Conn, agent *model.Agent, agentRegistry *registry.AgentRegistry) {
	agentConn, err := agentRegistry.Get(agent.ID)
	if err != nil {
		return
	}

	for msg := range agentConn.SendChan {
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			logger.Error("Error writing to agent", "agent_id", agent.ID, "error", err)
			return
		}
	}
}

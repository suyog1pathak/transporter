package cmd

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
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/suyog1pathak/transporter/model"
	"github.com/suyog1pathak/transporter/pkg/logger"
	"github.com/suyog1pathak/transporter/pkg/queue"
	"github.com/suyog1pathak/transporter/pkg/registry"
	"github.com/suyog1pathak/transporter/pkg/router"
	"github.com/suyog1pathak/transporter/pkg/storage"
)

var cpCmd = &cobra.Command{
	Use:   "cp",
	Short: "Start the Control Plane",
	Long:  `Start the Transporter Control Plane which manages agents and routes events.`,
	RunE:  runCP,
}

var cpConfig struct {
	// WebSocket Server
	wsAddr string
	wsPort int

	// Memphis Config
	memphisEnabled  bool
	memphisHost     string
	memphisUsername string
	memphisPassword string
	memphisStation  string
	memphisAccountID int

	// Redis Config
	redisAddr     string
	redisPassword string
	redisDB       int

	// Health & Timeouts
	heartbeatTimeout time.Duration
	eventRetryMax    int
}

func init() {
	rootCmd.AddCommand(cpCmd)

	// WebSocket flags
	cpCmd.Flags().StringVar(&cpConfig.wsAddr, "ws-addr", "0.0.0.0", "WebSocket server address")
	cpCmd.Flags().IntVar(&cpConfig.wsPort, "ws-port", 8080, "WebSocket server port")

	// Memphis flags
	cpCmd.Flags().BoolVar(&cpConfig.memphisEnabled, "memphis-enabled", true, "Enable Memphis queue integration")
	cpCmd.Flags().StringVar(&cpConfig.memphisHost, "memphis-host", "localhost", "Memphis server hostname")
	cpCmd.Flags().StringVar(&cpConfig.memphisUsername, "memphis-username", "root", "Memphis username")
	cpCmd.Flags().StringVar(&cpConfig.memphisPassword, "memphis-password", "memphis", "Memphis password")
	cpCmd.Flags().StringVar(&cpConfig.memphisStation, "memphis-station", "transporter-events", "Memphis station name")
	cpCmd.Flags().IntVar(&cpConfig.memphisAccountID, "memphis-account-id", 1, "Memphis account ID")

	// Redis flags
	cpCmd.Flags().StringVar(&cpConfig.redisAddr, "redis-addr", "localhost:6379", "Redis server address")
	cpCmd.Flags().StringVar(&cpConfig.redisPassword, "redis-password", "", "Redis password")
	cpCmd.Flags().IntVar(&cpConfig.redisDB, "redis-db", 0, "Redis database number")

	// Health & Timeouts
	cpCmd.Flags().DurationVar(&cpConfig.heartbeatTimeout, "heartbeat-timeout", 30*time.Second, "Agent heartbeat timeout")
	cpCmd.Flags().IntVar(&cpConfig.eventRetryMax, "event-retry-max", 3, "Maximum event retry attempts")

	// Bind flags to viper
	viper.BindPFlags(cpCmd.Flags())
}

func runCP(cmd *cobra.Command, args []string) error {
	// Initialize logger
	logger.InitLogger(viper.GetBool("debug"))
	logger.Info("🚀 Starting Transporter Control Plane")

	// Initialize Redis storage
	logger.Info("📦 Connecting to Redis", "addr", cpConfig.redisAddr)
	redisStorage, err := storage.NewRedisStorage(storage.Config{
		Addr:     cpConfig.redisAddr,
		Password: cpConfig.redisPassword,
		DB:       cpConfig.redisDB,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to Redis: %w", err)
	}
	defer redisStorage.Close()
	logger.Info("✅ Redis connected")

	// Initialize Memphis queue (optional)
	var memphisQueue *queue.MemphisQueue
	if cpConfig.memphisEnabled {
		logger.Info("📬 Connecting to Memphis", "host", cpConfig.memphisHost)
		var err error
		memphisQueue, err = queue.NewMemphisQueue(queue.Config{
			Host:        cpConfig.memphisHost,
			Username:    cpConfig.memphisUsername,
			Password:    cpConfig.memphisPassword,
			StationName: cpConfig.memphisStation,
			AccountID:   cpConfig.memphisAccountID,
		})
		if err != nil {
			return fmt.Errorf("failed to connect to Memphis: %w", err)
		}
		defer memphisQueue.Close()
		logger.Info("✅ Memphis connected")
	} else {
		logger.Info("⏭️  Memphis disabled, skipping event consumption")
	}

	// Initialize agent registry
	logger.Info("👥 Initializing agent registry")
	agentRegistry := registry.NewAgentRegistry(registry.Config{
		HeartbeatTimeout:       cpConfig.heartbeatTimeout,
		HeartbeatCheckInterval: 10 * time.Second,
		OnAgentConnected: func(agent *model.Agent) {
			logger.Info("✅ Agent connected", "agent_id", agent.ID, "cluster", agent.ClusterName, "region", agent.Region)
			// Save agent state to Redis
			if err := redisStorage.SaveAgent(agent); err != nil {
				logger.Warn("⚠️  Failed to save agent state", "error", err)
			}
			// Audit log
			redisStorage.SaveAuditLog(&storage.AuditLogEntry{
				Timestamp: time.Now(),
				AgentID:   agent.ID,
				Action:    "agent_connected",
			})
		},
		OnAgentDisconnected: func(agent *model.Agent) {
			logger.Info("❌ Agent disconnected", "agent_id", agent.ID)
			// Update agent state in Redis
			agent.MarkDisconnected()
			redisStorage.SaveAgent(agent)
			// Audit log
			redisStorage.SaveAuditLog(&storage.AuditLogEntry{
				Timestamp: time.Now(),
				AgentID:   agent.ID,
				Action:    "agent_disconnected",
			})
		},
	})
	logger.Info("✅ Agent registry initialized")

	// Initialize event router
	logger.Info("🔀 Initializing event router")
	eventRouter := router.NewEventRouter(router.Config{
		Registry:      agentRegistry,
		MaxRetries:    cpConfig.eventRetryMax,
		RetryInterval: 30 * time.Second,
		OnEventRouted: func(event *model.Event, agentID string) {
			logger.Info("📤 Event routed to agent", "event_id", event.ID, "agent_id", agentID)
			// Save event status
			status := model.NewEventStatus(event.ID, agentID)
			status.UpdateState(model.StateAssigned, "Event routed to agent")
			redisStorage.SaveEventStatus(status)
			redisStorage.IncrementEventStateCount(model.StateAssigned)
		},
		OnEventQueued: func(event *model.Event, agentID string) {
			logger.Info("📋 Event queued for offline agent", "event_id", event.ID, "agent_id", agentID)
			status := model.NewEventStatus(event.ID, agentID)
			status.UpdateState(model.StateQueued, "Agent offline, event queued")
			redisStorage.SaveEventStatus(status)
			redisStorage.IncrementEventStateCount(model.StateQueued)
		},
		OnEventExpired: func(event *model.Event) {
			logger.Warn("⏰ Event expired", "event_id", event.ID)
			status := model.NewEventStatus(event.ID, event.TargetAgent)
			status.MarkExpired()
			redisStorage.SaveEventStatus(status)
			redisStorage.IncrementEventStateCount(model.StateExpired)
		},
		OnEventFailed: func(event *model.Event, err error) {
			logger.Error("❌ Event failed", "event_id", event.ID, "error", err)
			status := model.NewEventStatus(event.ID, event.TargetAgent)
			status.MarkFailed(err.Error())
			redisStorage.SaveEventStatus(status)
			redisStorage.IncrementEventStateCount(model.StateFailed)
		},
	})
	logger.Info("✅ Event router initialized")

	// Start Memphis event consumer (if enabled)
	if cpConfig.memphisEnabled && memphisQueue != nil {
		logger.Info("🎧 Starting event consumer")
		go func() {
			err := memphisQueue.ConsumeEvents("transporter-cp-consumer", func(event *model.Event) error {
				logger.Info("📨 Received event", "event_id", event.ID, "type", event.Type, "target_agent", event.TargetAgent)

				// Increment stats
				redisStorage.IncrementEventCount()
				redisStorage.IncrementEventStateCount(model.StateCreated)

				// Audit log
				redisStorage.SaveAuditLog(&storage.AuditLogEntry{
					Timestamp: time.Now(),
					EventID:   event.ID,
					AgentID:   event.TargetAgent,
					Action:    "event_received",
					User:      event.CreatedBy,
				})

				// Route event to agent
				return eventRouter.RouteEvent(event)
			})
			if err != nil {
				logger.Error("❌ Event consumer error", "error", err)
			}
		}()
		logger.Info("✅ Event consumer started")
	}

	// Start WebSocket server for agents
	logger.Info("🌐 Starting WebSocket server", "addr", cpConfig.wsAddr, "port", cpConfig.wsPort)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // TODO: Add proper origin checking
		},
	}

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleAgentConnection(w, r, &upgrader, agentRegistry, redisStorage, eventRouter)
	})

	// Event submission endpoint (for testing without Memphis)
	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var event model.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			http.Error(w, fmt.Sprintf("Invalid event: %v", err), http.StatusBadRequest)
			return
		}

		// Validate event
		if err := event.Validate(); err != nil {
			http.Error(w, fmt.Sprintf("Event validation failed: %v", err), http.StatusBadRequest)
			return
		}

		logger.Info("📨 Received event via HTTP", "event_id", event.ID, "type", event.Type, "target_agent", event.TargetAgent)

		// Increment stats
		redisStorage.IncrementEventCount()
		redisStorage.IncrementEventStateCount(model.StateCreated)

		// Audit log
		redisStorage.SaveAuditLog(&storage.AuditLogEntry{
			Timestamp: time.Now(),
			EventID:   event.ID,
			AgentID:   event.TargetAgent,
			Action:    "event_received_http",
			User:      event.CreatedBy,
		})

		// Route event to agent
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

	// Health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":      "healthy",
			"agent_count": agentRegistry.Count(),
			"version":     "0.1.0",
		})
	})

	// Metrics endpoint (basic stats)
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
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
		Addr:    fmt.Sprintf("%s:%d", cpConfig.wsAddr, cpConfig.wsPort),
		Handler: http.DefaultServeMux,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		logger.Info("🛑 Shutting down Control Plane")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		server.Shutdown(ctx)
	}()

	logger.Info("✅ Control Plane started successfully!")
	logger.Info("   WebSocket endpoint", "url", fmt.Sprintf("ws://%s:%d/ws", cpConfig.wsAddr, cpConfig.wsPort))
	logger.Info("   Health endpoint", "url", fmt.Sprintf("http://%s:%d/health", cpConfig.wsAddr, cpConfig.wsPort))
	logger.Info("   Metrics endpoint", "url", fmt.Sprintf("http://%s:%d/metrics", cpConfig.wsAddr, cpConfig.wsPort))

	// Start server
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func handleAgentConnection(w http.ResponseWriter, r *http.Request, upgrader *websocket.Upgrader,
	agentRegistry *registry.AgentRegistry, redisStorage *storage.RedisStorage, eventRouter *router.EventRouter) {

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("Failed to upgrade connection", "error", err)
		return
	}

	// Expect agent registration message
	var registration model.AgentRegistration
	if err := conn.ReadJSON(&registration); err != nil {
		logger.Error("Failed to read registration", "error", err)
		conn.Close()
		return
	}

	// Validate registration
	if err := registration.Validate(); err != nil {
		logger.Error("Invalid registration", "error", err)
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}

	// Register agent
	agent, err := agentRegistry.Register(&registration, conn, r.RemoteAddr)
	if err != nil {
		logger.Error("Failed to register agent", "error", err)
		conn.WriteJSON(map[string]string{"error": err.Error()})
		conn.Close()
		return
	}

	// Send registration confirmation
	conn.WriteJSON(map[string]string{
		"status":  "registered",
		"message": fmt.Sprintf("Agent %s registered successfully", agent.ID),
	})

	// Start goroutines for reading and writing
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

		// Handle different message types
		msgType, ok := message["type"].(string)
		if !ok {
			continue
		}

		switch msgType {
		case "heartbeat":
			// Update heartbeat
			agentRegistry.UpdateHeartbeat(agent.ID)

		case "status_update":
			// Handle event status update from agent
			var statusUpdate model.StatusUpdate
			data, _ := json.Marshal(message)
			if err := json.Unmarshal(data, &statusUpdate); err != nil {
				logger.Error("Failed to unmarshal status update", "error", err)
				continue
			}

			// Get existing status or create new
			status, err := redisStorage.GetEventStatus(statusUpdate.EventID)
			if err != nil {
				status = model.NewEventStatus(statusUpdate.EventID, agent.ID)
			}

			// Update based on status update
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

			// Add log entry
			if statusUpdate.LogLevel != "" {
				status.AddLog(statusUpdate.LogLevel, statusUpdate.Phase, statusUpdate.Message, statusUpdate.Details)
			}

			status.UpdatedAt = time.Now()

			// Save to Redis
			redisStorage.SaveEventStatus(status)
			logger.Info("📊 Status update", "event_id", statusUpdate.EventID, "state", status.State, "phase", status.Phase)
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

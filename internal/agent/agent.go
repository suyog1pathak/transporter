package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/suyog1pathak/transporter/internal/model"
	"github.com/suyog1pathak/transporter/pkg/executor"
	"github.com/suyog1pathak/transporter/pkg/logger"
)

// Config holds all configuration for the data plane agent.
type Config struct {
	// Agent Identity
	AgentID         string
	AgentName       string
	ClusterName     string
	ClusterProvider string
	Region          string
	Namespace       string

	// Control Plane Connection
	CPURL string

	// Kubernetes Config
	KubeconfigPath string
	InCluster      bool

	// Heartbeat
	HeartbeatInterval time.Duration

	Debug bool
}

// Run starts the agent and blocks until shutdown.
func Run(cfg Config) error {
	logger.InitLogger(cfg.Debug)
	logger.Info("Starting Transporter Agent", "agent_id", cfg.AgentID)

	if cfg.AgentName == "" {
		cfg.AgentName = cfg.AgentID
	}

	hostname, _ := os.Hostname()

	// Initialize Kubernetes executor
	logger.Info("Initializing Kubernetes executor")
	k8sExecutor, err := executor.NewK8sExecutor(executor.Config{
		KubeconfigPath: cfg.KubeconfigPath,
		InCluster:      cfg.InCluster,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize Kubernetes executor: %w", err)
	}
	logger.Info("Kubernetes executor initialized")

	// Connect to Control Plane
	logger.Info("Connecting to Control Plane", "url", cfg.CPURL)
	conn, _, err := websocket.DefaultDialer.Dial(cfg.CPURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to Control Plane: %w", err)
	}
	defer conn.Close()
	logger.Info("Connected to Control Plane")

	// Send registration
	registration := model.AgentRegistration{
		ID:              cfg.AgentID,
		Name:            cfg.AgentName,
		ClusterName:     cfg.ClusterName,
		ClusterProvider: cfg.ClusterProvider,
		Region:          cfg.Region,
		Version:         "0.1.0",
		Labels:          map[string]string{},
		Capabilities:    []string{"k8s_crud"},
		Hostname:        hostname,
		Namespace:       cfg.Namespace,
		Metadata:        map[string]string{},
	}

	if err := conn.WriteJSON(registration); err != nil {
		return fmt.Errorf("failed to send registration: %w", err)
	}

	var response map[string]string
	if err := conn.ReadJSON(&response); err != nil {
		return fmt.Errorf("failed to read registration response: %w", err)
	}

	if response["status"] != "registered" {
		return fmt.Errorf("registration failed: %s", response["error"])
	}

	logger.Info("Agent registered successfully")

	// Start heartbeat goroutine
	stopHeartbeat := make(chan struct{})
	go sendHeartbeat(conn, cfg.HeartbeatInterval, stopHeartbeat)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Message processing loop
	go func() {
		for {
			var message map[string]interface{}
			if err := conn.ReadJSON(&message); err != nil {
				logger.Error("Error reading message", "error", err)
				return
			}

			msgType, ok := message["type"].(string)
			if !ok {
				continue
			}

			switch msgType {
			case "event":
				go handleEvent(conn, message, k8sExecutor)
			default:
				logger.Warn("Unknown message type", "type", msgType)
			}
		}
	}()

	logger.Info("Agent started successfully, waiting for events...")

	<-sigChan
	logger.Info("Shutting down agent...")
	close(stopHeartbeat)

	conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	time.Sleep(1 * time.Second)

	return nil
}

func sendHeartbeat(conn *websocket.Conn, interval time.Duration, stop chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			heartbeat := map[string]interface{}{
				"type":      "heartbeat",
				"timestamp": time.Now(),
				"metrics":   map[string]interface{}{},
			}
			if err := conn.WriteJSON(heartbeat); err != nil {
				logger.Error("Failed to send heartbeat", "error", err)
				return
			}
			logger.Debug("Heartbeat sent")

		case <-stop:
			return
		}
	}
}

func handleEvent(conn *websocket.Conn, message map[string]interface{}, k8sExecutor *executor.K8sExecutor) {
	eventData, err := json.Marshal(message["event"])
	if err != nil {
		logger.Error("Failed to marshal event", "error", err)
		return
	}

	var event model.Event
	if err := json.Unmarshal(eventData, &event); err != nil {
		logger.Error("Failed to unmarshal event", "error", err)
		return
	}

	logger.Info("Received event", "event_id", event.ID, "type", event.Type)

	sendStatusUpdate(conn, &event, model.StateInProgress, model.PhaseReceived, "Event received, starting execution", nil, nil)

	if err := event.Validate(); err != nil {
		logger.Error("Event validation failed", "event_id", event.ID, "error", err)
		sendStatusUpdate(conn, &event, model.StateFailed, model.PhaseFailed, err.Error(), nil, nil)
		return
	}

	sendStatusUpdate(conn, &event, model.StateInProgress, model.PhaseValidating, "Validating event payload", nil, nil)

	if event.Type == model.EventTypeK8sResource {
		if err := k8sExecutor.ValidateManifests(event.Payload.Manifests); err != nil {
			logger.Error("Manifest validation failed", "event_id", event.ID, "error", err)
			sendStatusUpdate(conn, &event, model.StateFailed, model.PhaseFailed, fmt.Sprintf("Manifest validation failed: %v", err), nil, nil)
			return
		}
	}

	sendStatusUpdate(conn, &event, model.StateInProgress, model.PhaseApplying, "Applying changes to cluster", nil, nil)

	result, err := k8sExecutor.ExecuteEvent(&event)
	if err != nil {
		logger.Error("Event execution failed", "event_id", event.ID, "error", err)
		sendStatusUpdate(conn, &event, model.StateFailed, model.PhaseFailed, err.Error(), nil, nil)
		return
	}

	sendStatusUpdate(conn, &event, model.StateInProgress, model.PhaseVerifying, "Verifying changes", nil, nil)

	// TODO: Add actual verification logic here
	time.Sleep(1 * time.Second)

	if result.Success {
		logger.Info("Event completed successfully", "event_id", event.ID)
		sendStatusUpdate(conn, &event, model.StateCompleted, model.PhaseCompleted, "Event completed successfully", result, nil)
	} else {
		logger.Error("Event failed", "event_id", event.ID, "error", result.ErrorMessage)
		sendStatusUpdate(conn, &event, model.StateFailed, model.PhaseFailed, result.ErrorMessage, result, nil)
	}
}

func sendStatusUpdate(conn *websocket.Conn, event *model.Event, state model.ExecutionState, phase model.ExecutionPhase,
	message string, result *model.EventResult, details map[string]interface{}) {

	update := model.StatusUpdate{
		EventID:   event.ID,
		AgentID:   event.TargetAgent,
		State:     state,
		Phase:     phase,
		Message:   message,
		LogLevel:  model.LogLevelInfo,
		Details:   details,
		Result:    result,
		Timestamp: time.Now(),
	}

	statusMsg := map[string]interface{}{
		"type":      "status_update",
		"event_id":  update.EventID,
		"agent_id":  update.AgentID,
		"state":     update.State,
		"phase":     update.Phase,
		"message":   update.Message,
		"log_level": update.LogLevel,
		"details":   update.Details,
		"result":    update.Result,
		"timestamp": update.Timestamp,
	}

	if err := conn.WriteJSON(statusMsg); err != nil {
		logger.Error("Failed to send status update", "event_id", event.ID, "error", err)
	}
}

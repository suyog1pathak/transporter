package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/suyog1pathak/transporter/model"
	"github.com/suyog1pathak/transporter/pkg/executor"
	"github.com/suyog1pathak/transporter/pkg/logger"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Start the Data Plane Agent",
	Long:  `Start the Transporter Data Plane Agent which executes events from the Control Plane.`,
	RunE:  runAgent,
}

var agentConfig struct {
	// Agent Identity
	agentID         string
	agentName       string
	clusterName     string
	clusterProvider string
	region          string
	namespace       string

	// Control Plane Connection
	cpURL string

	// Kubernetes Config
	kubeconfigPath string
	inCluster      bool

	// Heartbeat
	heartbeatInterval time.Duration
}

func init() {
	rootCmd.AddCommand(agentCmd)

	// Agent identity flags
	agentCmd.Flags().StringVar(&agentConfig.agentID, "agent-id", "", "Unique agent ID (required)")
	agentCmd.Flags().StringVar(&agentConfig.agentName, "agent-name", "", "Human-friendly agent name")
	agentCmd.Flags().StringVar(&agentConfig.clusterName, "cluster-name", "", "Kubernetes cluster name (required)")
	agentCmd.Flags().StringVar(&agentConfig.clusterProvider, "cluster-provider", "kind", "Cluster provider (eks, gke, aks, kind)")
	agentCmd.Flags().StringVar(&agentConfig.region, "region", "local", "Cluster region")
	agentCmd.Flags().StringVar(&agentConfig.namespace, "namespace", "default", "Namespace where agent is running")

	// Connection flags
	agentCmd.Flags().StringVar(&agentConfig.cpURL, "cp-url", "ws://localhost:8080/ws", "Control Plane WebSocket URL")

	// Kubernetes flags
	agentCmd.Flags().StringVar(&agentConfig.kubeconfigPath, "kubeconfig", "", "Path to kubeconfig file")
	agentCmd.Flags().BoolVar(&agentConfig.inCluster, "in-cluster", false, "Use in-cluster Kubernetes config")

	// Heartbeat
	agentCmd.Flags().DurationVar(&agentConfig.heartbeatInterval, "heartbeat-interval", 10*time.Second, "Heartbeat interval")

	// Mark required flags
	agentCmd.MarkFlagRequired("agent-id")
	agentCmd.MarkFlagRequired("cluster-name")

	// Bind flags to viper
	viper.BindPFlags(agentCmd.Flags())
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Initialize logger
	logger.InitLogger(viper.GetBool("debug"))
	logger.Info("🚀 Starting Transporter Agent", "agent_id", agentConfig.agentID)

	// Set defaults
	if agentConfig.agentName == "" {
		agentConfig.agentName = agentConfig.agentID
	}

	// Get hostname
	hostname, _ := os.Hostname()

	// Initialize Kubernetes executor
	logger.Info("☸️  Initializing Kubernetes executor")
	k8sExecutor, err := executor.NewK8sExecutor(executor.Config{
		KubeconfigPath: agentConfig.kubeconfigPath,
		InCluster:      agentConfig.inCluster,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize Kubernetes executor: %w", err)
	}
	logger.Info("✅ Kubernetes executor initialized")

	// Connect to Control Plane
	logger.Info("🔗 Connecting to Control Plane", "url", agentConfig.cpURL)
	conn, _, err := websocket.DefaultDialer.Dial(agentConfig.cpURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to Control Plane: %w", err)
	}
	defer conn.Close()
	logger.Info("✅ Connected to Control Plane")

	// Send registration
	registration := model.AgentRegistration{
		ID:              agentConfig.agentID,
		Name:            agentConfig.agentName,
		ClusterName:     agentConfig.clusterName,
		ClusterProvider: agentConfig.clusterProvider,
		Region:          agentConfig.region,
		Version:         "0.1.0",
		Labels:          map[string]string{},
		Capabilities:    []string{"k8s_crud"},
		Hostname:        hostname,
		Namespace:       agentConfig.namespace,
		Metadata:        map[string]string{},
	}

	if err := conn.WriteJSON(registration); err != nil {
		return fmt.Errorf("failed to send registration: %w", err)
	}

	// Wait for registration confirmation
	var response map[string]string
	if err := conn.ReadJSON(&response); err != nil {
		return fmt.Errorf("failed to read registration response: %w", err)
	}

	if response["status"] != "registered" {
		return fmt.Errorf("registration failed: %s", response["error"])
	}

	logger.Info("✅ Agent registered successfully")

	// Start heartbeat goroutine
	stopHeartbeat := make(chan struct{})
	go sendHeartbeat(conn, agentConfig.heartbeatInterval, stopHeartbeat)

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

			// Handle different message types
			msgType, ok := message["type"].(string)
			if !ok {
				continue
			}

			switch msgType {
			case "event":
				// Handle event execution
				go handleEvent(conn, message, k8sExecutor)
			default:
				logger.Warn("Unknown message type", "type", msgType)
			}
		}
	}()

	logger.Info("✅ Agent started successfully, waiting for events...")

	// Wait for shutdown signal
	<-sigChan
	logger.Info("🛑 Shutting down agent...")
	close(stopHeartbeat)

	// Send close message
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
			logger.Debug("💓 Heartbeat sent")

		case <-stop:
			return
		}
	}
}

func handleEvent(conn *websocket.Conn, message map[string]interface{}, k8sExecutor *executor.K8sExecutor) {
	// Extract event from message
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

	logger.Info("📥 Received event", "event_id", event.ID, "type", event.Type)

	// Send initial status update
	sendStatusUpdate(conn, &event, model.StateInProgress, model.PhaseReceived, "Event received, starting execution", nil, nil)

	// Validate event
	if err := event.Validate(); err != nil {
		logger.Error("Event validation failed", "event_id", event.ID, "error", err)
		sendStatusUpdate(conn, &event, model.StateFailed, model.PhaseFailed, err.Error(), nil, nil)
		return
	}

	// Validating phase
	sendStatusUpdate(conn, &event, model.StateInProgress, model.PhaseValidating, "Validating event payload", nil, nil)

	// For K8s resources, validate manifests
	if event.Type == model.EventTypeK8sResource {
		if err := k8sExecutor.ValidateManifests(event.Payload.Manifests); err != nil {
			logger.Error("Manifest validation failed", "event_id", event.ID, "error", err)
			sendStatusUpdate(conn, &event, model.StateFailed, model.PhaseFailed, fmt.Sprintf("Manifest validation failed: %v", err), nil, nil)
			return
		}
	}

	// Applying phase
	sendStatusUpdate(conn, &event, model.StateInProgress, model.PhaseApplying, "Applying changes to cluster", nil, nil)

	// Execute the event
	result, err := k8sExecutor.ExecuteEvent(&event)
	if err != nil {
		logger.Error("Event execution failed", "event_id", event.ID, "error", err)
		sendStatusUpdate(conn, &event, model.StateFailed, model.PhaseFailed, err.Error(), nil, nil)
		return
	}

	// Verifying phase
	sendStatusUpdate(conn, &event, model.StateInProgress, model.PhaseVerifying, "Verifying changes", nil, nil)

	// TODO: Add actual verification logic here
	time.Sleep(1 * time.Second)

	// Send final status based on result
	if result.Success {
		logger.Info("✅ Event completed successfully", "event_id", event.ID)
		sendStatusUpdate(conn, &event, model.StateCompleted, model.PhaseCompleted, "Event completed successfully", result, nil)
	} else {
		logger.Error("❌ Event failed", "event_id", event.ID, "error", result.ErrorMessage)
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
		"type":           "status_update",
		"event_id":       update.EventID,
		"agent_id":       update.AgentID,
		"state":          update.State,
		"phase":          update.Phase,
		"message":        update.Message,
		"log_level":      update.LogLevel,
		"details":        update.Details,
		"result":         update.Result,
		"timestamp":      update.Timestamp,
	}

	if err := conn.WriteJSON(statusMsg); err != nil {
		logger.Error("Failed to send status update", "event_id", event.ID, "error", err)
	}
}

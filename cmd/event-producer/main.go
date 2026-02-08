package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/suyog1pathak/transporter/model"
	"github.com/suyog1pathak/transporter/pkg/queue"
	"gopkg.in/yaml.v3"
)

var (
	// Connection mode
	mode string // "memphis" or "websocket"
	cpURL string // Control Plane WebSocket URL for direct mode

	// Memphis connection
	memphisHost     string
	memphisUsername string
	memphisPassword string
	memphisStation  string
	memphisAccountID int

	// Event metadata
	targetAgent string
	createdBy   string
	ttl         time.Duration
	priority    int

	rootCmd = &cobra.Command{
		Use:   "event-producer",
		Short: "Transporter Event Producer - Create and publish events",
		Long: `Event Producer is a CLI tool for creating and publishing events to the Transporter Control Plane.
It supports creating Kubernetes resource events from YAML manifests.

Modes:
  websocket - Send events directly to Control Plane via WebSocket (for testing)
  memphis   - Publish events to Memphis queue (production mode)`,
		Version: "0.1.0",
	}
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// Connection mode
	rootCmd.PersistentFlags().StringVar(&mode, "mode", "http", "Connection mode: http or memphis")
	rootCmd.PersistentFlags().StringVar(&cpURL, "cp-url", "http://localhost:8080", "Control Plane URL (for http mode)")

	// Memphis flags (for memphis mode)
	rootCmd.PersistentFlags().StringVar(&memphisHost, "memphis-host", "localhost:6666", "Memphis server host:port")
	rootCmd.PersistentFlags().StringVar(&memphisUsername, "memphis-username", "root", "Memphis username")
	rootCmd.PersistentFlags().StringVar(&memphisPassword, "memphis-password", "memphis", "Memphis password")
	rootCmd.PersistentFlags().StringVar(&memphisStation, "memphis-station", "transporter-events", "Memphis station name")
	rootCmd.PersistentFlags().IntVar(&memphisAccountID, "memphis-account-id", 1, "Memphis account ID")

	// Event metadata
	rootCmd.PersistentFlags().StringVar(&targetAgent, "agent", "", "Target agent ID (required)")
	rootCmd.PersistentFlags().StringVar(&createdBy, "created-by", "cli", "Event creator")
	rootCmd.PersistentFlags().DurationVar(&ttl, "ttl", 24*time.Hour, "Event time-to-live")
	rootCmd.PersistentFlags().IntVar(&priority, "priority", 0, "Event priority")

	rootCmd.MarkPersistentFlagRequired("agent")

	// Add subcommands
	rootCmd.AddCommand(createK8sEventCmd())
	rootCmd.AddCommand(createFromFileCmd())
}

func createK8sEventCmd() *cobra.Command {
	var manifestFiles []string

	cmd := &cobra.Command{
		Use:   "k8s",
		Short: "Create a Kubernetes resource event",
		Long:  `Create an event to apply Kubernetes YAML manifests on target agent.`,
		Example: `  # Create event from single manifest
  event-producer k8s --agent agent-1 --manifest namespace.yaml

  # Create event from multiple manifests
  event-producer k8s --agent agent-1 --manifest ns.yaml --manifest deployment.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(manifestFiles) == 0 {
				return fmt.Errorf("at least one manifest file is required")
			}

			// Read manifest files
			manifests := make([]string, 0, len(manifestFiles))
			for _, file := range manifestFiles {
				data, err := os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("failed to read manifest file %s: %w", file, err)
				}
				manifests = append(manifests, string(data))
			}

			// Create event
			event := model.NewEvent(
				model.EventTypeK8sResource,
				targetAgent,
				model.EventPayload{
					Manifests: manifests,
				},
				createdBy,
			)
			event.TTL = ttl
			event.Priority = priority

			// Publish event
			return publishEvent(event)
		},
	}

	cmd.Flags().StringSliceVarP(&manifestFiles, "manifest", "m", []string{}, "Path to Kubernetes YAML manifest file (can be specified multiple times)")
	cmd.MarkFlagRequired("manifest")

	return cmd
}

func createFromFileCmd() *cobra.Command {
	var eventFile string

	cmd := &cobra.Command{
		Use:   "from-file",
		Short: "Create event from JSON or YAML file",
		Long:  `Create an event from a pre-defined JSON or YAML event file.`,
		Example: `  # Create from JSON
  event-producer from-file --file event.json

  # Create from YAML
  event-producer from-file --file event.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(eventFile)
			if err != nil {
				return fmt.Errorf("failed to read event file: %w", err)
			}

			var event model.Event

			// Try JSON first
			if err := json.Unmarshal(data, &event); err != nil {
				// Try YAML
				if err := yaml.Unmarshal(data, &event); err != nil {
					return fmt.Errorf("failed to parse event file as JSON or YAML: %w", err)
				}
			}

			// Override if not set
			if event.ID == "" {
				event.ID = uuid.New().String()
			}
			if event.CreatedAt.IsZero() {
				event.CreatedAt = time.Now()
			}

			// Validate
			if err := event.Validate(); err != nil {
				return fmt.Errorf("event validation failed: %w", err)
			}

			// Publish event
			return publishEvent(&event)
		},
	}

	cmd.Flags().StringVarP(&eventFile, "file", "f", "", "Path to event file (JSON or YAML)")
	cmd.MarkFlagRequired("file")

	return cmd
}

func publishEvent(event *model.Event) error {
	fmt.Printf("📤 Publishing event %s to agent %s\n", event.ID, event.TargetAgent)

	var err error
	switch mode {
	case "http":
		err = publishViaHTTP(event)
	case "memphis":
		err = publishViaMemphis(event)
	default:
		return fmt.Errorf("invalid mode: %s (must be 'http' or 'memphis')", mode)
	}

	if err != nil {
		return err
	}

	fmt.Printf("✅ Event published successfully!\n")
	fmt.Printf("\nEvent Details:\n")
	fmt.Printf("  ID:           %s\n", event.ID)
	fmt.Printf("  Type:         %s\n", event.Type)
	fmt.Printf("  Target Agent: %s\n", event.TargetAgent)
	fmt.Printf("  Created By:   %s\n", event.CreatedBy)
	fmt.Printf("  Created At:   %s\n", event.CreatedAt.Format(time.RFC3339))
	fmt.Printf("  TTL:          %s\n", event.TTL)

	if event.Type == model.EventTypeK8sResource {
		fmt.Printf("  Manifests:    %d file(s)\n", len(event.Payload.Manifests))
	}

	return nil
}

// publishViaHTTP sends event directly to Control Plane via HTTP POST
func publishViaHTTP(event *model.Event) error {
	// Ensure CP URL doesn't have trailing slash
	cpURL = strings.TrimSuffix(cpURL, "/")
	eventsURL := cpURL + "/events"

	fmt.Printf("🔌 Sending event to Control Plane at %s...\n", eventsURL)

	// Serialize event to JSON
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to serialize event: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", eventsURL, bytes.NewBuffer(eventJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send request
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send event: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, _ := io.ReadAll(resp.Body)

	// Check status
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("CP returned error (status %d): %s", resp.StatusCode, string(body))
	}

	fmt.Println("✅ Event accepted by Control Plane")

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(body, &response); err == nil {
		if msg, ok := response["message"].(string); ok {
			fmt.Printf("   %s\n", msg)
		}
	}

	return nil
}

// publishViaMemphis publishes event to Memphis queue
func publishViaMemphis(event *model.Event) error {
	fmt.Printf("🔌 Connecting to Memphis at %s...\n", memphisHost)

	memphisQueue, err := queue.NewMemphisQueue(queue.Config{
		Host:        memphisHost,
		Username:    memphisUsername,
		Password:    memphisPassword,
		StationName: memphisStation,
		AccountID:   memphisAccountID,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to Memphis: %w", err)
	}
	defer memphisQueue.Close()

	fmt.Println("✅ Connected to Memphis")

	// Publish event
	if err := memphisQueue.ProduceEvent(event); err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	return nil
}

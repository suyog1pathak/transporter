package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/suyog1pathak/transporter/internal/agent"
	"github.com/suyog1pathak/transporter/internal/controlplane"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "transporter",
	Short: "Event-driven multi-cluster Kubernetes management",
	Long: `Transporter is a lightweight, event-driven system that enables platform teams
to manage Kubernetes resources across multiple clusters from a centralized control plane.`,
	Version: "0.1.0",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.transporter.yaml)")
	rootCmd.PersistentFlags().Bool("debug", false, "enable debug logging")
	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))

	rootCmd.AddCommand(newControlPlaneCmd())
	rootCmd.AddCommand(newAgentCmd())
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".transporter")
	}
	viper.SetEnvPrefix("TRANSPORTER")
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

func newControlPlaneCmd() *cobra.Command {
	var cfg controlplane.Config

	cmd := &cobra.Command{
		Use:   "control-plane",
		Short: "Start the Control Plane",
		Long:  `Start the Transporter Control Plane which manages agents and routes events.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Debug = viper.GetBool("debug")
			return controlplane.Run(cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.WSAddr, "ws-addr", "0.0.0.0", "WebSocket server address")
	cmd.Flags().IntVar(&cfg.WSPort, "ws-port", 8080, "WebSocket server port")

	cmd.Flags().BoolVar(&cfg.MemphisEnabled, "memphis-enabled", true, "Enable Memphis queue integration")
	cmd.Flags().StringVar(&cfg.MemphisHost, "memphis-host", "localhost", "Memphis server hostname")
	cmd.Flags().StringVar(&cfg.MemphisUsername, "memphis-username", "root", "Memphis username")
	cmd.Flags().StringVar(&cfg.MemphisPassword, "memphis-password", "memphis", "Memphis password (alternative to connection token)")
	cmd.Flags().StringVar(&cfg.MemphisConnectionToken, "memphis-connection-token", "", "Memphis connection token (preferred over password)")
	cmd.Flags().StringVar(&cfg.MemphisStation, "memphis-station", "transporter-events", "Memphis station name")
	cmd.Flags().IntVar(&cfg.MemphisAccountID, "memphis-account-id", 0, "Memphis account ID (optional)")

	cmd.Flags().StringVar(&cfg.RedisAddr, "redis-addr", "localhost:6379", "Redis server address")
	cmd.Flags().StringVar(&cfg.RedisPassword, "redis-password", "", "Redis password")
	cmd.Flags().IntVar(&cfg.RedisDB, "redis-db", 0, "Redis database number")

	cmd.Flags().DurationVar(&cfg.HeartbeatTimeout, "heartbeat-timeout", 30*time.Second, "Agent heartbeat timeout")
	cmd.Flags().IntVar(&cfg.EventRetryMax, "event-retry-max", 3, "Maximum event retry attempts")

	viper.BindPFlags(cmd.Flags())

	return cmd
}

func newAgentCmd() *cobra.Command {
	var cfg agent.Config

	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Start the Data Plane Agent",
		Long:  `Start the Transporter Data Plane Agent which executes events from the Control Plane.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Debug = viper.GetBool("debug")
			return agent.Run(cfg)
		},
	}

	cmd.Flags().StringVar(&cfg.AgentID, "agent-id", "", "Unique agent ID (required)")
	cmd.Flags().StringVar(&cfg.AgentName, "agent-name", "", "Human-friendly agent name")
	cmd.Flags().StringVar(&cfg.ClusterName, "cluster-name", "", "Kubernetes cluster name (required)")
	cmd.Flags().StringVar(&cfg.ClusterProvider, "cluster-provider", "kind", "Cluster provider (eks, gke, aks, kind)")
	cmd.Flags().StringVar(&cfg.Region, "region", "local", "Cluster region")
	cmd.Flags().StringVar(&cfg.Namespace, "namespace", "default", "Namespace where agent is running")
	cmd.Flags().StringVar(&cfg.CPURL, "cp-url", "ws://localhost:8080/ws", "Control Plane WebSocket URL")
	cmd.Flags().StringVar(&cfg.KubeconfigPath, "kubeconfig", "", "Path to kubeconfig file")
	cmd.Flags().BoolVar(&cfg.InCluster, "in-cluster", false, "Use in-cluster Kubernetes config")
	cmd.Flags().DurationVar(&cfg.HeartbeatInterval, "heartbeat-interval", 10*time.Second, "Heartbeat interval")

	cmd.MarkFlagRequired("agent-id")
	cmd.MarkFlagRequired("cluster-name")

	viper.BindPFlags(cmd.Flags())

	return cmd
}

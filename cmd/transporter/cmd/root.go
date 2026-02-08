package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	rootCmd = &cobra.Command{
		Use:   "transporter",
		Short: "Event-driven multi-cluster Kubernetes management",
		Long: `Transporter is a lightweight, event-driven system that enables platform teams
to manage Kubernetes resources across multiple clusters from a centralized control plane.

It's designed for environments where direct cluster API access is restricted
(air-gapped clusters, strict security policies, network isolation).`,
		Version: "0.1.0",
	}
)

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.transporter.yaml)")
	rootCmd.PersistentFlags().Bool("debug", false, "enable debug logging")

	viper.BindPFlag("debug", rootCmd.PersistentFlags().Lookup("debug"))
}

func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".transporter" (without extension)
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".transporter")
	}

	// Read environment variables
	viper.SetEnvPrefix("TRANSPORTER")
	viper.AutomaticEnv()

	// If a config file is found, read it in
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

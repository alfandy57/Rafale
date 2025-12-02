// Package cli implements the Rafale command-line interface.
package cli

import (
	"fmt"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/0xredeth/Rafale/internal/version"
)

var (
	cfgFile string
	verbose bool
)

// rootCmd is the base command for Rafale CLI.
var rootCmd = &cobra.Command{
	Use:   "rafale",
	Short: "Lightweight event indexer for Linea zkEVM",
	Long: `Rafale is a minimal, high-performance blockchain event indexer
built specifically for Linea zkEVM. It indexes smart contract events
into PostgreSQL with TimescaleDB and exposes them via GraphQL API.

A burst of blockchain events. Index fast. Query faster.`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version.Version, version.Commit, version.Date),
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		setupLogging()
	},
}

// Execute runs the root command.
//
// Returns:
//   - error: nil on success, command execution error on failure
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./rafale.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose logging")

	// Bind flags to viper
	_ = viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
}

// initConfig reads in config file and ENV variables.
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("rafale")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("$HOME/.rafale")
	}

	viper.SetEnvPrefix("RAFALE")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintf(os.Stderr, "Error reading config: %v\n", err)
		}
	}
}

// setupLogging configures zerolog based on verbosity.
func setupLogging() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	if verbose {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Pretty console output for development
	if os.Getenv("RAFALE_ENV") != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
}

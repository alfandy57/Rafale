package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/0xredeth/Rafale/pkg/config"
	"github.com/0xredeth/Rafale/internal/store"
)

var forceReset bool

// resetCmd resets indexed data.
var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset all indexed data",
	Long: `Reset all indexed data from the database.
This will truncate all event tables and force a re-sync from start blocks.

WARNING: This action is irreversible!`,
	RunE: runReset,
}

func init() {
	rootCmd.AddCommand(resetCmd)

	resetCmd.Flags().BoolVarP(&forceReset, "force", "f", false, "skip confirmation prompt")
}

// runReset executes the reset command.
//
// Parameters:
//   - cmd (*cobra.Command): the cobra command
//   - args ([]string): command arguments
//
// Returns:
//   - error: nil on success, reset error on failure
func runReset(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if !forceReset {
		fmt.Print("This will delete all indexed data. Are you sure? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Reset cancelled.")
			return nil
		}
	}

	log.Warn().
		Str("network", cfg.Network).
		Msg("resetting indexed data")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to database
	storeCfg := store.DefaultConfig()
	storeCfg.DSN = cfg.Database

	db, err := store.New(storeCfg)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()

	// Execute reset
	if err := db.Reset(ctx); err != nil {
		return fmt.Errorf("resetting data: %w", err)
	}

	fmt.Println("Reset complete. All indexed data has been cleared.")
	fmt.Println("Run 'rafale start' to begin re-indexing from start blocks.")

	return nil
}

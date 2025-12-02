package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/0xredeth/Rafale/pkg/config"
	"github.com/0xredeth/Rafale/internal/rpc"
	"github.com/0xredeth/Rafale/internal/store"
)

// statusCmd shows sync status.
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show indexer sync status",
	Long:  `Display current synchronization status including indexed block height and sync lag.`,
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

// runStatus executes the status command.
//
// Parameters:
//   - cmd (*cobra.Command): the cobra command
//   - args ([]string): command arguments
//
// Returns:
//   - error: nil on success, status retrieval error on failure
func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

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

	// Get indexed block
	indexedBlock, err := db.GetMaxBlockNumber(ctx, "transfers")
	if err != nil {
		return fmt.Errorf("getting max block: %w", err)
	}

	// Get transfer count
	transferCount, err := db.GetTransferCount(ctx)
	if err != nil {
		return fmt.Errorf("getting transfer count: %w", err)
	}

	// Get chain head via RPC
	rpcCfg := rpc.DefaultConfig()
	rpcCfg.URL = cfg.RPCURL

	rpcClient, err := rpc.New(ctx, rpcCfg)
	if err != nil {
		return fmt.Errorf("connecting to RPC: %w", err)
	}
	defer rpcClient.Close()

	chainHead, err := rpcClient.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("getting chain head: %w", err)
	}

	// Calculate lag
	var lag uint64
	if chainHead > indexedBlock {
		lag = chainHead - indexedBlock
	}

	// Determine status
	status := "Idle"
	if indexedBlock == 0 {
		status = "Not started"
	} else if lag > 100 {
		status = "Syncing"
	} else if lag > 0 {
		status = "Catching up"
	} else {
		status = "Synced"
	}

	// Display status
	fmt.Println()
	fmt.Println("Sync Status")
	fmt.Println("===========")
	fmt.Printf("Network:       %s\n", cfg.Network)
	fmt.Printf("Chain ID:      %d\n", cfg.ChainID)
	fmt.Printf("Current Block: %d\n", indexedBlock)
	fmt.Printf("Chain Head:    %d\n", chainHead)
	fmt.Printf("Lag:           %d blocks\n", lag)
	fmt.Printf("Transfers:     %d indexed\n", transferCount)
	fmt.Printf("Status:        %s\n", status)
	fmt.Println()

	return nil
}

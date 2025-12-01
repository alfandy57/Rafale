// Package engine provides the unified sync loop for Rafale.
package engine

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/0xredeth/Rafale/pkg/config"
	"github.com/0xredeth/Rafale/pkg/decoder"
	"github.com/0xredeth/Rafale/pkg/handler"
	"github.com/0xredeth/Rafale/pkg/rpc"
	"github.com/0xredeth/Rafale/pkg/store"
)

// Metrics for engine monitoring.
var (
	blocksIndexed = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "rafale_blocks_indexed_total",
			Help: "Total number of blocks indexed",
		},
	)

	syncLag = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "rafale_sync_lag_blocks",
			Help: "Number of blocks behind chain head",
		},
	)

	currentBlock = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "rafale_current_block",
			Help: "Current indexed block number",
		},
	)
)

// Engine orchestrates the sync loop.
type Engine struct {
	cfg      *config.Config
	rpc      *rpc.Client
	store    *store.Store
	decoder  *decoder.Decoder
	handlers *handler.Registry

	// State
	lastBlock uint64
}

// New creates a new engine instance.
//
// Parameters:
//   - cfg (*config.Config): configuration
//
// Returns:
//   - *Engine: initialized engine
//   - error: nil on success, initialization error on failure
func New(cfg *config.Config) (*Engine, error) {
	// Initialize RPC client
	rpcCfg := rpc.DefaultConfig()
	rpcCfg.URL = cfg.RPCURL

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rpcClient, err := rpc.New(ctx, rpcCfg)
	if err != nil {
		return nil, fmt.Errorf("creating RPC client: %w", err)
	}

	// Verify chain ID
	if rpcClient.ChainID().Uint64() != cfg.ChainID {
		rpcClient.Close()
		return nil, fmt.Errorf("chain ID mismatch: expected %d, got %d", cfg.ChainID, rpcClient.ChainID().Uint64())
	}

	// Initialize store
	storeCfg := store.DefaultConfig()
	storeCfg.DSN = cfg.Database

	db, err := store.New(storeCfg)
	if err != nil {
		rpcClient.Close()
		return nil, fmt.Errorf("creating store: %w", err)
	}

	// Auto-migrate event tables
	if err := db.Migrate(
		&store.SyncStatus{},
		&store.IndexerMeta{},
		&store.Transfer{},
	); err != nil {
		db.Close()
		rpcClient.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	log.Info().Msg("database migrations complete")

	// Setup TimescaleDB optimizations (hypertable + compression + retention)
	tsCfg := store.DefaultTimescaleConfig()
	if err := db.SetupTimescaleDB(context.Background(), "transfers", "timestamp", tsCfg); err != nil {
		log.Warn().Err(err).Msg("TimescaleDB setup warning (non-fatal)")
	}

	// Initialize decoder
	dec := decoder.New()

	// Register contracts from config
	for name, contract := range cfg.Contracts {
		abiJSON, err := os.ReadFile(contract.ABI)
		if err != nil {
			db.Close()
			rpcClient.Close()
			return nil, fmt.Errorf("reading ABI for %s: %w", name, err)
		}

		addr := common.HexToAddress(contract.Address)
		if err := dec.RegisterContract(name, addr, string(abiJSON), contract.Events); err != nil {
			db.Close()
			rpcClient.Close()
			return nil, fmt.Errorf("registering contract %s: %w", name, err)
		}

		log.Info().
			Str("contract", name).
			Str("address", contract.Address).
			Int("events", len(contract.Events)).
			Msg("registered contract")
	}

	return &Engine{
		cfg:      cfg,
		rpc:      rpcClient,
		store:    db,
		decoder:  dec,
		handlers: handler.Global(),
	}, nil
}

// Run starts the sync loop.
//
// Parameters:
//   - ctx (context.Context): context for cancellation
//
// Returns:
//   - error: nil on graceful shutdown, error on failure
func (e *Engine) Run(ctx context.Context) error {
	log.Info().
		Str("network", e.cfg.Network).
		Uint64("chainID", e.cfg.ChainID).
		Msg("starting sync engine")

	// Determine start block
	startBlock, err := e.determineStartBlock(ctx)
	if err != nil {
		return fmt.Errorf("determining start block: %w", err)
	}

	e.lastBlock = startBlock
	log.Info().Uint64("startBlock", startBlock).Msg("resuming from block")

	// Start sync loop
	ticker := time.NewTicker(e.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("sync engine shutting down")
			return nil

		case <-ticker.C:
			if err := e.syncOnce(ctx); err != nil {
				log.Error().Err(err).Msg("sync error")
				// Continue on error - circuit breaker will handle RPC issues
			}
		}
	}
}

// syncOnce performs a single sync iteration.
func (e *Engine) syncOnce(ctx context.Context) error {
	// Get current chain head
	headBlock, err := e.rpc.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("getting block number: %w", err)
	}

	// Update sync lag metric
	lag := int64(headBlock) - int64(e.lastBlock)
	if lag < 0 {
		lag = 0
	}
	syncLag.Set(float64(lag))

	// Nothing to sync
	if e.lastBlock >= headBlock {
		return nil
	}

	// Calculate batch range
	fromBlock := e.lastBlock + 1
	toBlock := fromBlock + e.cfg.Sync.BatchSize - 1
	if toBlock > headBlock {
		toBlock = headBlock
	}

	log.Debug().
		Uint64("from", fromBlock).
		Uint64("to", toBlock).
		Uint64("head", headBlock).
		Msg("syncing blocks")

	// Fetch and process logs
	if err := e.processBlockRange(ctx, fromBlock, toBlock); err != nil {
		return fmt.Errorf("processing blocks %d-%d: %w", fromBlock, toBlock, err)
	}

	// Update state
	e.lastBlock = toBlock
	currentBlock.Set(float64(toBlock))
	blocksIndexed.Add(float64(toBlock - fromBlock + 1))

	return nil
}

// processBlockRange fetches and processes logs for a block range.
func (e *Engine) processBlockRange(ctx context.Context, fromBlock, toBlock uint64) error {
	// Build filter query
	addresses := e.decoder.GetAddresses()
	topics := [][]common.Hash{e.decoder.GetEventSignatures()}

	// Fetch logs with binary split on range errors
	logs, err := e.rpc.FetchLogs(ctx, addresses, topics, fromBlock, toBlock)
	if err != nil {
		return fmt.Errorf("fetching logs: %w", err)
	}

	if len(logs) == 0 {
		return nil
	}

	log.Debug().
		Uint64("from", fromBlock).
		Uint64("to", toBlock).
		Int("logs", len(logs)).
		Msg("fetched logs")

	// Process logs in a transaction
	return e.store.Transaction(ctx, func(tx *gorm.DB) error {
		for _, logEntry := range logs {
			if err := e.processLog(ctx, tx, logEntry); err != nil {
				return fmt.Errorf("processing log at block %d: %w", logEntry.BlockNumber, err)
			}
		}
		return nil
	})
}

// processLog decodes and handles a single log entry.
func (e *Engine) processLog(ctx context.Context, tx *gorm.DB, logEntry types.Log) error {
	// Decode the event
	event, err := e.decoder.Decode(logEntry)
	if err != nil {
		log.Warn().
			Err(err).
			Str("txHash", logEntry.TxHash.Hex()).
			Uint64("block", logEntry.BlockNumber).
			Msg("failed to decode log")
		return nil // Skip unknown events
	}

	// Get block info for context
	header, err := e.rpc.HeaderByNumber(ctx, new(big.Int).SetUint64(logEntry.BlockNumber))
	if err != nil {
		return fmt.Errorf("getting block header: %w", err)
	}

	// Build handler context
	handlerCtx := &handler.Context{
		DB: tx,
		Block: handler.BlockInfo{
			Number:     logEntry.BlockNumber,
			Hash:       header.Hash().Hex(),
			Time:       time.Unix(int64(header.Time), 0),
			ParentHash: header.ParentHash.Hex(),
		},
		Log:   logEntry,
		Event: event,
	}

	// Execute handler
	if err := e.handlers.Handle(handlerCtx); err != nil {
		return fmt.Errorf("handling event %s: %w", event.EventID, err)
	}

	return nil
}

// determineStartBlock finds the starting block for sync.
// Uses MAX(block_number) from event tables per Rafale design.
func (e *Engine) determineStartBlock(ctx context.Context) (uint64, error) {
	// Query MAX(block_number) from transfers table
	maxBlock, err := e.store.GetMaxBlockNumber(ctx, "transfers")
	if err != nil {
		return 0, fmt.Errorf("getting max block: %w", err)
	}

	// If we have indexed data, resume from there
	if maxBlock > 0 {
		log.Info().
			Uint64("maxIndexedBlock", maxBlock).
			Msg("found existing indexed data, resuming")
		return maxBlock, nil // Start from last indexed block (syncOnce will add 1)
	}

	// Otherwise use minimum configured start_block
	var minConfiguredStart uint64 = ^uint64(0)
	for _, contract := range e.cfg.Contracts {
		if contract.StartBlock < minConfiguredStart {
			minConfiguredStart = contract.StartBlock
		}
	}

	if minConfiguredStart == ^uint64(0) {
		minConfiguredStart = 0
	}

	return minConfiguredStart, nil
}

// Close shuts down the engine.
//
// Returns:
//   - error: nil on success, close error on failure
func (e *Engine) Close() error {
	e.rpc.Close()
	return e.store.Close()
}

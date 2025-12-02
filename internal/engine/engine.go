// Package engine provides the unified sync loop for Rafale.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/0xredeth/Rafale/internal/api/graphql/model"
	"github.com/0xredeth/Rafale/internal/pubsub"
	"github.com/0xredeth/Rafale/internal/rpc"
	"github.com/0xredeth/Rafale/internal/store"
	"github.com/0xredeth/Rafale/pkg/config"
	"github.com/0xredeth/Rafale/pkg/decoder"
	"github.com/0xredeth/Rafale/pkg/handler"
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
	cfg         *config.Config
	rpc         *rpc.Client
	store       *store.Store
	decoder     *decoder.Decoder
	handlers    *handler.Registry
	broadcaster *pubsub.Broadcaster

	// State
	lastBlock uint64
}

// New creates a new engine instance.
//
// Parameters:
//   - cfg (*config.Config): configuration
//   - broadcaster (*pubsub.Broadcaster): pub/sub broadcaster for real-time subscriptions
//
// Returns:
//   - *Engine: initialized engine
//   - error: nil on success, initialization error on failure
func New(cfg *config.Config, broadcaster *pubsub.Broadcaster) (*Engine, error) {
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
		&store.Event{},
		&store.Transfer{},
	); err != nil {
		_ = db.Close()
		rpcClient.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	log.Info().Msg("database migrations complete")

	// Setup TimescaleDB optimizations (hypertable + compression + retention)
	tsCfg := store.DefaultTimescaleConfig()

	// Setup hypertable for generic events table
	if err := db.SetupTimescaleDB(context.Background(), "events", "timestamp", tsCfg); err != nil {
		log.Warn().Err(err).Msg("TimescaleDB setup for events table warning (non-fatal)")
	}

	// Setup hypertable for typed transfers table
	if err := db.SetupTimescaleDB(context.Background(), "transfers", "timestamp", tsCfg); err != nil {
		log.Warn().Err(err).Msg("TimescaleDB setup for transfers table warning (non-fatal)")
	}

	// Initialize decoder
	dec := decoder.New()

	// Register contracts from config
	for name, contract := range cfg.Contracts {
		abiJSON, err := os.ReadFile(contract.ABI)
		if err != nil {
			_ = db.Close()
			rpcClient.Close()
			return nil, fmt.Errorf("reading ABI for %s: %w", name, err)
		}

		addr := common.HexToAddress(contract.Address)
		if err := dec.RegisterContract(name, addr, string(abiJSON), contract.Events); err != nil {
			_ = db.Close()
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
		cfg:         cfg,
		rpc:         rpcClient,
		store:       db,
		decoder:     dec,
		handlers:    handler.Global(),
		broadcaster: broadcaster,
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
	lag := int64(headBlock) - int64(e.lastBlock) //nolint:gosec // G115: Block numbers won't overflow int64
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

	// Broadcast blocks to subscribers (if broadcaster is configured)
	if e.broadcaster != nil {
		// Broadcast the latest processed block
		header, err := e.rpc.HeaderByNumber(ctx, new(big.Int).SetUint64(toBlock))
		if err == nil && header != nil {
			e.broadcaster.BroadcastBlock(&model.Block{
				Number:     strconv.FormatUint(header.Number.Uint64(), 10),
				Hash:       header.Hash().Hex(),
				Timestamp:  time.Unix(int64(header.Time), 0), //nolint:gosec // G115: Timestamp won't overflow
				ParentHash: header.ParentHash.Hex(),
			})
		}
	}

	// Update state
	e.lastBlock = toBlock
	currentBlock.Set(float64(toBlock))
	blocksIndexed.Add(float64(toBlock - fromBlock + 1))

	// Broadcast sync status to subscribers (if broadcaster is configured)
	if e.broadcaster != nil {
		newLag := int64(headBlock) - int64(toBlock) //nolint:gosec // G115: Block numbers won't overflow int64
		if newLag < 0 {
			newLag = 0
		}
		e.broadcaster.BroadcastSyncStatus(&model.SyncStatus{
			Network:      e.cfg.Network,
			ChainID:      strconv.FormatUint(e.cfg.ChainID, 10),
			CurrentBlock: strconv.FormatUint(toBlock, 10),
			HeadBlock:    strconv.FormatUint(headBlock, 10),
			Lag:          strconv.FormatInt(newLag, 10),
			IsSynced:     newLag == 0,
			LastSyncTime: func() *time.Time { t := time.Now(); return &t }(),
		})
	}

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
// All decoded events are auto-stored in the generic events table.
// Typed handlers are optional and run only if registered.
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

	blockTime := time.Unix(int64(header.Time), 0) //nolint:gosec // G115: Timestamp won't overflow

	// Auto-store event in generic events table (always)
	if err := e.storeGenericEvent(tx, logEntry, event, blockTime); err != nil {
		return fmt.Errorf("storing generic event: %w", err)
	}

	// Broadcast event to subscribers (if broadcaster is configured)
	if e.broadcaster != nil {
		e.broadcaster.BroadcastEvent(&model.GenericEvent{
			ID:          "0", // ID not available until tx commits
			BlockNumber: strconv.FormatUint(logEntry.BlockNumber, 10),
			TxHash:      logEntry.TxHash.Hex(),
			TxIndex:     int(logEntry.TxIndex), //nolint:gosec // G115: TxIndex is small
			LogIndex:    int(logEntry.Index),   //nolint:gosec // G115: LogIndex is small
			Timestamp:   blockTime,
			Contract:    event.ContractName,
			EventName:   event.EventName,
			Data:        convertEventData(event.Data),
		})
	}

	// Build handler context for optional typed handlers
	handlerCtx := &handler.Context{
		DB: tx,
		Block: handler.BlockInfo{
			Number:     logEntry.BlockNumber,
			Hash:       header.Hash().Hex(),
			Time:       blockTime,
			ParentHash: header.ParentHash.Hex(),
		},
		Log:   logEntry,
		Event: event,
	}

	// Execute typed handler if registered (optional - for performance optimization)
	if e.handlers.HasHandler(event.EventID) {
		if err := e.handlers.Handle(handlerCtx); err != nil {
			return fmt.Errorf("handling event %s: %w", event.EventID, err)
		}
	}

	return nil
}

// storeGenericEvent saves a decoded event to the generic events table.
//
// Parameters:
//   - tx (*gorm.DB): database transaction
//   - logEntry (types.Log): raw Ethereum log
//   - event (*decoder.DecodedEvent): decoded event data
//   - blockTime (time.Time): block timestamp
//
// Returns:
//   - error: nil on success, error on failure
func (e *Engine) storeGenericEvent(tx *gorm.DB, logEntry types.Log, event *decoder.DecodedEvent, blockTime time.Time) error {
	// Serialize event data to JSON
	dataJSON, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("marshaling event data: %w", err)
	}

	genericEvent := &store.Event{
		BaseEvent: store.BaseEvent{
			BlockNumber: logEntry.BlockNumber,
			TxHash:      logEntry.TxHash.Hex(),
			TxIndex:     logEntry.TxIndex,
			LogIndex:    logEntry.Index,
			Timestamp:   blockTime,
		},
		ContractName: event.ContractName,
		ContractAddr: logEntry.Address.Hex(),
		EventName:    event.EventName,
		EventSig:     logEntry.Topics[0].Hex(),
		Data:         datatypes.JSON(dataJSON),
	}

	if err := tx.Create(genericEvent).Error; err != nil {
		return fmt.Errorf("inserting generic event: %w", err)
	}

	return nil
}

// determineStartBlock finds the starting block for sync.
// Uses MAX(block_number) from generic events table per Rafale design.
func (e *Engine) determineStartBlock(ctx context.Context) (uint64, error) {
	// Query MAX(block_number) from generic events table (source of truth)
	maxBlock, err := e.store.GetMaxBlockNumber(ctx, "events")
	if err != nil {
		return 0, fmt.Errorf("getting max block: %w", err)
	}

	// If we have indexed data, resume from there
	if maxBlock > 0 {
		log.Info().
			Uint64("maxIndexedBlock", maxBlock).
			Msg("found existing indexed data in events table, resuming")
		return maxBlock, nil // Start from last indexed block (syncOnce will add 1)
	}

	// Otherwise use minimum configured start_block
	minConfiguredStart := ^uint64(0)
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

// Reload reloads the engine configuration and re-registers contracts.
// Used for hot-reload during development.
//
// Parameters:
//   - newCfg (*config.Config): new configuration to apply
//
// Returns:
//   - error: nil on success, reload error on failure
func (e *Engine) Reload(newCfg *config.Config) error {
	log.Info().Msg("reloading engine configuration")

	// Clear existing decoder state
	e.decoder.Clear()

	// Re-register contracts from new config
	for name, contract := range newCfg.Contracts {
		abiJSON, err := os.ReadFile(contract.ABI)
		if err != nil {
			return fmt.Errorf("reading ABI for %s: %w", name, err)
		}

		addr := common.HexToAddress(contract.Address)
		if err := e.decoder.RegisterContract(name, addr, string(abiJSON), contract.Events); err != nil {
			return fmt.Errorf("registering contract %s: %w", name, err)
		}

		log.Info().
			Str("contract", name).
			Str("address", contract.Address).
			Int("events", len(contract.Events)).
			Msg("re-registered contract")
	}

	// Update config reference
	e.cfg = newCfg

	log.Info().
		Int("contracts", len(newCfg.Contracts)).
		Msg("configuration reloaded successfully")

	return nil
}

// Close shuts down the engine.
//
// Returns:
//   - error: nil on success, close error on failure
func (e *Engine) Close() error {
	e.rpc.Close()
	return e.store.Close()
}

// convertEventData converts decoded event data to map[string]any for GraphQL.
// Handles common Ethereum types like common.Address and *big.Int.
func convertEventData(data map[string]interface{}) map[string]any {
	result := make(map[string]any, len(data))
	for k, v := range data {
		switch val := v.(type) {
		case common.Address:
			result[k] = val.Hex()
		case *big.Int:
			if val != nil {
				result[k] = val.String()
			} else {
				result[k] = "0"
			}
		case []byte:
			result[k] = common.Bytes2Hex(val)
		default:
			result[k] = v
		}
	}
	return result
}

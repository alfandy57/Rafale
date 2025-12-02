package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/0xredeth/Rafale/internal/api"
	"github.com/0xredeth/Rafale/internal/engine"
	"github.com/0xredeth/Rafale/internal/pubsub"
	"github.com/0xredeth/Rafale/internal/rpc"
	"github.com/0xredeth/Rafale/internal/store"
	"github.com/0xredeth/Rafale/internal/watcher"
	"github.com/0xredeth/Rafale/pkg/config"

	// Import handler package to register event handlers via init()
	_ "github.com/0xredeth/Rafale/pkg/handler"
)

var watchMode bool

// startCmd starts the indexer.
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Rafale indexer",
	Long: `Start indexing blockchain events from Linea.
Events are stored in PostgreSQL/TimescaleDB and exposed via GraphQL.

Use --watch for development mode with hot reload.`,
	RunE: runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)

	startCmd.Flags().BoolVarP(&watchMode, "watch", "w", false, "enable watch mode for development")
}

// runStart executes the start command.
//
// Parameters:
//   - cmd (*cobra.Command): the cobra command
//   - args ([]string): command arguments
//
// Returns:
//   - error: nil on success, startup error on failure
func runStart(_ *cobra.Command, _ []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	log.Info().
		Str("name", cfg.Name).
		Str("network", cfg.Network).
		Bool("watch", watchMode).
		Msg("starting rafale indexer")

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Info().Str("signal", sig.String()).Msg("received shutdown signal")
		cancel()
	}()

	// Initialize RPC client
	rpcCfg := rpc.DefaultConfig()
	rpcCfg.URL = cfg.RPCURL
	rpcClient, err := rpc.New(ctx, rpcCfg)
	if err != nil {
		return fmt.Errorf("creating RPC client: %w", err)
	}
	defer rpcClient.Close()

	// Initialize store
	storeCfg := store.DefaultConfig()
	storeCfg.DSN = cfg.Database
	db, err := store.New(storeCfg)
	if err != nil {
		return fmt.Errorf("creating store: %w", err)
	}
	defer db.Close() //nolint:errcheck // Error on close is not actionable in defer

	// Initialize broadcaster for real-time subscriptions
	broadcaster := pubsub.NewBroadcaster()

	// Initialize engine
	eng, err := engine.New(cfg, broadcaster)
	if err != nil {
		return fmt.Errorf("creating engine: %w", err)
	}
	defer func() {
		if err := eng.Close(); err != nil {
			log.Error().Err(err).Msg("error closing engine")
		}
	}()

	// Initialize API server
	apiServer := api.NewServer(cfg, db, rpcClient, broadcaster)

	// Run all services concurrently
	g, gctx := errgroup.WithContext(ctx)

	// Setup watch mode if enabled
	if watchMode {
		fileWatcher, err := setupWatchMode(eng)
		if err != nil {
			return fmt.Errorf("setting up watch mode: %w", err)
		}
		defer func() {
			if err := fileWatcher.Close(); err != nil {
				log.Error().Err(err).Msg("error closing watcher")
			}
		}()

		// Run watcher in background
		g.Go(func() error {
			return fileWatcher.Start()
		})

		// Stop watcher on context cancellation
		go func() {
			<-gctx.Done()
			_ = fileWatcher.Close() // Error already logged in defer
		}()
	}

	// Run indexer engine
	g.Go(func() error {
		if err := eng.Run(gctx); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
		return nil
	})

	// Run GraphQL API server
	g.Go(func() error {
		if err := apiServer.Start(gctx); err != nil {
			return fmt.Errorf("api server: %w", err)
		}
		return nil
	})

	// Run metrics server
	g.Go(func() error {
		if err := apiServer.StartMetrics(gctx); err != nil {
			return fmt.Errorf("metrics server: %w", err)
		}
		return nil
	})

	// Wait for all services to complete
	if err := g.Wait(); err != nil {
		log.Error().Err(err).Msg("service error")
	}

	log.Info().Msg("rafale stopped")
	return nil
}

// setupWatchMode initializes file watching for hot-reload.
//
// Parameters:
//   - eng (*engine.Engine): engine instance to reload
//
// Returns:
//   - *watcher.Watcher: configured file watcher
//   - error: nil on success, setup error on failure
func setupWatchMode(eng *engine.Engine) (*watcher.Watcher, error) {
	watchCfg := watcher.DefaultConfig()
	fileWatcher, err := watcher.New(watchCfg)
	if err != nil {
		return nil, fmt.Errorf("creating watcher: %w", err)
	}

	// Mutex to prevent concurrent reloads
	var reloadMu sync.Mutex

	// Watch rafale.yaml for changes
	err = fileWatcher.Watch("rafale.yaml", func() {
		reloadMu.Lock()
		defer reloadMu.Unlock()

		log.Info().Msg("config file changed, reloading...")

		// Reload configuration
		newCfg, err := config.Load()
		if err != nil {
			log.Error().Err(err).Msg("failed to reload config")
			return
		}

		// Reload engine with new config
		if err := eng.Reload(newCfg); err != nil {
			log.Error().Err(err).Msg("failed to reload engine")
			return
		}

		log.Info().Msg("hot-reload complete")
	})
	if err != nil {
		_ = fileWatcher.Close() // Ignore error on cleanup after setup failure
		return nil, fmt.Errorf("watching config file: %w", err)
	}

	log.Info().Msg("watch mode enabled - config changes will hot-reload")

	return fileWatcher, nil
}

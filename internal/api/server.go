// Package api provides the GraphQL API server for Rafale.
package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/0xredeth/Rafale/internal/api/graphql/generated"
	"github.com/0xredeth/Rafale/internal/api/graphql/resolver"
	"github.com/0xredeth/Rafale/internal/pubsub"
	"github.com/0xredeth/Rafale/internal/rpc"
	"github.com/0xredeth/Rafale/internal/store"
	"github.com/0xredeth/Rafale/pkg/config"
)

// Server is the GraphQL API server.
type Server struct {
	cfg        *config.Config
	httpServer *http.Server
	resolver   *resolver.Resolver
}

// NewServer creates a new API server.
//
// Parameters:
//   - cfg (*config.Config): application configuration
//   - store (*store.Store): database store
//   - rpc (*rpc.Client): RPC client
//   - broadcaster (*pubsub.Broadcaster): pub/sub broadcaster for subscriptions
//
// Returns:
//   - *Server: initialized server
func NewServer(cfg *config.Config, store *store.Store, rpc *rpc.Client, broadcaster *pubsub.Broadcaster) *Server {
	return &Server{
		cfg:      cfg,
		resolver: resolver.NewResolver(cfg, store, rpc, broadcaster),
	}
}

// Start starts the API server.
//
// Parameters:
//   - ctx (context.Context): context for shutdown
//
// Returns:
//   - error: nil on graceful shutdown, error on failure
func (s *Server) Start(ctx context.Context) error {
	// Create GraphQL handler
	srv := handler.New(generated.NewExecutableSchema(generated.Config{
		Resolvers: s.resolver,
	}))

	// Add transports
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.MultipartForm{})

	// Add WebSocket transport for subscriptions
	srv.AddTransport(&transport.Websocket{
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				// Allow localhost connections for development
				if strings.HasPrefix(origin, "http://localhost") ||
					strings.HasPrefix(origin, "http://127.0.0.1") ||
					origin == "" {
					return true
				}
				// For production, configure a reverse proxy (nginx, etc.)
				// to handle CORS, or extend config with AllowedOrigins
				return false
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		KeepAlivePingInterval: 10 * time.Second,
	})

	// Add caching
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))

	// Add extensions
	srv.Use(extension.Introspection{})
	srv.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
	})

	// Setup routes
	mux := http.NewServeMux()

	// GraphQL endpoint
	mux.Handle("/graphql", srv)

	// GraphQL playground (development)
	mux.Handle("/", playground.Handler("Rafale GraphQL", "/graphql"))

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	addr := fmt.Sprintf(":%d", s.cfg.Server.GraphQLPort)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Info().
		Int("port", s.cfg.Server.GraphQLPort).
		Msg("starting GraphQL server")

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		log.Info().Msg("shutting down GraphQL server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}
}

// StartMetrics starts the metrics server.
//
// Parameters:
//   - ctx (context.Context): context for shutdown
//
// Returns:
//   - error: nil on graceful shutdown, error on failure
func (s *Server) StartMetrics(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%d", s.cfg.Server.MetricsPort)
	metricsServer := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Info().
		Int("port", s.cfg.Server.MetricsPort).
		Msg("starting metrics server")

	errCh := make(chan error, 1)
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("shutting down metrics server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return metricsServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("metrics server error: %w", err)
	}
}

// Package rpc provides a Linea RPC client with circuit breaker support.
package rpc

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
	"github.com/sony/gobreaker"
)

// Metrics for RPC client monitoring.
var (
	rpcRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rafale_rpc_request_duration_seconds",
			Help:    "RPC request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	rpcRequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rafale_rpc_requests_total",
			Help: "Total number of RPC requests",
		},
		[]string{"method", "status"},
	)

	circuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rafale_circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=half-open, 2=open)",
		},
		[]string{"name"},
	)
)

// Client wraps an Ethereum client with circuit breaker and metrics.
type Client struct {
	eth     *ethclient.Client
	cb      *gobreaker.CircuitBreaker
	chainID *big.Int
	url     string
}

// ClientConfig holds RPC client configuration.
type ClientConfig struct {
	// URL is the RPC endpoint URL.
	URL string

	// Timeout is the request timeout.
	Timeout time.Duration

	// MaxRetries is the maximum retry attempts.
	MaxRetries int

	// CircuitBreaker holds circuit breaker settings.
	CircuitBreaker CircuitBreakerConfig
}

// CircuitBreakerConfig holds circuit breaker settings.
type CircuitBreakerConfig struct {
	// MaxRequests is the max requests in half-open state.
	MaxRequests uint32

	// Interval is the cyclic period of closed state.
	Interval time.Duration

	// Timeout is the period of open state.
	Timeout time.Duration

	// FailureThreshold is consecutive failures before opening.
	FailureThreshold uint32
}

// DefaultConfig returns default client configuration.
//
// Returns:
//   - ClientConfig: default configuration values
func DefaultConfig() ClientConfig {
	return ClientConfig{
		Timeout:    30 * time.Second,
		MaxRetries: 3,
		CircuitBreaker: CircuitBreakerConfig{
			MaxRequests:      5,
			Interval:         60 * time.Second,
			Timeout:          30 * time.Second,
			FailureThreshold: 5,
		},
	}
}

// New creates a new RPC client.
//
// Parameters:
//   - ctx (context.Context): context for connection
//   - cfg (ClientConfig): client configuration
//
// Returns:
//   - *Client: the initialized client
//   - error: nil on success, connection error on failure
func New(ctx context.Context, cfg ClientConfig) (*Client, error) {
	eth, err := ethclient.DialContext(ctx, cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("connecting to RPC: %w", err)
	}

	chainID, err := eth.ChainID(ctx)
	if err != nil {
		eth.Close()
		return nil, fmt.Errorf("getting chain ID: %w", err)
	}

	// Configure circuit breaker
	cbSettings := gobreaker.Settings{
		Name:        "linea-rpc",
		MaxRequests: cfg.CircuitBreaker.MaxRequests,
		Interval:    cfg.CircuitBreaker.Interval,
		Timeout:     cfg.CircuitBreaker.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.CircuitBreaker.FailureThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			log.Warn().
				Str("name", name).
				Str("from", from.String()).
				Str("to", to.String()).
				Msg("circuit breaker state changed")

			// Update metric
			var state float64
			switch to {
			case gobreaker.StateClosed:
				state = 0
			case gobreaker.StateHalfOpen:
				state = 1
			case gobreaker.StateOpen:
				state = 2
			}
			circuitBreakerState.WithLabelValues(name).Set(state)
		},
	}

	cb := gobreaker.NewCircuitBreaker(cbSettings)

	log.Info().
		Str("url", cfg.URL).
		Uint64("chainID", chainID.Uint64()).
		Msg("connected to Linea RPC")

	return &Client{
		eth:     eth,
		cb:      cb,
		chainID: chainID,
		url:     cfg.URL,
	}, nil
}

// Close closes the RPC connection.
func (c *Client) Close() {
	c.eth.Close()
}

// ChainID returns the chain ID.
//
// Returns:
//   - *big.Int: the chain ID
func (c *Client) ChainID() *big.Int {
	return c.chainID
}

// BlockNumber returns the current block number.
//
// Parameters:
//   - ctx (context.Context): request context
//
// Returns:
//   - uint64: current block number
//   - error: nil on success, RPC error on failure
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	start := time.Now()

	result, err := c.cb.Execute(func() (interface{}, error) {
		return c.eth.BlockNumber(ctx)
	})

	duration := time.Since(start).Seconds()
	rpcRequestDuration.WithLabelValues("eth_blockNumber").Observe(duration)

	if err != nil {
		rpcRequestTotal.WithLabelValues("eth_blockNumber", "error").Inc()
		return 0, fmt.Errorf("getting block number: %w", err)
	}

	rpcRequestTotal.WithLabelValues("eth_blockNumber", "success").Inc()
	return result.(uint64), nil
}

// BlockByNumber returns a block by number.
//
// Parameters:
//   - ctx (context.Context): request context
//   - number (*big.Int): block number (nil for latest)
//
// Returns:
//   - *types.Block: the block
//   - error: nil on success, RPC error on failure
func (c *Client) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	start := time.Now()

	result, err := c.cb.Execute(func() (interface{}, error) {
		return c.eth.BlockByNumber(ctx, number)
	})

	duration := time.Since(start).Seconds()
	rpcRequestDuration.WithLabelValues("eth_getBlockByNumber").Observe(duration)

	if err != nil {
		rpcRequestTotal.WithLabelValues("eth_getBlockByNumber", "error").Inc()
		return nil, fmt.Errorf("getting block: %w", err)
	}

	rpcRequestTotal.WithLabelValues("eth_getBlockByNumber", "success").Inc()
	return result.(*types.Block), nil
}

// FilterLogs returns logs matching the filter query.
//
// Parameters:
//   - ctx (context.Context): request context
//   - query (ethereum.FilterQuery): filter query
//
// Returns:
//   - []types.Log: matching logs
//   - error: nil on success, RPC error on failure
func (c *Client) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	start := time.Now()

	result, err := c.cb.Execute(func() (interface{}, error) {
		return c.eth.FilterLogs(ctx, query)
	})

	duration := time.Since(start).Seconds()
	rpcRequestDuration.WithLabelValues("eth_getLogs").Observe(duration)

	if err != nil {
		rpcRequestTotal.WithLabelValues("eth_getLogs", "error").Inc()
		return nil, fmt.Errorf("filtering logs: %w", err)
	}

	rpcRequestTotal.WithLabelValues("eth_getLogs", "success").Inc()
	return result.([]types.Log), nil
}

// HeaderByNumber returns a block header by number.
//
// Parameters:
//   - ctx (context.Context): request context
//   - number (*big.Int): block number (nil for latest)
//
// Returns:
//   - *types.Header: the block header
//   - error: nil on success, RPC error on failure
func (c *Client) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	start := time.Now()

	result, err := c.cb.Execute(func() (interface{}, error) {
		return c.eth.HeaderByNumber(ctx, number)
	})

	duration := time.Since(start).Seconds()
	rpcRequestDuration.WithLabelValues("eth_getBlockByNumber").Observe(duration)

	if err != nil {
		rpcRequestTotal.WithLabelValues("eth_getBlockByNumber", "error").Inc()
		return nil, fmt.Errorf("getting header: %w", err)
	}

	rpcRequestTotal.WithLabelValues("eth_getBlockByNumber", "success").Inc()
	return result.(*types.Header), nil
}

// TransactionReceipt returns the receipt for a transaction.
//
// Parameters:
//   - ctx (context.Context): request context
//   - txHash (common.Hash): transaction hash
//
// Returns:
//   - *types.Receipt: the transaction receipt
//   - error: nil on success, RPC error on failure
func (c *Client) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	start := time.Now()

	result, err := c.cb.Execute(func() (interface{}, error) {
		return c.eth.TransactionReceipt(ctx, txHash)
	})

	duration := time.Since(start).Seconds()
	rpcRequestDuration.WithLabelValues("eth_getTransactionReceipt").Observe(duration)

	if err != nil {
		rpcRequestTotal.WithLabelValues("eth_getTransactionReceipt", "error").Inc()
		return nil, fmt.Errorf("getting receipt: %w", err)
	}

	rpcRequestTotal.WithLabelValues("eth_getTransactionReceipt", "success").Inc()
	return result.(*types.Receipt), nil
}

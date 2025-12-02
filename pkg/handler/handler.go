// Package handler provides the event handler registry for Rafale.
package handler

import (
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/0xredeth/Rafale/pkg/decoder"
)

// Metrics for handler monitoring.
var (
	eventsProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rafale_events_processed_total",
			Help: "Total number of events processed",
		},
		[]string{"contract", "event"},
	)

	handlerDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rafale_handler_duration_seconds",
			Help:    "Handler execution duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"contract", "event"},
	)

	handlerErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rafale_handler_errors_total",
			Help: "Total number of handler errors",
		},
		[]string{"contract", "event"},
	)
)

// Context provides context to event handlers.
type Context struct {
	// DB is the GORM database instance.
	DB *gorm.DB

	// Block contains block information.
	Block BlockInfo

	// Log is the raw log entry.
	Log types.Log

	// Event is the decoded event data.
	Event *decoder.DecodedEvent
}

// BlockInfo contains block metadata.
type BlockInfo struct {
	// Number is the block number.
	Number uint64

	// Hash is the block hash.
	Hash string

	// Time is the block timestamp.
	Time time.Time

	// ParentHash is the parent block hash.
	ParentHash string
}

// Func is the signature for event handlers.
// The event parameter contains decoded event data.
type Func func(ctx *Context) error

// Registry manages event handlers.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Func // eventID -> handler
}

// globalRegistry is the default handler registry.
var globalRegistry = NewRegistry()

// NewRegistry creates a new handler registry.
//
// Returns:
//   - *Registry: initialized registry
func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[string]Func),
	}
}

// Register adds a handler for an event to the global registry.
// The eventID should be in format "ContractName:EventName".
//
// Parameters:
//   - eventID (string): event identifier (e.g., "USDC:Transfer")
//   - handler (Func): the handler function
func Register(eventID string, handler Func) {
	globalRegistry.Register(eventID, handler)
}

// Register adds a handler for an event.
//
// Parameters:
//   - eventID (string): event identifier (e.g., "USDC:Transfer")
//   - handler (Func): the handler function
func (r *Registry) Register(eventID string, handler Func) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[eventID]; exists {
		log.Warn().Str("eventID", eventID).Msg("overwriting existing handler")
	}

	r.handlers[eventID] = handler
	log.Debug().Str("eventID", eventID).Msg("registered handler")
}

// Get retrieves a handler for an event from the global registry.
//
// Parameters:
//   - eventID (string): event identifier
//
// Returns:
//   - Func: the handler function
//   - bool: true if handler exists
func Get(eventID string) (Func, bool) {
	return globalRegistry.Get(eventID)
}

// Get retrieves a handler for an event.
//
// Parameters:
//   - eventID (string): event identifier
//
// Returns:
//   - Func: the handler function
//   - bool: true if handler exists
func (r *Registry) Get(eventID string) (Func, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, ok := r.handlers[eventID]
	return handler, ok
}

// Handle executes the handler for a decoded event.
//
// Parameters:
//   - ctx (*Context): handler context
//
// Returns:
//   - error: nil on success, handler error on failure
func (r *Registry) Handle(ctx *Context) error {
	if ctx.Event == nil {
		return fmt.Errorf("event is nil")
	}

	handler, ok := r.Get(ctx.Event.EventID)
	if !ok {
		// No handler registered - skip silently
		log.Debug().
			Str("eventID", ctx.Event.EventID).
			Msg("no handler registered for event")
		return nil
	}

	start := time.Now()

	err := handler(ctx)

	duration := time.Since(start)
	handlerDuration.WithLabelValues(ctx.Event.ContractName, ctx.Event.EventName).Observe(duration.Seconds())

	if err != nil {
		handlerErrors.WithLabelValues(ctx.Event.ContractName, ctx.Event.EventName).Inc()
		return fmt.Errorf("handler %s: %w", ctx.Event.EventID, err)
	}

	eventsProcessed.WithLabelValues(ctx.Event.ContractName, ctx.Event.EventName).Inc()

	log.Debug().
		Str("eventID", ctx.Event.EventID).
		Uint64("block", ctx.Block.Number).
		Dur("duration", duration).
		Msg("handled event")

	return nil
}

// HasHandler checks if a handler is registered for an event.
//
// Parameters:
//   - eventID (string): event identifier
//
// Returns:
//   - bool: true if handler exists
func (r *Registry) HasHandler(eventID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.handlers[eventID]
	return ok
}

// ListHandlers returns all registered event IDs.
//
// Returns:
//   - []string: list of registered event IDs
func (r *Registry) ListHandlers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.handlers))
	for id := range r.handlers {
		ids = append(ids, id)
	}
	return ids
}

// Global returns the global handler registry.
//
// Returns:
//   - *Registry: the global registry
func Global() *Registry {
	return globalRegistry
}

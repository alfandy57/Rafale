// Package pubsub provides in-memory pub/sub for real-time GraphQL subscriptions.
package pubsub

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/0xredeth/Rafale/internal/api/graphql/model"
)

// Broadcaster manages subscription channels for real-time event streaming.
// It provides a thread-safe pub/sub mechanism for GraphQL subscriptions.
type Broadcaster struct {
	mu sync.RWMutex

	// Event subscriptions: subscriberID -> channel
	eventSubs map[string]*eventSubscription

	// Block subscriptions: subscriberID -> channel
	blockSubs map[string]chan *model.Block

	// Sync status subscriptions: subscriberID -> channel
	statusSubs map[string]chan *model.SyncStatus
}

// eventSubscription holds an event channel with optional filters.
type eventSubscription struct {
	ch        chan *model.GenericEvent
	contract  *string // optional contract filter
	eventName *string // optional event name filter
}

// NewBroadcaster creates a new broadcaster instance.
//
// Returns:
//   - *Broadcaster: initialized broadcaster
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		eventSubs:  make(map[string]*eventSubscription),
		blockSubs:  make(map[string]chan *model.Block),
		statusSubs: make(map[string]chan *model.SyncStatus),
	}
}

// SubscribeEvents creates a new event subscription with optional filters.
// The returned channel receives events matching the filters.
// Call the returned cleanup function to unsubscribe.
//
// Parameters:
//   - ctx (context.Context): context for automatic cleanup on cancellation
//   - contract (*string): optional contract name filter
//   - eventName (*string): optional event name filter
//
// Returns:
//   - <-chan *model.GenericEvent: channel receiving matching events
//   - func(): cleanup function to call when done
func (b *Broadcaster) SubscribeEvents(ctx context.Context, contract, eventName *string) (<-chan *model.GenericEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := uuid.New().String()
	ch := make(chan *model.GenericEvent, 100) // buffered to prevent blocking

	b.eventSubs[id] = &eventSubscription{
		ch:        ch,
		contract:  contract,
		eventName: eventName,
	}

	log.Debug().
		Str("subscriberID", id).
		Interface("contract", contract).
		Interface("eventName", eventName).
		Msg("new event subscription")

	// Cleanup function
	cleanup := func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		if sub, exists := b.eventSubs[id]; exists {
			close(sub.ch)
			delete(b.eventSubs, id)
			log.Debug().Str("subscriberID", id).Msg("event subscription removed")
		}
	}

	// Auto-cleanup on context cancellation
	go func() {
		<-ctx.Done()
		cleanup()
	}()

	return ch, cleanup
}

// SubscribeBlocks creates a new block subscription.
// The returned channel receives new blocks as they are indexed.
//
// Parameters:
//   - ctx (context.Context): context for automatic cleanup on cancellation
//
// Returns:
//   - <-chan *model.Block: channel receiving new blocks
//   - func(): cleanup function to call when done
func (b *Broadcaster) SubscribeBlocks(ctx context.Context) (<-chan *model.Block, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := uuid.New().String()
	ch := make(chan *model.Block, 100)

	b.blockSubs[id] = ch

	log.Debug().Str("subscriberID", id).Msg("new block subscription")

	cleanup := func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		if existingCh, exists := b.blockSubs[id]; exists {
			close(existingCh)
			delete(b.blockSubs, id)
			log.Debug().Str("subscriberID", id).Msg("block subscription removed")
		}
	}

	go func() {
		<-ctx.Done()
		cleanup()
	}()

	return ch, cleanup
}

// SubscribeSyncStatus creates a new sync status subscription.
// The returned channel receives status updates as sync progresses.
//
// Parameters:
//   - ctx (context.Context): context for automatic cleanup on cancellation
//
// Returns:
//   - <-chan *model.SyncStatus: channel receiving status updates
//   - func(): cleanup function to call when done
func (b *Broadcaster) SubscribeSyncStatus(ctx context.Context) (<-chan *model.SyncStatus, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := uuid.New().String()
	ch := make(chan *model.SyncStatus, 10)

	b.statusSubs[id] = ch

	log.Debug().Str("subscriberID", id).Msg("new sync status subscription")

	cleanup := func() {
		b.mu.Lock()
		defer b.mu.Unlock()

		if existingCh, exists := b.statusSubs[id]; exists {
			close(existingCh)
			delete(b.statusSubs, id)
			log.Debug().Str("subscriberID", id).Msg("sync status subscription removed")
		}
	}

	go func() {
		<-ctx.Done()
		cleanup()
	}()

	return ch, cleanup
}

// BroadcastEvent sends an event to all matching subscribers.
// Events are filtered by contract and event name if specified by the subscriber.
// Non-blocking: if a subscriber's buffer is full, the event is dropped for that subscriber.
//
// Parameters:
//   - event (*model.GenericEvent): the event to broadcast
func (b *Broadcaster) BroadcastEvent(event *model.GenericEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for id, sub := range b.eventSubs {
		// Apply filters
		if sub.contract != nil && *sub.contract != event.Contract {
			continue
		}
		if sub.eventName != nil && *sub.eventName != event.EventName {
			continue
		}

		// Non-blocking send
		select {
		case sub.ch <- event:
		default:
			log.Warn().
				Str("subscriberID", id).
				Str("eventName", event.EventName).
				Msg("event subscription buffer full, dropping event")
		}
	}
}

// BroadcastBlock sends a block to all block subscribers.
// Non-blocking: if a subscriber's buffer is full, the block is dropped for that subscriber.
//
// Parameters:
//   - block (*model.Block): the block to broadcast
func (b *Broadcaster) BroadcastBlock(block *model.Block) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for id, ch := range b.blockSubs {
		select {
		case ch <- block:
		default:
			log.Warn().
				Str("subscriberID", id).
				Str("blockNumber", block.Number).
				Msg("block subscription buffer full, dropping block")
		}
	}
}

// BroadcastSyncStatus sends a sync status update to all status subscribers.
// Non-blocking: if a subscriber's buffer is full, the status is dropped for that subscriber.
//
// Parameters:
//   - status (*model.SyncStatus): the status to broadcast
func (b *Broadcaster) BroadcastSyncStatus(status *model.SyncStatus) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for id, ch := range b.statusSubs {
		select {
		case ch <- status:
		default:
			log.Warn().
				Str("subscriberID", id).
				Msg("sync status subscription buffer full, dropping status")
		}
	}
}

// SubscriberCount returns the current number of subscribers for each type.
//
// Returns:
//   - events (int): number of event subscribers
//   - blocks (int): number of block subscribers
//   - status (int): number of sync status subscribers
func (b *Broadcaster) SubscriberCount() (events, blocks, status int) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return len(b.eventSubs), len(b.blockSubs), len(b.statusSubs)
}

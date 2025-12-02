package store

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// BaseEvent contains common fields for all event models.
// Embed this in your event structs for consistent indexing.
// Uses composite primary key (timestamp, id) for TimescaleDB hypertable compatibility.
type BaseEvent struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement"`
	Timestamp   time.Time `gorm:"primaryKey;index;not null"` // Must be in PK for TimescaleDB hypertable
	BlockNumber uint64    `gorm:"index;not null"`
	TxHash      string    `gorm:"type:varchar(66);index;not null"`
	TxIndex     uint      `gorm:"not null"`
	LogIndex    uint      `gorm:"not null"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}

// BeforeCreate sets the timestamp if not already set.
func (b *BaseEvent) BeforeCreate(_ *gorm.DB) error {
	if b.Timestamp.IsZero() {
		b.Timestamp = time.Now()
	}
	return nil
}

// SyncStatus represents the synchronization status for the indexer.
type SyncStatus struct {
	Contract        string    `gorm:"primaryKey;type:varchar(100)"`
	LastBlockNumber uint64    `gorm:"not null"`
	LastBlockHash   string    `gorm:"type:varchar(66)"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime"`
}

// IndexerMeta stores metadata about the indexer instance.
type IndexerMeta struct {
	Key       string    `gorm:"primaryKey;type:varchar(100)"`
	Value     string    `gorm:"type:text"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

// Transfer represents an ERC20 Transfer event.
// Stores USDC transfers on Linea with indexed fields for efficient queries.
type Transfer struct {
	BaseEvent
	From  string `gorm:"type:varchar(42);index;not null"`
	To    string `gorm:"type:varchar(42);index;not null"`
	Value string `gorm:"type:numeric(78);not null"` // uint256 max is 78 digits
}

// TableName returns the table name for Transfer.
func (Transfer) TableName() string {
	return "transfers"
}

// Event is the generic event storage model with JSONB data.
// All decoded events are automatically stored here for exploration and debugging.
// Typed handlers remain optional - use them only when you need indexed query performance.
type Event struct {
	BaseEvent
	ContractName string         `gorm:"type:varchar(100);index:idx_events_contract;not null"`
	ContractAddr string         `gorm:"type:varchar(42);index:idx_events_address;not null"`
	EventName    string         `gorm:"type:varchar(100);index:idx_events_event;not null"`
	EventSig     string         `gorm:"type:varchar(66);index;not null"` // Topic[0] hash
	Data         datatypes.JSON `gorm:"type:jsonb;not null"`
}

// TableName returns the table name for Event.
func (Event) TableName() string {
	return "events"
}

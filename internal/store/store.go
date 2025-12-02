// Package store provides PostgreSQL/TimescaleDB storage for Rafale.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Metrics for store monitoring.
var (
	dbQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rafale_db_query_duration_seconds",
			Help:    "Database query duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	dbConnectionsOpen = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "rafale_db_connections_open",
			Help: "Number of open database connections",
		},
	)
)

// Store wraps GORM with TimescaleDB support.
type Store struct {
	db             *gorm.DB
	hasTimescaleDB bool
}

// Config holds database configuration.
type Config struct {
	// DSN is the PostgreSQL connection string.
	DSN string

	// MaxOpenConns is the maximum number of open connections.
	MaxOpenConns int

	// MaxIdleConns is the maximum number of idle connections.
	MaxIdleConns int

	// ConnMaxLifetime is the maximum connection lifetime.
	ConnMaxLifetime time.Duration

	// LogLevel is the GORM log level.
	LogLevel logger.LogLevel
}

// DefaultConfig returns default store configuration.
//
// Returns:
//   - Config: default configuration values
func DefaultConfig() Config {
	return Config{
		MaxOpenConns:    25,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
		LogLevel:        logger.Warn,
	}
}

// New creates a new store instance.
//
// Parameters:
//   - cfg (Config): store configuration
//
// Returns:
//   - *Store: the initialized store
//   - error: nil on success, connection error on failure
func New(cfg Config) (*Store, error) {
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(cfg.LogLevel),
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("getting underlying DB: %w", err)
	}

	// Configure connection pool
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	// Verify connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Check TimescaleDB extension
	var extExists bool
	if err := db.Raw("SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')").Scan(&extExists).Error; err != nil {
		return nil, fmt.Errorf("checking TimescaleDB: %w", err)
	}

	if !extExists {
		log.Warn().Msg("TimescaleDB not available - using regular PostgreSQL tables")
		log.Warn().Msg("To enable hypertables: add 'shared_preload_libraries = timescaledb' to postgresql.conf and restart PostgreSQL")
	} else {
		log.Info().Msg("TimescaleDB extension detected")
	}

	log.Info().
		Int("maxOpenConns", cfg.MaxOpenConns).
		Int("maxIdleConns", cfg.MaxIdleConns).
		Msg("connected to PostgreSQL")

	return &Store{db: db, hasTimescaleDB: extExists}, nil
}

// Close closes the database connection.
//
// Returns:
//   - error: nil on success, close error on failure
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("getting underlying DB: %w", err)
	}
	return sqlDB.Close()
}

// DB returns the underlying GORM instance.
//
// Returns:
//   - *gorm.DB: the GORM database instance
func (s *Store) DB() *gorm.DB {
	return s.db
}

// Migrate runs auto-migrations for the given models.
//
// Parameters:
//   - models (...interface{}): models to migrate
//
// Returns:
//   - error: nil on success, migration error on failure
func (s *Store) Migrate(models ...interface{}) error {
	if err := s.db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	return nil
}

// CreateHypertable converts a table to a TimescaleDB hypertable.
//
// Parameters:
//   - tableName (string): name of the table
//   - timeColumn (string): name of the time column
//   - chunkInterval (string): chunk time interval (e.g., "1 day")
//
// Returns:
//   - error: nil on success, hypertable creation error on failure
func (s *Store) CreateHypertable(tableName, timeColumn, chunkInterval string) error {
	if !s.hasTimescaleDB {
		log.Debug().
			Str("table", tableName).
			Msg("skipping hypertable creation (TimescaleDB not available)")
		return nil
	}

	sql := fmt.Sprintf(
		"SELECT create_hypertable('%s', '%s', chunk_time_interval => INTERVAL '%s', if_not_exists => TRUE)",
		tableName, timeColumn, chunkInterval,
	)

	if err := s.db.Exec(sql).Error; err != nil {
		return fmt.Errorf("creating hypertable %s: %w", tableName, err)
	}

	log.Info().
		Str("table", tableName).
		Str("timeColumn", timeColumn).
		Str("chunkInterval", chunkInterval).
		Msg("created hypertable")

	return nil
}

// Transaction executes a function within a database transaction.
//
// Parameters:
//   - ctx (context.Context): request context
//   - fn (func(*gorm.DB) error): function to execute within transaction
//
// Returns:
//   - error: nil on success, transaction or function error on failure
func (s *Store) Transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(fn)
}

// CreateInBatches inserts records in batches.
//
// Parameters:
//   - ctx (context.Context): request context
//   - records (interface{}): slice of records to insert
//   - batchSize (int): number of records per batch
//
// Returns:
//   - error: nil on success, insert error on failure
func (s *Store) CreateInBatches(ctx context.Context, records interface{}, batchSize int) error {
	start := time.Now()

	if err := s.db.WithContext(ctx).CreateInBatches(records, batchSize).Error; err != nil {
		return fmt.Errorf("batch insert: %w", err)
	}

	dbQueryDuration.WithLabelValues("batch_insert").Observe(time.Since(start).Seconds())
	return nil
}

// GetMaxBlockNumber returns the maximum block number from a table.
// This is used instead of a checkpoint table per the Rafale design.
//
// Parameters:
//   - ctx (context.Context): request context
//   - tableName (string): name of the table
//
// Returns:
//   - uint64: maximum block number (0 if table is empty)
//   - error: nil on success, query error on failure
func (s *Store) GetMaxBlockNumber(ctx context.Context, tableName string) (uint64, error) {
	var maxBlock *uint64

	sql := fmt.Sprintf("SELECT MAX(block_number) FROM %s", tableName)
	if err := s.db.WithContext(ctx).Raw(sql).Scan(&maxBlock).Error; err != nil {
		return 0, fmt.Errorf("getting max block from %s: %w", tableName, err)
	}

	if maxBlock == nil {
		return 0, nil
	}

	return *maxBlock, nil
}

// UpdateStats updates database connection statistics.
func (s *Store) UpdateStats() {
	sqlDB, err := s.db.DB()
	if err != nil {
		return
	}

	stats := sqlDB.Stats()
	dbConnectionsOpen.Set(float64(stats.OpenConnections))
}

// Reset truncates all event tables, clearing indexed data.
// This is a destructive operation requiring explicit confirmation.
//
// Parameters:
//   - ctx (context.Context): request context
//
// Returns:
//   - error: nil on success, truncate error on failure
func (s *Store) Reset(ctx context.Context) error {
	// Truncate transfers table
	if err := s.db.WithContext(ctx).Exec("TRUNCATE TABLE transfers RESTART IDENTITY CASCADE").Error; err != nil {
		return fmt.Errorf("truncating transfers: %w", err)
	}

	// Truncate sync_statuses if it exists
	if err := s.db.WithContext(ctx).Exec("TRUNCATE TABLE sync_statuses RESTART IDENTITY CASCADE").Error; err != nil {
		// Table might not exist, log warning and continue
		log.Warn().Err(err).Msg("failed to truncate sync_statuses (may not exist)")
	}

	log.Info().Msg("database reset complete")
	return nil
}

// GetTransferCount returns the total number of transfers indexed.
//
// Parameters:
//   - ctx (context.Context): request context
//
// Returns:
//   - int64: count of transfers
//   - error: nil on success, query error on failure
func (s *Store) GetTransferCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&Transfer{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("counting transfers: %w", err)
	}
	return count, nil
}

// TransferQuery holds query parameters for transfers.
type TransferQuery struct {
	FromBlock *uint64
	ToBlock   *uint64
	FromTime  *time.Time
	ToTime    *time.Time
	OrderBy   string // "block_number" or "timestamp"
	OrderDir  string // "ASC" or "DESC"
	Limit     int
	AfterID   *uint64 // cursor-based pagination
	BeforeID  *uint64
}

// QueryTransfers queries transfers with filtering, ordering, and pagination.
//
// Parameters:
//   - ctx (context.Context): request context
//   - q (TransferQuery): query parameters
//
// Returns:
//   - []Transfer: matching transfers
//   - int64: total count matching filters (before pagination)
//   - error: nil on success, query error on failure
func (s *Store) QueryTransfers(ctx context.Context, q TransferQuery) ([]Transfer, int64, error) {
	start := time.Now()

	// Build base query with filters
	query := s.db.WithContext(ctx).Model(&Transfer{})

	if q.FromBlock != nil {
		query = query.Where("block_number >= ?", *q.FromBlock)
	}
	if q.ToBlock != nil {
		query = query.Where("block_number <= ?", *q.ToBlock)
	}
	if q.FromTime != nil {
		query = query.Where("timestamp >= ?", *q.FromTime)
	}
	if q.ToTime != nil {
		query = query.Where("timestamp <= ?", *q.ToTime)
	}

	// Get total count
	var totalCount int64
	if err := query.Count(&totalCount).Error; err != nil {
		return nil, 0, fmt.Errorf("counting transfers: %w", err)
	}

	// Apply cursor-based pagination
	if q.AfterID != nil {
		query = query.Where("id > ?", *q.AfterID)
	}
	if q.BeforeID != nil {
		query = query.Where("id < ?", *q.BeforeID)
	}

	// Apply ordering
	orderBy := "block_number"
	if q.OrderBy == "timestamp" {
		orderBy = "timestamp"
	}
	orderDir := "ASC"
	if q.OrderDir == "DESC" {
		orderDir = "DESC"
	}
	query = query.Order(fmt.Sprintf("%s %s, id %s", orderBy, orderDir, orderDir))

	// Apply limit
	if q.Limit > 0 {
		query = query.Limit(q.Limit)
	}

	// Execute query
	var transfers []Transfer
	if err := query.Find(&transfers).Error; err != nil {
		return nil, 0, fmt.Errorf("querying transfers: %w", err)
	}

	dbQueryDuration.WithLabelValues("query_transfers").Observe(time.Since(start).Seconds())
	return transfers, totalCount, nil
}

// GetTransferByID retrieves a single transfer by ID.
//
// Parameters:
//   - ctx (context.Context): request context
//   - id (uint64): transfer ID
//
// Returns:
//   - *Transfer: the transfer or nil if not found
//   - error: nil on success, query error on failure
func (s *Store) GetTransferByID(ctx context.Context, id uint64) (*Transfer, error) {
	var transfer Transfer
	if err := s.db.WithContext(ctx).First(&transfer, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("getting transfer %d: %w", id, err)
	}
	return &transfer, nil
}

// GetTransfersByTxHash retrieves transfers by transaction hash.
//
// Parameters:
//   - ctx (context.Context): request context
//   - txHash (string): transaction hash
//
// Returns:
//   - []Transfer: matching transfers
//   - error: nil on success, query error on failure
func (s *Store) GetTransfersByTxHash(ctx context.Context, txHash string) ([]Transfer, error) {
	var transfers []Transfer
	if err := s.db.WithContext(ctx).Where("tx_hash = ?", txHash).Order("log_index ASC").Find(&transfers).Error; err != nil {
		return nil, fmt.Errorf("getting transfers by tx_hash %s: %w", txHash, err)
	}
	return transfers, nil
}

// EventQuery holds query parameters for generic events.
type EventQuery struct {
	ContractName *string
	EventName    *string
	FromBlock    *uint64
	ToBlock      *uint64
	FromTime     *time.Time
	ToTime       *time.Time
	OrderBy      string // "block_number" or "timestamp"
	OrderDir     string // "ASC" or "DESC"
	Limit        int
	AfterID      *uint64 // cursor-based pagination
	BeforeID     *uint64
}

// QueryEvents queries generic events with filtering, ordering, and pagination.
//
// Parameters:
//   - ctx (context.Context): request context
//   - q (EventQuery): query parameters
//
// Returns:
//   - []Event: matching events
//   - int64: total count matching filters (before pagination)
//   - error: nil on success, query error on failure
func (s *Store) QueryEvents(ctx context.Context, q EventQuery) ([]Event, int64, error) {
	start := time.Now()

	// Build base query with filters
	query := s.db.WithContext(ctx).Model(&Event{})

	if q.ContractName != nil {
		query = query.Where("contract_name = ?", *q.ContractName)
	}
	if q.EventName != nil {
		query = query.Where("event_name = ?", *q.EventName)
	}
	if q.FromBlock != nil {
		query = query.Where("block_number >= ?", *q.FromBlock)
	}
	if q.ToBlock != nil {
		query = query.Where("block_number <= ?", *q.ToBlock)
	}
	if q.FromTime != nil {
		query = query.Where("timestamp >= ?", *q.FromTime)
	}
	if q.ToTime != nil {
		query = query.Where("timestamp <= ?", *q.ToTime)
	}

	// Get total count
	var totalCount int64
	if err := query.Count(&totalCount).Error; err != nil {
		return nil, 0, fmt.Errorf("counting events: %w", err)
	}

	// Apply cursor-based pagination
	if q.AfterID != nil {
		query = query.Where("id > ?", *q.AfterID)
	}
	if q.BeforeID != nil {
		query = query.Where("id < ?", *q.BeforeID)
	}

	// Apply ordering
	orderBy := "block_number"
	if q.OrderBy == "timestamp" {
		orderBy = "timestamp"
	}
	orderDir := "ASC"
	if q.OrderDir == "DESC" {
		orderDir = "DESC"
	}
	query = query.Order(fmt.Sprintf("%s %s, id %s", orderBy, orderDir, orderDir))

	// Apply limit
	if q.Limit > 0 {
		query = query.Limit(q.Limit)
	}

	// Execute query
	var events []Event
	if err := query.Find(&events).Error; err != nil {
		return nil, 0, fmt.Errorf("querying events: %w", err)
	}

	dbQueryDuration.WithLabelValues("query_events").Observe(time.Since(start).Seconds())
	return events, totalCount, nil
}

// GetEventByID retrieves a single generic event by ID.
//
// Parameters:
//   - ctx (context.Context): request context
//   - id (uint64): event ID
//
// Returns:
//   - *Event: the event or nil if not found
//   - error: nil on success, query error on failure
func (s *Store) GetEventByID(ctx context.Context, id uint64) (*Event, error) {
	var event Event
	if err := s.db.WithContext(ctx).First(&event, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("getting event %d: %w", id, err)
	}
	return &event, nil
}

// GetEventsByTxHash retrieves generic events by transaction hash.
//
// Parameters:
//   - ctx (context.Context): request context
//   - txHash (string): transaction hash
//
// Returns:
//   - []Event: matching events
//   - error: nil on success, query error on failure
func (s *Store) GetEventsByTxHash(ctx context.Context, txHash string) ([]Event, error) {
	var events []Event
	if err := s.db.WithContext(ctx).Where("tx_hash = ?", txHash).Order("log_index ASC").Find(&events).Error; err != nil {
		return nil, fmt.Errorf("getting events by tx_hash %s: %w", txHash, err)
	}
	return events, nil
}

// GetEventCount returns the total number of generic events indexed.
//
// Parameters:
//   - ctx (context.Context): request context
//
// Returns:
//   - int64: count of events
//   - error: nil on success, query error on failure
func (s *Store) GetEventCount(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&Event{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("counting events: %w", err)
	}
	return count, nil
}

// Package store provides PostgreSQL/TimescaleDB storage for Rafale.
package store

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
)

// TimescaleConfig holds TimescaleDB optimization settings.
type TimescaleConfig struct {
	// ChunkInterval is the time interval for hypertable chunks (e.g., "1 day").
	ChunkInterval string

	// CompressAfter is when to compress old chunks (e.g., "7 days").
	// Empty string disables compression.
	CompressAfter string

	// RetainFor is how long to keep data (e.g., "90 days").
	// Empty string disables retention (keeps all data).
	RetainFor string
}

// DefaultTimescaleConfig returns default TimescaleDB settings.
//
// Returns:
//   - TimescaleConfig: default configuration with 1 day chunks and 7 day compression
func DefaultTimescaleConfig() TimescaleConfig {
	return TimescaleConfig{
		ChunkInterval: "1 day",
		CompressAfter: "7 days",
		RetainFor:     "", // Keep all data by default
	}
}

// SetupTimescaleDB configures a table as an optimized TimescaleDB hypertable.
//
// Parameters:
//   - ctx (context.Context): request context
//   - tableName (string): name of the table to optimize
//   - timeColumn (string): name of the time column for partitioning
//   - cfg (TimescaleConfig): optimization settings
//
// Returns:
//   - error: nil on success, setup error on failure
func (s *Store) SetupTimescaleDB(ctx context.Context, tableName, timeColumn string, cfg TimescaleConfig) error {
	if !s.hasTimescaleDB {
		log.Debug().
			Str("table", tableName).
			Msg("skipping TimescaleDB setup (extension not available)")
		return nil
	}

	// 1. Create hypertable
	if err := s.CreateHypertable(tableName, timeColumn, cfg.ChunkInterval); err != nil {
		return fmt.Errorf("creating hypertable: %w", err)
	}

	// 2. Enable compression if configured
	if cfg.CompressAfter != "" {
		if err := s.EnableCompression(ctx, tableName, timeColumn); err != nil {
			return fmt.Errorf("enabling compression: %w", err)
		}

		if err := s.AddCompressionPolicy(ctx, tableName, cfg.CompressAfter); err != nil {
			return fmt.Errorf("adding compression policy: %w", err)
		}
	}

	// 3. Add retention policy if configured
	if cfg.RetainFor != "" {
		if err := s.AddRetentionPolicy(ctx, tableName, cfg.RetainFor); err != nil {
			return fmt.Errorf("adding retention policy: %w", err)
		}
	}

	log.Info().
		Str("table", tableName).
		Str("chunkInterval", cfg.ChunkInterval).
		Str("compressAfter", cfg.CompressAfter).
		Str("retainFor", cfg.RetainFor).
		Msg("TimescaleDB setup complete")

	return nil
}

// EnableCompression enables compression on a hypertable.
//
// Parameters:
//   - ctx (context.Context): request context
//   - tableName (string): hypertable name
//   - orderByColumn (string): column for compression ordering (typically time column)
//
// Returns:
//   - error: nil on success, compression error on failure
func (s *Store) EnableCompression(ctx context.Context, tableName, orderByColumn string) error {
	if !s.hasTimescaleDB {
		return nil
	}

	sql := fmt.Sprintf(`
		ALTER TABLE %s SET (
			timescaledb.compress,
			timescaledb.compress_orderby = '%s DESC'
		)
	`, tableName, orderByColumn)

	if err := s.db.WithContext(ctx).Exec(sql).Error; err != nil {
		return fmt.Errorf("enabling compression on %s: %w", tableName, err)
	}

	log.Debug().
		Str("table", tableName).
		Msg("compression enabled")

	return nil
}

// AddCompressionPolicy adds an automatic compression policy.
//
// Parameters:
//   - ctx (context.Context): request context
//   - tableName (string): hypertable name
//   - compressAfter (string): interval after which to compress (e.g., "7 days")
//
// Returns:
//   - error: nil on success, policy error on failure
func (s *Store) AddCompressionPolicy(ctx context.Context, tableName, compressAfter string) error {
	if !s.hasTimescaleDB {
		return nil
	}

	// Remove existing policy if any (idempotent)
	s.db.WithContext(ctx).Exec(
		"SELECT remove_compression_policy($1, if_exists => true)",
		tableName,
	)

	sql := fmt.Sprintf(
		"SELECT add_compression_policy('%s', INTERVAL '%s', if_not_exists => true)",
		tableName, compressAfter,
	)

	if err := s.db.WithContext(ctx).Exec(sql).Error; err != nil {
		return fmt.Errorf("adding compression policy to %s: %w", tableName, err)
	}

	log.Info().
		Str("table", tableName).
		Str("compressAfter", compressAfter).
		Msg("compression policy added")

	return nil
}

// AddRetentionPolicy adds an automatic data retention policy.
//
// Parameters:
//   - ctx (context.Context): request context
//   - tableName (string): hypertable name
//   - retainFor (string): interval to retain data (e.g., "90 days")
//
// Returns:
//   - error: nil on success, policy error on failure
func (s *Store) AddRetentionPolicy(ctx context.Context, tableName, retainFor string) error {
	if !s.hasTimescaleDB {
		return nil
	}

	// Remove existing policy if any (idempotent)
	s.db.WithContext(ctx).Exec(
		"SELECT remove_retention_policy($1, if_exists => true)",
		tableName,
	)

	sql := fmt.Sprintf(
		"SELECT add_retention_policy('%s', INTERVAL '%s', if_not_exists => true)",
		tableName, retainFor,
	)

	if err := s.db.WithContext(ctx).Exec(sql).Error; err != nil {
		return fmt.Errorf("adding retention policy to %s: %w", tableName, err)
	}

	log.Info().
		Str("table", tableName).
		Str("retainFor", retainFor).
		Msg("retention policy added")

	return nil
}

// GetCompressionStats returns compression statistics for a hypertable.
//
// Parameters:
//   - ctx (context.Context): request context
//   - tableName (string): hypertable name
//
// Returns:
//   - map[string]interface{}: compression stats
//   - error: nil on success, query error on failure
func (s *Store) GetCompressionStats(ctx context.Context, tableName string) (map[string]interface{}, error) {
	if !s.hasTimescaleDB {
		return nil, nil
	}

	var result map[string]interface{}
	sql := `
		SELECT
			pg_size_pretty(before_compression_total_bytes) as before_compression,
			pg_size_pretty(after_compression_total_bytes) as after_compression,
			ROUND((1 - (after_compression_total_bytes::float / NULLIF(before_compression_total_bytes, 0))) * 100, 2) as compression_ratio
		FROM hypertable_compression_stats($1)
	`

	if err := s.db.WithContext(ctx).Raw(sql, tableName).Scan(&result).Error; err != nil {
		return nil, fmt.Errorf("getting compression stats for %s: %w", tableName, err)
	}

	return result, nil
}

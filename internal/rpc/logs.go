package rpc

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/rs/zerolog/log"
)

// FetchLogs fetches logs for a block range with binary split on range errors.
//
// Parameters:
//   - ctx (context.Context): request context
//   - addresses ([]common.Address): contract addresses to filter
//   - topics ([][]common.Hash): topic filters
//   - fromBlock (uint64): start block number
//   - toBlock (uint64): end block number
//
// Returns:
//   - []types.Log: all logs in the range
//   - error: nil on success, fetch error on failure
func (c *Client) FetchLogs(
	ctx context.Context,
	addresses []common.Address,
	topics [][]common.Hash,
	fromBlock, toBlock uint64,
) ([]types.Log, error) {
	return c.fetchLogsWithSplit(ctx, addresses, topics, fromBlock, toBlock, 0)
}

// fetchLogsWithSplit recursively fetches logs, splitting range on errors.
func (c *Client) fetchLogsWithSplit(
	ctx context.Context,
	addresses []common.Address,
	topics [][]common.Hash,
	fromBlock, toBlock uint64,
	depth int,
) ([]types.Log, error) {
	// Prevent infinite recursion
	const maxDepth = 20
	if depth > maxDepth {
		return nil, fmt.Errorf("max split depth exceeded (depth=%d)", depth)
	}

	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(toBlock),
		Addresses: addresses,
		Topics:    topics,
	}

	logs, err := c.FilterLogs(ctx, query)
	if err == nil {
		return logs, nil
	}

	// Check if error indicates range too large
	if !isRangeTooLargeError(err) {
		return nil, err
	}

	// Binary split: divide range in half
	if fromBlock >= toBlock {
		return nil, fmt.Errorf("cannot split further: from=%d, to=%d", fromBlock, toBlock)
	}

	mid := fromBlock + (toBlock-fromBlock)/2

	log.Debug().
		Uint64("from", fromBlock).
		Uint64("mid", mid).
		Uint64("to", toBlock).
		Int("depth", depth).
		Msg("splitting log range")

	// Fetch first half
	logsFirst, err := c.fetchLogsWithSplit(ctx, addresses, topics, fromBlock, mid, depth+1)
	if err != nil {
		return nil, fmt.Errorf("fetching first half [%d-%d]: %w", fromBlock, mid, err)
	}

	// Fetch second half
	logsSecond, err := c.fetchLogsWithSplit(ctx, addresses, topics, mid+1, toBlock, depth+1)
	if err != nil {
		return nil, fmt.Errorf("fetching second half [%d-%d]: %w", mid+1, toBlock, err)
	}

	// Combine results
	allLogs := make([]types.Log, 0, len(logsFirst)+len(logsSecond))
	allLogs = append(allLogs, logsFirst...)
	allLogs = append(allLogs, logsSecond...)

	return allLogs, nil
}

// isRangeTooLargeError checks if the error indicates the block range is too large.
//
// Parameters:
//   - err (error): the error to check
//
// Returns:
//   - bool: true if error indicates range too large
func isRangeTooLargeError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Common error messages from various providers
	rangeTooLargeIndicators := []string{
		"query returned more than",
		"block range too large",
		"exceed maximum block range",
		"too many results",
		"range too wide",
		"block range is too wide",
		"query timeout",
		"response too large",
		"max results",
		"limit exceeded",
	}

	for _, indicator := range rangeTooLargeIndicators {
		if strings.Contains(errStr, indicator) {
			return true
		}
	}

	return false
}

// FetchLogsInBatches fetches logs in fixed-size batches.
//
// Parameters:
//   - ctx (context.Context): request context
//   - addresses ([]common.Address): contract addresses to filter
//   - topics ([][]common.Hash): topic filters
//   - fromBlock (uint64): start block number
//   - toBlock (uint64): end block number
//   - batchSize (uint64): blocks per batch
//
// Returns:
//   - []types.Log: all logs in the range
//   - error: nil on success, fetch error on failure
func (c *Client) FetchLogsInBatches(
	ctx context.Context,
	addresses []common.Address,
	topics [][]common.Hash,
	fromBlock, toBlock uint64,
	batchSize uint64,
) ([]types.Log, error) {
	var allLogs []types.Log

	for start := fromBlock; start <= toBlock; start += batchSize {
		end := start + batchSize - 1
		if end > toBlock {
			end = toBlock
		}

		logs, err := c.FetchLogs(ctx, addresses, topics, start, end)
		if err != nil {
			return nil, fmt.Errorf("fetching batch [%d-%d]: %w", start, end, err)
		}

		allLogs = append(allLogs, logs...)

		log.Debug().
			Uint64("from", start).
			Uint64("to", end).
			Int("logs", len(logs)).
			Int("total", len(allLogs)).
			Msg("fetched log batch")
	}

	return allLogs, nil
}

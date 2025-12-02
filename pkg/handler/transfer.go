package handler

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog/log"

	"github.com/0xredeth/Rafale/internal/store"
)

func init() {
	// Register handlers for ERC20 Transfer events.
	// Contract name in config is lowercase "usdc", event name is "Transfer".
	Register("usdc:Transfer", handleUSDCTransfer)
}

// handleUSDCTransfer processes USDC Transfer events and stores them in the database.
func handleUSDCTransfer(ctx *Context) error {
	// Extract decoded event data
	data := ctx.Event.Data

	// Get "from" address
	fromRaw, ok := data["from"]
	if !ok {
		return fmt.Errorf("missing 'from' field in Transfer event")
	}
	from, ok := fromRaw.(common.Address)
	if !ok {
		return fmt.Errorf("invalid 'from' type: %T", fromRaw)
	}

	// Get "to" address
	toRaw, ok := data["to"]
	if !ok {
		return fmt.Errorf("missing 'to' field in Transfer event")
	}
	to, ok := toRaw.(common.Address)
	if !ok {
		return fmt.Errorf("invalid 'to' type: %T", toRaw)
	}

	// Get "value"
	valueRaw, ok := data["value"]
	if !ok {
		return fmt.Errorf("missing 'value' field in Transfer event")
	}
	value, ok := valueRaw.(*big.Int)
	if !ok {
		return fmt.Errorf("invalid 'value' type: %T", valueRaw)
	}

	// Create the Transfer record
	transfer := store.Transfer{
		BaseEvent: store.BaseEvent{
			BlockNumber: ctx.Block.Number,
			TxHash:      ctx.Log.TxHash.Hex(),
			TxIndex:     ctx.Log.TxIndex,
			LogIndex:    ctx.Log.Index,
			Timestamp:   ctx.Block.Time,
		},
		From:  from.Hex(),
		To:    to.Hex(),
		Value: value.String(),
	}

	// Insert into database
	if err := ctx.DB.Create(&transfer).Error; err != nil {
		return fmt.Errorf("inserting transfer: %w", err)
	}

	log.Debug().
		Str("from", from.Hex()).
		Str("to", to.Hex()).
		Str("value", value.String()).
		Uint64("block", ctx.Block.Number).
		Msg("stored USDC transfer")

	return nil
}

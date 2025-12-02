// Package decoder provides ABI event decoding for Rafale.
package decoder

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Decoder decodes Ethereum event logs using contract ABIs.
type Decoder struct {
	abis    map[common.Address]*abi.ABI
	events  map[common.Hash]*EventInfo
	sigToID map[common.Hash]string // eventSig -> "ContractName:EventName"
}

// EventInfo holds metadata about a registered event.
type EventInfo struct {
	// ContractName is the user-defined contract name.
	ContractName string

	// EventName is the Solidity event name.
	EventName string

	// ABI is the parsed contract ABI.
	ABI *abi.ABI

	// Event is the ABI event definition.
	Event abi.Event

	// Address is the contract address.
	Address common.Address
}

// DecodedEvent represents a decoded event log.
type DecodedEvent struct {
	// ContractName is the user-defined contract name.
	ContractName string

	// EventName is the Solidity event name.
	EventName string

	// EventID is the unique identifier "ContractName:EventName".
	EventID string

	// Log is the original log entry.
	Log types.Log

	// Data contains the decoded indexed and non-indexed parameters.
	Data map[string]interface{}
}

// New creates a new decoder.
//
// Returns:
//   - *Decoder: initialized decoder
func New() *Decoder {
	return &Decoder{
		abis:    make(map[common.Address]*abi.ABI),
		events:  make(map[common.Hash]*EventInfo),
		sigToID: make(map[common.Hash]string),
	}
}

// RegisterContract registers a contract ABI for decoding.
//
// Parameters:
//   - name (string): user-defined contract name
//   - address (common.Address): contract address
//   - abiJSON (string): ABI JSON string
//   - eventNames ([]string): event names to register (empty for all)
//
// Returns:
//   - error: nil on success, parse error on failure
func (d *Decoder) RegisterContract(name string, address common.Address, abiJSON string, eventNames []string) error {
	parsed, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return fmt.Errorf("parsing ABI for %s: %w", name, err)
	}

	d.abis[address] = &parsed

	// Create event name set for filtering
	eventSet := make(map[string]bool)
	for _, en := range eventNames {
		eventSet[en] = true
	}

	// Register events
	for eventName, event := range parsed.Events {
		// Skip if event names specified and this isn't one of them
		if len(eventNames) > 0 && !eventSet[eventName] {
			continue
		}

		info := &EventInfo{
			ContractName: name,
			EventName:    eventName,
			ABI:          &parsed,
			Event:        event,
			Address:      address,
		}

		d.events[event.ID] = info
		d.sigToID[event.ID] = fmt.Sprintf("%s:%s", name, eventName)
	}

	return nil
}

// GetEventSignatures returns all registered event signatures.
//
// Returns:
//   - []common.Hash: list of event topic0 signatures
func (d *Decoder) GetEventSignatures() []common.Hash {
	sigs := make([]common.Hash, 0, len(d.events))
	for sig := range d.events {
		sigs = append(sigs, sig)
	}
	return sigs
}

// GetAddresses returns all registered contract addresses.
//
// Returns:
//   - []common.Address: list of contract addresses
func (d *Decoder) GetAddresses() []common.Address {
	addrs := make([]common.Address, 0, len(d.abis))
	for addr := range d.abis {
		addrs = append(addrs, addr)
	}
	return addrs
}

// Decode decodes a log entry into a DecodedEvent.
//
// Parameters:
//   - log (types.Log): the log entry to decode
//
// Returns:
//   - *DecodedEvent: decoded event data
//   - error: nil on success, decode error on failure or if event not registered
func (d *Decoder) Decode(log types.Log) (*DecodedEvent, error) {
	if len(log.Topics) == 0 {
		return nil, fmt.Errorf("log has no topics")
	}

	eventSig := log.Topics[0]
	info, ok := d.events[eventSig]
	if !ok {
		return nil, fmt.Errorf("unknown event signature: %s", eventSig.Hex())
	}

	// Decode non-indexed data
	data := make(map[string]interface{})

	if len(log.Data) > 0 {
		if err := info.ABI.UnpackIntoMap(data, info.EventName, log.Data); err != nil {
			return nil, fmt.Errorf("unpacking event data: %w", err)
		}
	}

	// Decode indexed topics
	indexedArgs := make([]abi.Argument, 0)
	for _, arg := range info.Event.Inputs {
		if arg.Indexed {
			indexedArgs = append(indexedArgs, arg)
		}
	}

	// Topics[0] is the event signature, indexed params start at Topics[1]
	for i, arg := range indexedArgs {
		if i+1 >= len(log.Topics) {
			break
		}

		topic := log.Topics[i+1]

		// Handle different types
		switch arg.Type.T {
		case abi.AddressTy:
			data[arg.Name] = common.BytesToAddress(topic.Bytes())
		case abi.BoolTy:
			data[arg.Name] = topic.Big().Uint64() != 0
		default:
			// For integers and other types, store as big.Int
			data[arg.Name] = topic.Big()
		}
	}

	return &DecodedEvent{
		ContractName: info.ContractName,
		EventName:    info.EventName,
		EventID:      d.sigToID[eventSig],
		Log:          log,
		Data:         data,
	}, nil
}

// CanDecode checks if the decoder can handle a log entry.
//
// Parameters:
//   - log (types.Log): the log entry to check
//
// Returns:
//   - bool: true if the event is registered
func (d *Decoder) CanDecode(log types.Log) bool {
	if len(log.Topics) == 0 {
		return false
	}
	_, ok := d.events[log.Topics[0]]
	return ok
}

// GetEventID returns the event ID for a log entry.
//
// Parameters:
//   - log (types.Log): the log entry
//
// Returns:
//   - string: event ID in format "ContractName:EventName"
//   - bool: true if found
func (d *Decoder) GetEventID(log types.Log) (string, bool) {
	if len(log.Topics) == 0 {
		return "", false
	}
	id, ok := d.sigToID[log.Topics[0]]
	return id, ok
}

// Clear removes all registered contracts and events.
// Used during hot-reload to reset state before re-registering.
func (d *Decoder) Clear() {
	d.abis = make(map[common.Address]*abi.ABI)
	d.events = make(map[common.Hash]*EventInfo)
	d.sigToID = make(map[common.Hash]string)
}

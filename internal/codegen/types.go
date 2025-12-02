// Package codegen generates Go bindings from Ethereum ABIs.
package codegen

import (
	"regexp"
	"strings"
)

// SolidityToGo maps Solidity types to Go types.
var SolidityToGo = map[string]string{
	// Address types
	"address": "string",

	// Boolean
	"bool": "bool",

	// Signed integers
	"int8":   "int8",
	"int16":  "int16",
	"int32":  "int32",
	"int64":  "int64",
	"int128": "string", // Too large for int64, use string
	"int256": "string", // Too large for int64, use string

	// Unsigned integers
	"uint8":   "uint8",
	"uint16":  "uint16",
	"uint32":  "uint32",
	"uint64":  "uint64",
	"uint128": "string", // Too large for uint64, use string
	"uint256": "string", // Too large for uint64, use string

	// Bytes types
	"bytes":   "[]byte",
	"bytes1":  "string",
	"bytes2":  "string",
	"bytes3":  "string",
	"bytes4":  "string",
	"bytes8":  "string",
	"bytes16": "string",
	"bytes32": "string",

	// String
	"string": "string",
}

// arrayPattern matches Solidity array types like address[], uint256[10], etc.
var arrayPattern = regexp.MustCompile(`^(.+)\[(\d*)\]$`)

// MapType converts a Solidity type to its Go equivalent.
//
// Parameters:
//   - solidityType (string): the Solidity type name
//
// Returns:
//   - string: the corresponding Go type
func MapType(solidityType string) string {
	// Normalize: remove spaces
	solidityType = strings.TrimSpace(solidityType)

	// Check for array types
	if matches := arrayPattern.FindStringSubmatch(solidityType); matches != nil {
		baseType := matches[1]
		// Both fixed and dynamic arrays map to slices in Go
		goBaseType := MapType(baseType)
		return "[]" + goBaseType
	}

	// Check direct mapping
	if goType, ok := SolidityToGo[solidityType]; ok {
		return goType
	}

	// Handle int/uint without size (defaults to 256 bits)
	if solidityType == "int" {
		return "string"
	}
	if solidityType == "uint" {
		return "string"
	}

	// Handle tuple types (structs) - for now, use interface{}
	if strings.HasPrefix(solidityType, "tuple") {
		return "interface{}"
	}

	// Default to string for unknown types
	return "string"
}

// IsIndexable returns true if the Solidity type can be indexed in events.
//
// Parameters:
//   - solidityType (string): the Solidity type name
//
// Returns:
//   - bool: true if the type can be indexed
func IsIndexable(solidityType string) bool {
	// Only value types up to 32 bytes can be indexed
	indexable := map[string]bool{
		"address": true,
		"bool":    true,
		"bytes1":  true,
		"bytes2":  true,
		"bytes3":  true,
		"bytes4":  true,
		"bytes8":  true,
		"bytes16": true,
		"bytes32": true,
	}

	// All int/uint types are indexable
	if strings.HasPrefix(solidityType, "int") || strings.HasPrefix(solidityType, "uint") {
		return true
	}

	return indexable[solidityType]
}

// NeedsHexConversion returns true if the type should be stored as hex string.
//
// Parameters:
//   - solidityType (string): the Solidity type name
//
// Returns:
//   - bool: true if the type should be hex-encoded
func NeedsHexConversion(solidityType string) bool {
	// Addresses and bytes types are typically stored as hex
	if solidityType == "address" {
		return true
	}
	if strings.HasPrefix(solidityType, "bytes") {
		return true
	}
	return false
}

// IsBigInt returns true if the type represents a big integer (>64 bits).
//
// Parameters:
//   - solidityType (string): the Solidity type name
//
// Returns:
//   - bool: true if the type is a big integer
func IsBigInt(solidityType string) bool {
	bigTypes := map[string]bool{
		"int128":  true,
		"int256":  true,
		"int":     true,
		"uint128": true,
		"uint256": true,
		"uint":    true,
	}
	return bigTypes[solidityType]
}

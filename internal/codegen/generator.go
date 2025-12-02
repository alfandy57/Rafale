package codegen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// EventInfo holds parsed event information for code generation.
type EventInfo struct {
	// ContractName is the name of the contract (e.g., "usdc").
	ContractName string

	// EventName is the name of the event (e.g., "Transfer").
	EventName string

	// StructName is the Go struct name (e.g., "UsdcTransfer").
	StructName string

	// TableName is the database table name (e.g., "usdc_transfers").
	TableName string

	// EventID is the handler registry key (e.g., "usdc:Transfer").
	EventID string

	// Fields are the event fields/arguments.
	Fields []FieldInfo

	// IndexedFields are the indexed event fields.
	IndexedFields []FieldInfo

	// DataFields are the non-indexed event fields.
	DataFields []FieldInfo
}

// FieldInfo holds information about an event field.
type FieldInfo struct {
	// Name is the field name from the ABI.
	Name string

	// GoName is the Go-safe field name (PascalCase).
	GoName string

	// SolidityType is the original Solidity type.
	SolidityType string

	// GoType is the mapped Go type.
	GoType string

	// Indexed indicates if this is an indexed field.
	Indexed bool

	// JSONTag is the JSON tag for serialization.
	JSONTag string

	// GormTag is the GORM tag for database mapping.
	GormTag string
}

// Generator generates Go code from ABIs.
type Generator struct {
	// OutputDir is the directory for generated files.
	OutputDir string

	// templates holds the loaded templates.
	templates *Templates

	// allEvents tracks all generated events for migrate file.
	allEvents []EventInfo
}

// NewGenerator creates a new code generator.
//
// Parameters:
//   - outputDir (string): directory for generated files
//
// Returns:
//   - *Generator: the generator instance
//   - error: nil on success, error on failure
func NewGenerator(outputDir string) (*Generator, error) {
	tmpl, err := LoadTemplates()
	if err != nil {
		return nil, fmt.Errorf("loading templates: %w", err)
	}

	return &Generator{
		OutputDir: outputDir,
		templates: tmpl,
	}, nil
}

// Generate generates Go code for a contract's events.
//
// Parameters:
//   - contractName (string): the contract name
//   - abiJSON (string): the ABI JSON content
//   - events ([]string): list of event names to generate
//
// Returns:
//   - error: nil on success, generation error on failure
func (g *Generator) Generate(contractName, abiJSON string, events []string) error {
	// Parse ABI
	parsedABI, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return fmt.Errorf("parsing ABI: %w", err)
	}

	// Build event filter map
	eventFilter := make(map[string]bool)
	for _, e := range events {
		eventFilter[e] = true
	}

	// Collect event info
	var eventInfos []EventInfo
	for eventName, event := range parsedABI.Events {
		if !eventFilter[eventName] {
			continue
		}

		info := g.buildEventInfo(contractName, eventName, event)
		eventInfos = append(eventInfos, info)
	}

	if len(eventInfos) == 0 {
		return fmt.Errorf("no matching events found in ABI")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(g.OutputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Generate one file per event (model + handler combined)
	for _, info := range eventInfos {
		eventFile := filepath.Join(g.OutputDir, fmt.Sprintf("%s_%s.go",
			toSnakeCase(contractName), toSnakeCase(info.EventName)))
		if err := g.generateEventFile(eventFile, info); err != nil {
			return fmt.Errorf("generating %s: %w", info.EventName, err)
		}
	}

	// Track events for migrate file
	g.trackEvents(eventInfos)

	return nil
}

// buildEventInfo constructs EventInfo from ABI event.
func (g *Generator) buildEventInfo(contractName, eventName string, event abi.Event) EventInfo {
	info := EventInfo{
		ContractName: contractName,
		EventName:    eventName,
		StructName:   toPascalCase(contractName) + toPascalCase(eventName),
		TableName:    toSnakeCase(contractName) + "_" + toSnakeCase(eventName) + "s",
		EventID:      contractName + ":" + eventName,
	}

	for _, input := range event.Inputs {
		field := FieldInfo{
			Name:         input.Name,
			GoName:       toPascalCase(input.Name),
			SolidityType: input.Type.String(),
			GoType:       MapType(input.Type.String()),
			Indexed:      input.Indexed,
			JSONTag:      toSnakeCase(input.Name),
			GormTag:      buildGormTag(input.Name, input.Type.String(), input.Indexed),
		}

		info.Fields = append(info.Fields, field)

		if input.Indexed {
			info.IndexedFields = append(info.IndexedFields, field)
		} else {
			info.DataFields = append(info.DataFields, field)
		}
	}

	return info
}

// generateEventFile generates a single event file (model + handler).
func (g *Generator) generateEventFile(outputPath string, event EventInfo) error {
	var buf bytes.Buffer

	templateData := map[string]interface{}{
		"ContractName":  event.ContractName,
		"EventName":     event.EventName,
		"StructName":    event.StructName,
		"TableName":     event.TableName,
		"EventID":       event.EventID,
		"Fields":        event.Fields,
		"IndexedFields": event.IndexedFields,
		"DataFields":    event.DataFields,
		"PackageName":   "generated",
	}

	if err := g.templates.Execute(&buf, "event", templateData); err != nil {
		return fmt.Errorf("executing event template: %w", err)
	}

	if err := os.WriteFile(outputPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing file %s: %w", outputPath, err)
	}

	return nil
}

// trackEvents adds events to the all-events list for migrate file.
func (g *Generator) trackEvents(events []EventInfo) {
	g.allEvents = append(g.allEvents, events...)
}

// Finalize generates the migrate.go file with all tracked events.
func (g *Generator) Finalize() error {
	if len(g.allEvents) == 0 {
		return nil
	}

	var buf bytes.Buffer

	templateData := map[string]interface{}{
		"Events":      g.allEvents,
		"PackageName": "generated",
	}

	if err := g.templates.Execute(&buf, "migrate", templateData); err != nil {
		return fmt.Errorf("executing migrate template: %w", err)
	}

	migrateFile := filepath.Join(g.OutputDir, "migrate.go")
	if err := os.WriteFile(migrateFile, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing migrate file: %w", err)
	}

	return nil
}

// toPascalCase converts a string to PascalCase.
func toPascalCase(s string) string {
	if s == "" {
		return ""
	}

	// Handle snake_case
	parts := strings.Split(s, "_")
	var result strings.Builder

	for _, part := range parts {
		if len(part) > 0 {
			result.WriteString(strings.ToUpper(part[:1]))
			if len(part) > 1 {
				result.WriteString(part[1:])
			}
		}
	}

	return result.String()
}

// toSnakeCase converts a string to snake_case.
func toSnakeCase(s string) string {
	var result strings.Builder

	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}

	return strings.ToLower(result.String())
}

// buildGormTag builds a GORM tag for a field.
func buildGormTag(name, solidityType string, indexed bool) string {
	column := toSnakeCase(name)
	tag := fmt.Sprintf("column:%s", column)

	// Add index for indexed fields
	if indexed {
		tag += ";index"
	}

	return tag
}

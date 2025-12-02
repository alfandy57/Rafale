package codegen

import (
	"embed"
	"fmt"
	"io"
	"strings"
	"text/template"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// Templates holds parsed templates for code generation.
type Templates struct {
	event   *template.Template
	migrate *template.Template
}

// templateFuncs provides helper functions for templates.
var templateFuncs = template.FuncMap{
	"lower":       strings.ToLower,
	"upper":       strings.ToUpper,
	"title":       cases.Title(language.English).String,
	"toPascal":    toPascalCase,
	"toSnake":     toSnakeCase,
	"join":        strings.Join,
	"mapType":     MapType,
	"isBigInt":    IsBigInt,
	"needsHex":    NeedsHexConversion,
	"isIndexable": IsIndexable,
	"add":         func(a, b int) int { return a + b },
}

// LoadTemplates loads and parses the embedded templates.
//
// Returns:
//   - *Templates: the loaded templates
//   - error: nil on success, template error on failure
func LoadTemplates() (*Templates, error) {
	t := &Templates{}

	// Load event template (combined model + handler per event)
	eventContent, err := templatesFS.ReadFile("templates/event.tmpl")
	if err != nil {
		return nil, fmt.Errorf("reading event template: %w", err)
	}

	t.event, err = template.New("event").Funcs(templateFuncs).Parse(string(eventContent))
	if err != nil {
		return nil, fmt.Errorf("parsing event template: %w", err)
	}

	// Load migrate template
	migrateContent, err := templatesFS.ReadFile("templates/migrate.tmpl")
	if err != nil {
		return nil, fmt.Errorf("reading migrate template: %w", err)
	}

	t.migrate, err = template.New("migrate").Funcs(templateFuncs).Parse(string(migrateContent))
	if err != nil {
		return nil, fmt.Errorf("parsing migrate template: %w", err)
	}

	return t, nil
}

// Execute executes a named template with the given data.
//
// Parameters:
//   - w (io.Writer): the output writer
//   - name (string): template name ("event" or "migrate")
//   - data (interface{}): template data
//
// Returns:
//   - error: nil on success, execution error on failure
func (t *Templates) Execute(w io.Writer, name string, data interface{}) error {
	switch name {
	case "event":
		return t.event.Execute(w, data)
	case "migrate":
		return t.migrate.Execute(w, data)
	default:
		return fmt.Errorf("unknown template: %s", name)
	}
}

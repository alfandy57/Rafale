package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/0xredeth/Rafale/internal/codegen"
	"github.com/0xredeth/Rafale/pkg/config"
)

// Default output directory for generated files.
const defaultOutputDir = "generated"

// codegenCmd generates code from ABIs.
var codegenCmd = &cobra.Command{
	Use:   "codegen",
	Short: "Generate Go bindings from contract ABIs",
	Long: `Generate type-safe Go bindings from contract ABIs defined in rafale.yaml.
This creates event types and decoder functions in the generated/ directory.`,
	RunE: runCodegen,
}

var (
	outputDir string
)

func init() {
	rootCmd.AddCommand(codegenCmd)

	codegenCmd.Flags().StringVarP(&outputDir, "output", "o", defaultOutputDir, "Output directory for generated files")
}

// runCodegen executes the codegen command.
//
// Parameters:
//   - cmd (*cobra.Command): the cobra command
//   - args ([]string): command arguments
//
// Returns:
//   - error: nil on success, code generation error on failure
func runCodegen(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	log.Info().
		Int("contracts", len(cfg.Contracts)).
		Str("output", outputDir).
		Msg("generating code from ABIs")

	// Create generator
	gen, err := codegen.NewGenerator(outputDir)
	if err != nil {
		return fmt.Errorf("creating generator: %w", err)
	}

	// Process each contract
	for name, contract := range cfg.Contracts {
		log.Info().
			Str("contract", name).
			Str("abi", contract.ABI).
			Int("events", len(contract.Events)).
			Msg("generating bindings")

		// Read ABI file
		abiPath := contract.ABI
		if !filepath.IsAbs(abiPath) {
			// Make relative to current directory
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}
			abiPath = filepath.Join(cwd, abiPath)
		}

		abiJSON, err := os.ReadFile(abiPath) //nolint:gosec // G304: Path is validated from config
		if err != nil {
			return fmt.Errorf("reading ABI for %s: %w", name, err)
		}

		// Generate code
		if err := gen.Generate(name, string(abiJSON), contract.Events); err != nil {
			return fmt.Errorf("generating %s: %w", name, err)
		}

		log.Info().
			Str("contract", name).
			Msg("bindings generated")
	}

	// Generate migrate.go with all models
	if err := gen.Finalize(); err != nil {
		return fmt.Errorf("generating migrate file: %w", err)
	}

	log.Info().
		Str("output", outputDir).
		Msg("code generation completed")

	return nil
}

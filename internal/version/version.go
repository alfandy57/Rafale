// Package version provides build-time version information for Rafale.
package version

// Build-time variables set via ldflags.
var (
	// Version is the semantic version (e.g., "1.0.0").
	Version = "1.0.0"

	// Commit is the git commit hash.
	Commit = "unknown"

	// Date is the build date in RFC3339 format.
	Date = "unknown"
)

// Package main provides the CLI entry point for Rafale.
package main

import (
	"os"

	"github.com/0xredeth/Rafale/cmd/rafale/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}

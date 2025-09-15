package main

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dora-utils",
	Short: "Dora blockchain explorer utilities",
	Long:  "Various utilities for the Dora blockchain explorer including database migration, block syncing, and token generation",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

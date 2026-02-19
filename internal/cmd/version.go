package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version can be set via LDFLAGS during build
var Version = "0.0.36"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of op",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("op version %s\n", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

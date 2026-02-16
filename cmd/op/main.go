package main

import (
	"log"

	"github.com/octopilot/octopilot-pipeline-tools/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

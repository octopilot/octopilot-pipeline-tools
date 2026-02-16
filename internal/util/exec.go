package util

import (
	"fmt"
	"os"
	"os/exec"
)

// RunCommand executes a command connecting stdout/stderr/stdin.
func RunCommand(name string, args ...string) error {
	fmt.Printf("Running: %s %v\n", name, args)
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

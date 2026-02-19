package util

import (
	"fmt"
	"os"
	"os/exec"
)

// RunCommandFn is the function used to execute external commands.
// It is a var so tests can replace it without spawning real processes.
var RunCommandFn = runCommandImpl

// RunCommand executes a command, connecting stdout/stderr/stdin.
func RunCommand(name string, args ...string) error {
	return RunCommandFn(name, args...)
}

func runCommandImpl(name string, args ...string) error {
	fmt.Printf("Running: %s %v\n", name, args)
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

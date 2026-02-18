package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// We assume the binary 'op' is built and available in the root or dist/
// Or we can build it as part of the test setup.
// To keep it simple, let's assume `op-integration-test` or similar binary path is provided via env var.

func TestIntegration_Buildpack(t *testing.T) {
	opBin := os.Getenv("OP_BINARY")
	if opBin == "" {
		t.Skip("OP_BINARY env var not set")
	}

	testDir := "fixtures/buildpack"
	absTestDir, _ := filepath.Abs(testDir)

	cmd := exec.Command(opBin, "build", "--push=false", "--repo=integration-test")
	cmd.Dir = absTestDir
	// Capture output
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("op build failed: %v\nOutput:\n%s", err, string(out))
	}
	fmt.Printf("Buildpack Output: %s\n", string(out))
}

func TestIntegration_Dockerfile(t *testing.T) {
	opBin := os.Getenv("OP_BINARY")
	if opBin == "" {
		t.Skip("OP_BINARY env var not set")
	}

	testDir := "fixtures/dockerfile"
	absTestDir, _ := filepath.Abs(testDir)

	cmd := exec.Command(opBin, "build", "--push=false", "--repo=integration-test")
	cmd.Dir = absTestDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("op build failed: %v\nOutput:\n%s", err, string(out))
	}
	fmt.Printf("Dockerfile Output: %s\n", string(out))
}

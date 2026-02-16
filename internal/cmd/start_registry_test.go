package cmd

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStartRegistryIntegration(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker not found, skipping integration test")
	}

	// 1. Cleanup any existing registry
	exec.Command("docker", "rm", "-f", "octopilot-registry").Run()

	// 2. Run start-registry (no trust to avoid sudo prompt in tests)
	// We use the root command to execute the subcommand logic if possible,
	// or just call the function if exposed. Since logic is in Run, we can execute the binary?
	// Or we can invoke the cobra command.

	cmd := startRegistryCmd
	cmd.SetArgs([]string{"--trust=false"}) // Explicitly disable trust

	// Execute
	// Note: Verify logic. It runs exec.Command("docker", "run", ...)
	// This will actually start a container on the host.
	err := cmd.Execute()
	assert.NoError(t, err)

	// 3. Verify Container is running
	out, err := exec.Command("docker", "ps", "--filter", "name=octopilot-registry", "--format", "{{.Names}}").Output()
	assert.NoError(t, err)
	assert.Contains(t, strings.TrimSpace(string(out)), "octopilot-registry")

	// 4. Verify Port 5001 is listening (or mapped)
	// curl -k https://localhost:5001/v2/
	// Give it a second to start
	time.Sleep(2 * time.Second)

	curlCmd := exec.Command("curl", "-k", "https://localhost:5001/v2/")
	outCurl, err := curlCmd.Output()
	// curl might fail if cert validation fails, but -k ignores it.
	// If connection refused, err will be set.
	if err != nil {
		t.Logf("curl failed: %v", err)
		// It might be because the container failed to start?
		logs, _ := exec.Command("docker", "logs", "octopilot-registry").CombinedOutput()
		t.Logf("Registry logs: %s", logs)
		t.Fail()
	}
	assert.Contains(t, string(outCurl), "{}") // Empty registry returns {} on /v2/ usually? Or 401?
	// Registry v2 returns 200 OK and {} usually if auth not enabled.
}

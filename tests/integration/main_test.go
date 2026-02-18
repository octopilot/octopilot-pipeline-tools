//go:build integration

package integration

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// We assume the binary 'op' is built and available in the root or dist/
// Or we can build it as part of the test setup.
// To keep it simple, let's assume `op-integration-test` or similar binary path is provided via env var.

func requireRegistry(t *testing.T) string {
	t.Helper()
	registryPort := "5001"
	registryHost := "localhost"
	registryUrl := fmt.Sprintf("http://%s:%s/v2/", registryHost, registryPort)
	registryTag := fmt.Sprintf("%s:%s", registryHost, registryPort)

	// Check if registry is running
	if isRegistryRunning(registryUrl) {
		t.Logf("Registry found at %s", registryUrl)
		return registryTag
	}

	t.Fatalf("Integration tests require a local registry running on port %s. Please start one (e.g. 'docker run -d -p 5001:5000 registry:2').", registryPort)
	return ""
}

func isRegistryRunning(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return true
		}
	}

	// Try HTTPS if HTTP failed
	if strings.HasPrefix(url, "http://") {
		httpsUrl := strings.Replace(url, "http://", "https://", 1)
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		httpsClient := &http.Client{Timeout: 2 * time.Second, Transport: tr}
		resp, err := httpsClient.Get(httpsUrl)
		if err == nil {
			resp.Body.Close()
			return resp.StatusCode == 200
		}
	}

	return false
}

func TestIntegration_Buildpack(t *testing.T) {
	opBin := os.Getenv("OP_BINARY")
	if opBin == "" {
		t.Skip("OP_BINARY env var not set")
	}

	repoHost := requireRegistry(t)

	// On Docker for Mac, buildpack containers running on "bridge" network cannot easily reach the host registry
	// due to networking and TLS trust issues. We skip this test on Mac to avoid fragility.
	// It runs correctly in CI (Linux) where we use OP_PACK_NETWORK=host.
	if runtime.GOOS == "darwin" {
		t.Skip("Skipping Buildpack integration test on Mac due to Docker networking/TLS limitations")
	}

	repo := fmt.Sprintf("%s/integration-test", repoHost)

	testDir := "fixtures/buildpack"
	absTestDir, _ := filepath.Abs(testDir)

	// We use --push=true to bypass daemon export issues (containerd) by using standard Pack build-to-registry
	// This exercises the 'useDirectPack' codepath in build.go
	cmd := exec.Command(opBin, "build", "--push=true", "--repo="+repo)
	cmd.Dir = absTestDir

	// Pass current environment
	cmd.Env = os.Environ()

	// In CI (GitHub Actions), we are likely on Linux and need host networking for the build container
	// to access the registry on localhost.
	if os.Getenv("CI") == "true" {
		cmd.Env = append(cmd.Env, "OP_PACK_NETWORK=host")
	}

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

	repoHost := requireRegistry(t)
	repo := fmt.Sprintf("%s/integration-test", repoHost)

	testDir := "fixtures/dockerfile"
	absTestDir, _ := filepath.Abs(testDir)

	cmd := exec.Command(opBin, "build", "--push=false", "--repo="+repo)
	cmd.Dir = absTestDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("op build failed: %v\nOutput:\n%s", err, string(out))
	}
	fmt.Printf("Dockerfile Output: %s\n", string(out))
}

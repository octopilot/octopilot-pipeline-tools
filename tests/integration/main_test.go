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
	repo := fmt.Sprintf("%s/integration-test", repoHost)

	testDir := "fixtures/buildpack"
	absTestDir, _ := filepath.Abs(testDir)

	// We use --push=true to bypass daemon export issues (containerd) by using standard Pack build-to-registry
	// https://github.com/octopilot/registry-tls provides the TLS registry. We also use it as a service in CI.
	// docker run -p 5001:5001 -v registry-data:/var/lib/registry registry-tls
	// This exercises the 'useDirectPack' codepath in build.go
	cmd := exec.Command(opBin, "build", "--push=true", "--repo="+repo)
	cmd.Dir = absTestDir

	// Pass current environment
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("SKAFFOLD_INSECURE_REGISTRY=%s", repoHost))
	// Enable pack debug logging
	cmd.Env = append(cmd.Env, "OP_DEBUG=true")

	// In CI (GitHub Actions) and now on Mac with the TLS registry, we might need host networking
	// or just standard access. The user said "our octopilot custom registry solves these TLS issues".
	// Let's try enabling host networking for Mac too if that helps, or just rely on the test environment.
	// For now, I'll append it if CI OR Darwin, or just always for these tests if safe.
	if os.Getenv("CI") == "true" || runtime.GOOS == "darwin" {
		cmd.Env = append(cmd.Env, "OP_PACK_NETWORK=host")
	}

	// Capture output
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("op build failed: %v\nOutput:\n%s", err, string(out))
	}
	fmt.Printf("Buildpack Output: %s\n", string(out))
}

func TestIntegration_BuildpackRunImage(t *testing.T) {
	opBin := os.Getenv("OP_BINARY")
	if opBin == "" {
		t.Skip("OP_BINARY env var not set")
	}

	repoHost := requireRegistry(t)

	repo := fmt.Sprintf("%s/integration-test", repoHost)

	// Run the build op with custom skaffold file
	cmd := exec.Command(opBin, "build", "--push", "--platform=linux/arm64", "-f", "skaffold-runimage.yaml")
	cmd.Dir = filepath.Join("fixtures", "buildpack")
	cmd.Env = append(os.Environ(), fmt.Sprintf("SKAFFOLD_DEFAULT_REPO=%s", repo))
	cmd.Env = append(cmd.Env, fmt.Sprintf("SKAFFOLD_INSECURE_REGISTRY=%s", repoHost))
	if os.Getenv("CI") == "true" || runtime.GOOS == "darwin" {
		cmd.Env = append(cmd.Env, "OP_PACK_NETWORK=host")
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("op build runImage failed: %v\nOutput:\n%s", err, out)
	}
	// ... (existing test content)
	t.Logf("Output:\n%s", out)
}

func TestIntegration_BuildpackMultiContext(t *testing.T) {
	opBin := os.Getenv("OP_BINARY")
	if opBin == "" {
		t.Skip("OP_BINARY env var not set")
	}

	repoHost := requireRegistry(t)

	repo := fmt.Sprintf("%s/integration-test", repoHost)

	// Run the build op with multi-context skaffold file
	cmd := exec.Command(opBin, "build", "--push", "--platform=linux/arm64")
	cmd.Dir = filepath.Join("fixtures", "multicontext")
	cmd.Env = append(os.Environ(), fmt.Sprintf("SKAFFOLD_DEFAULT_REPO=%s", repo))
	cmd.Env = append(cmd.Env, fmt.Sprintf("SKAFFOLD_INSECURE_REGISTRY=%s", repoHost))

	// CRITICAL: Disable default BuildKit attestations (provenance).
	// Without this, BuildKit creates an OCI Image Index containing both the image manifest
	// and an attestation manifest (platform: unknown/unknown).
	// The `pack` lifecycle (via go-containerregistry) fails to handle this Index correctly when
	// trying to use it as a run-image, resulting in the error:
	// "failed to export: get run image top layer SHA: image has no layers".
	// Setting this env var forces a standard single-manifest image.
	cmd.Env = append(cmd.Env, "BUILDX_NO_DEFAULT_ATTESTATIONS=1")

	if os.Getenv("CI") == "true" || runtime.GOOS == "darwin" {
		cmd.Env = append(cmd.Env, "OP_PACK_NETWORK=host")
	}

	// Setup CA cert for pack lifecycle if needed
	// On macOS/Docker Desktop, the container runs in a VM. To access the registry on the host (or another container mapped to host),
	// we use a bridge network or host.docker.internal.
	// The registry uses a self-signed certificate. For `pack` (lifecycle) to trust it, we must:
	// 1. Extract the CA cert from the running registry container.
	// 2. Mount it into the build container (handled in internal/cmd/build.go via OP_REGISTRY_CA_PATH).
	if runtime.GOOS == "darwin" || strings.Contains(repoHost, "localhost") {
		certDir := filepath.Join("fixtures", "certs")
		if err := os.MkdirAll(certDir, 0755); err != nil {
			t.Logf("Failed to create cert dir: %v", err)
		} else {
			// Find registry container ID dynamically (required for CI where names are generated)
			containerID := "octopilot-registry" // Default for local manual runs
			out, err := exec.Command("docker", "ps", "-q", "--filter", "ancestor=ghcr.io/octopilot/registry-tls:latest").Output()
			if err == nil {
				ids := strings.Fields(string(out))
				if len(ids) > 0 {
					containerID = ids[0]
					t.Logf("Found registry container ID: %s", containerID)
				}
			}

			// Copy certs: using the found container ID
			// Cert path is /etc/envoy/certs/tls.crt (Envoy is the TLS terminator in this container)
			cmdCP := exec.Command("docker", "cp", fmt.Sprintf("%s:/etc/envoy/certs/tls.crt", containerID), certDir)
			if out, err := cmdCP.CombinedOutput(); err != nil {
				t.Logf("Failed to copy certs from registry container (%s): %v. Output: %s", containerID, err, string(out))
			} else {
				// Set Env. The file is copied as "tls.crt" into certDir
				caPath, _ := filepath.Abs(filepath.Join(certDir, "tls.crt"))
				// Check if file exists
				if _, err := os.Stat(caPath); err == nil {
					cmd.Env = append(cmd.Env, fmt.Sprintf("OP_REGISTRY_CA_PATH=%s", caPath))
					t.Logf("Successfully set OP_REGISTRY_CA_PATH=%s", caPath)
				} else {
					t.Logf("Cert file not found at %s after copy", caPath)
				}
			}
		}
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("op build multi-context failed: %v\nOutput:\n%s", err, out)
	}
	outputStr := string(out)
	t.Logf("Output:\n%s", outputStr)

	// Verify that the runImage was resolved.
	// We expect a line like: "Resolving runImage base-image to built artifact ..."
	if !strings.Contains(outputStr, "Resolving runImage base-image to built artifact") {
		t.Errorf("Expected output to contain 'Resolving runImage base-image to built artifact', but it didn't")
	}
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

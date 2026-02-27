//go:build integration

package integration

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// logWriter writes to w and also logs each line via t.Logf (visible with go test -v), so
// integration subprocess output is not sunk by go test's stdout capture.
type logWriter struct {
	t *testing.T
	w io.Writer
	b bytes.Buffer
}

func (l *logWriter) Write(p []byte) (n int, err error) {
	n, err = l.w.Write(p)
	if err != nil {
		return n, err
	}
	l.b.Write(p)
	for {
		line, err := l.b.ReadString('\n')
		if err == io.EOF {
			if line != "" {
				l.b.WriteString(line)
			}
			break
		}
		if line == "" {
			break
		}
		l.t.Logf("%s", strings.TrimSuffix(line, "\n"))
	}
	return n, nil
}

func streamToTestLog(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Stdout = &logWriter{t: t, w: os.Stdout}
	cmd.Stderr = &logWriter{t: t, w: os.Stderr}
}

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

func setupBuildEnv(t *testing.T, cmd *exec.Cmd, repoHost string) {
	t.Helper()

	// Initialize Env if not set, otherwise append to it.
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, fmt.Sprintf("SKAFFOLD_INSECURE_REGISTRY=%s", repoHost))

	// Enable pack debug logging for troubleshooting
	cmd.Env = append(cmd.Env, "OP_DEBUG=true")

	// CRITICAL: Disable default BuildKit attestations (provenance).
	// Without this, BuildKit creates an OCI Image Index containing both the image manifest
	// and an attestation manifest (platform: unknown/unknown).
	// The `pack` lifecycle (via go-containerregistry) fails to handle this Index correctly when
	// trying to use it as a run-image, resulting in the error:
	// "failed to export: get run image top layer SHA: image has no layers".
	// Setting this env var forces a standard single-manifest image.
	cmd.Env = append(cmd.Env, "BUILDX_NO_DEFAULT_ATTESTATIONS=1")

	// In CI (GitHub Actions) and now on Mac with the TLS registry, we might need host networking
	// or just standard access.
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
}

// integrationTarget defines one fixture to build. Each target corresponds to a
// directory under tests/integration/fixtures/ (buildpack, dockerfile, multicontext, helm).
type integrationTarget struct {
	name     string
	dir      string
	push     bool
	args     []string // extra args (e.g. -f skaffold-runimage.yaml, --platform=...)
	checkOut string   // if set, output must contain this string (e.g. runImage resolution)
}

func TestIntegration(t *testing.T) {
	opBin := os.Getenv("OP_BINARY")
	if opBin == "" {
		t.Skip("OP_BINARY env var not set")
	}

	repoHost := requireRegistry(t)
	repo := fmt.Sprintf("%s/integration-test", repoHost)

	targets := []integrationTarget{
		{
			name: "Buildpack",
			dir:  "fixtures/buildpack",
			push: true,
			args: nil,
		},
		{
			name: "BuildpackRunImage",
			dir:  "fixtures/buildpack",
			push: true,
			args: []string{"--platform=linux/arm64", "-f", "skaffold-runimage.yaml"},
		},
		{
			name: "BuildpackMultiContext",
			dir:  "fixtures/multicontext",
			push: true,
			args: []string{"--platform=linux/arm64"},
			checkOut: "Resolving runImage base-image to built artifact",
		},
		{
			name: "Dockerfile",
			dir:  "fixtures/dockerfile",
			push: false,
			args: nil,
		},
		{
			name: "HelmChart",
			dir:  "fixtures/helm",
			push: true,
			args: nil,
		},
	}

	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			absTestDir, _ := filepath.Abs(target.dir)

			args := []string{"build", "--repo=" + repo}
			if target.push {
				args = append(args, "--push=true")
			} else {
				args = append(args, "--push=false")
			}
			args = append(args, target.args...)

			cmd := exec.Command(opBin, args...)
			cmd.Dir = absTestDir
			cmd.Env = append(os.Environ(), fmt.Sprintf("SKAFFOLD_DEFAULT_REPO=%s", repo))
			setupBuildEnv(t, cmd, repoHost)

			if target.checkOut != "" {
				var stdoutBuf, stderrBuf strings.Builder
				cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf, &logWriter{t: t, w: io.Discard})
				cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf, &logWriter{t: t, w: io.Discard})
				if err := cmd.Run(); err != nil {
					t.Fatalf("op build failed: %v\nStderr:\n%s", err, stderrBuf.String())
				}
				outputStr := stdoutBuf.String() + stderrBuf.String()
				if !strings.Contains(outputStr, target.checkOut) {
					t.Errorf("output should contain %q;\noutput:\n%s", target.checkOut, outputStr)
				}
				return
			}

			var stderrBuf strings.Builder
			cmd.Stdout = io.MultiWriter(os.Stdout, &logWriter{t: t, w: io.Discard})
			cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf, &logWriter{t: t, w: io.Discard})
			if err := cmd.Run(); err != nil {
				t.Fatalf("op build failed: %v\nStderr:\n%s", err, stderrBuf.String())
			}
		})
	}
}

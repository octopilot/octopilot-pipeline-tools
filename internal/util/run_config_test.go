package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runConfigYAML = `
default_repo: localhost:5001
tag: latest
contexts:
  api:
    ports:
      - "8081:8080"
    env:
      PORT: "8080"
      LOG_LEVEL: "debug"
  frontend:
    ports:
      - "3000:3000"
    env:
      PORT: "3000"
    volumes:
      - "./public:/app/public"
`

func writeRunConfig(t *testing.T, cwd, content string) {
	t.Helper()
	dir := filepath.Join(cwd, ".github")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "octopilot.yaml"), []byte(content), 0o644))
}

func TestLoadRunConfig(t *testing.T) {
	cwd := t.TempDir()
	writeRunConfig(t, cwd, runConfigYAML)

	cfg, err := LoadRunConfig(cwd)
	require.NoError(t, err)
	assert.Equal(t, "localhost:5001", cfg.DefaultRepo)
	assert.Equal(t, "latest", cfg.Tag)
	require.Contains(t, cfg.Contexts, "api")
	assert.Equal(t, []string{"8081:8080"}, cfg.Contexts["api"].Ports)
	assert.Equal(t, "8080", cfg.Contexts["api"].Env["PORT"])
}

func TestLoadRunConfig_Missing(t *testing.T) {
	cfg, err := LoadRunConfig(t.TempDir())
	require.NoError(t, err) // returns empty config, not an error
	assert.NotNil(t, cfg)
	assert.Empty(t, cfg.DefaultRepo)
}

func TestGetRunOptionsForContext_WithConfig(t *testing.T) {
	cwd := t.TempDir()
	writeRunConfig(t, cwd, runConfigYAML)

	cfg, err := LoadRunConfig(cwd)
	require.NoError(t, err)

	ports, env, volumes, containerPort := GetRunOptionsForContext("api", cwd, cfg, cwd)
	assert.Equal(t, []string{"8081:8080"}, ports)
	assert.Equal(t, "8080", env["PORT"])
	assert.Equal(t, "debug", env["LOG_LEVEL"])
	assert.Empty(t, volumes)
	assert.Equal(t, 8080, containerPort) // default inferred port
}

func TestGetRunOptionsForContext_WithVolumes(t *testing.T) {
	cwd := t.TempDir()
	writeRunConfig(t, cwd, runConfigYAML)

	cfg, err := LoadRunConfig(cwd)
	require.NoError(t, err)

	ports, env, volumes, _ := GetRunOptionsForContext("frontend", cwd, cfg, cwd)
	assert.Equal(t, []string{"3000:3000"}, ports)
	assert.Equal(t, "3000", env["PORT"])
	assert.Equal(t, []string{"./public:/app/public"}, volumes)
}

func TestGetRunOptionsForContext_UnknownContext(t *testing.T) {
	cwd := t.TempDir()
	writeRunConfig(t, cwd, runConfigYAML)

	cfg, err := LoadRunConfig(cwd)
	require.NoError(t, err)

	// Unknown context returns inferred defaults, no ports
	ports, env, _, containerPort := GetRunOptionsForContext("unknown", cwd, cfg, cwd)
	assert.Empty(t, ports) // caller must auto-assign
	assert.Equal(t, "8080", env["PORT"])
	assert.Equal(t, 8080, containerPort)
}

func TestGetRunOptionsForContext_NilConfig(t *testing.T) {
	cwd := t.TempDir()
	// nil config: should fall back to LoadRunConfig (empty config) without panicking
	ports, env, _, _ := GetRunOptionsForContext("api", cwd, nil, cwd)
	assert.Empty(t, ports)
	assert.Equal(t, "8080", env["PORT"])
}

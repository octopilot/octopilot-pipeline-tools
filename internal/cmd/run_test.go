package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSkaffoldForRun(t *testing.T, dir string) {
	t.Helper()
	yaml := `
apiVersion: skaffold/v4beta1
kind: Config
build:
  artifacts:
    - image: my-app
      context: app
`
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "app"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skaffold.yaml"), []byte(yaml), 0o644))
}

func TestResolveRunImage_FromBuildResult(t *testing.T) {
	dir := t.TempDir()
	data, _ := json.Marshal(util.BuildResult{Builds: []util.BuildEntry{
		{ImageName: "my-app", Tag: "ghcr.io/acme/my-app:v1@sha256:abc"},
	}})
	require.NoError(t, os.WriteFile(filepath.Join(dir, util.BuildResultFilename), data, 0o644))

	img := resolveRunImage(dir, "my-app")
	assert.Equal(t, "ghcr.io/acme/my-app:v1@sha256:abc", img)
}

func TestResolveRunImage_FallbackToDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SKAFFOLD_DEFAULT_REPO", "localhost:5001")
	// No build_result.json
	img := resolveRunImage(dir, "my-app")
	assert.Equal(t, "localhost:5001/my-app:latest", img)
}

func TestResolveRunImage_BuildResultWrongImage(t *testing.T) {
	dir := t.TempDir()
	data, _ := json.Marshal(util.BuildResult{Builds: []util.BuildEntry{
		{ImageName: "other-app", Tag: "ghcr.io/acme/other-app:v1@sha256:abc"},
	}})
	require.NoError(t, os.WriteFile(filepath.Join(dir, util.BuildResultFilename), data, 0o644))

	t.Setenv("SKAFFOLD_DEFAULT_REPO", "ghcr.io/acme")
	img := resolveRunImage(dir, "my-app")
	// my-app not found in build_result → falls back to default
	assert.Equal(t, "ghcr.io/acme/my-app:latest", img)
}

func TestRunCmd_ContextList(t *testing.T) {
	dir := t.TempDir()
	writeSkaffoldForRun(t, dir)

	// Change directory for the command
	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(orig)

	runCmd.Flags().Set("skaffold-file", "skaffold.yaml")
	err := runCmd.RunE(runCmd, []string{"context", "list"})
	require.NoError(t, err)
}

func TestRunCmd_UnknownContext(t *testing.T) {
	dir := t.TempDir()
	writeSkaffoldForRun(t, dir)

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(orig)

	runCmd.Flags().Set("skaffold-file", "skaffold.yaml")
	err := runCmd.RunE(runCmd, []string{"nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

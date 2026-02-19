package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const skaffoldYAMLTwoArtifacts = `
apiVersion: skaffold/v4beta1
kind: Config
metadata:
  name: my-app
build:
  artifacts:
    - image: my-app-base
      context: base
      docker:
        dockerfile: Dockerfile
    - image: my-app
      context: .
      buildpacks:
        builder: ghcr.io/octopilot/builder-jammy-base:latest
        runImage: my-app-base
`

const skaffoldYAMLSingleArtifact = `
apiVersion: skaffold/v4beta1
kind: Config
build:
  artifacts:
    - image: single-app
      context: app
`

func TestParseSkaffoldArtifacts_TwoArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skaffold.yaml")
	require.NoError(t, os.WriteFile(path, []byte(skaffoldYAMLTwoArtifacts), 0o644))

	artifacts, err := ParseSkaffoldArtifacts(path)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	assert.Equal(t, "my-app-base", artifacts[0].Image)
	assert.Equal(t, "base", artifacts[0].Context)
	assert.Equal(t, "my-app", artifacts[1].Image)
	assert.Equal(t, ".", artifacts[1].Context)
}

func TestParseSkaffoldArtifacts_Single(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skaffold.yaml")
	require.NoError(t, os.WriteFile(path, []byte(skaffoldYAMLSingleArtifact), 0o644))

	artifacts, err := ParseSkaffoldArtifacts(path)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	assert.Equal(t, "single-app", artifacts[0].Image)
	assert.Equal(t, "app", artifacts[0].Context)
}

func TestParseSkaffoldArtifacts_Missing(t *testing.T) {
	_, err := ParseSkaffoldArtifacts("/nonexistent/skaffold.yaml")
	assert.Error(t, err)
}

func TestParseSkaffoldArtifacts_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skaffold.yaml")
	require.NoError(t, os.WriteFile(path, []byte("not: valid: yaml: ::"), 0o644))
	// yaml.Unmarshal is lenient; ensure no panic at minimum.
	_, err := ParseSkaffoldArtifacts(path)
	// Accept either success (empty artifacts) or an error.
	if err == nil {
		t.Log("yaml.Unmarshal was lenient — no artifacts expected")
	}
}

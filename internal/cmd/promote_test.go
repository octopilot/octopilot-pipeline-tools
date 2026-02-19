package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeBuildResultFile(t *testing.T, dir string, builds []util.BuildEntry) {
	t.Helper()
	data, err := json.Marshal(util.BuildResult{Builds: builds})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, util.BuildResultFilename), data, 0o644))
}

func TestPromote_SingleArtifact(t *testing.T) {
	dir := t.TempDir()
	writeBuildResultFile(t, dir, []util.BuildEntry{
		{ImageName: "my-app", Tag: "ghcr.io/acme/my-app:v1@sha256:abc"},
	})

	t.Setenv("GOOGLE_GKE_IMAGE_REPOSITORY", "ghcr.io/acme")
	t.Setenv("GOOGLE_GKE_IMAGE_PP_REPOSITORY", "europe-west1-docker.pkg.dev/proj/reg")

	var srcRef, dstRef string
	old := craneCopy
	craneCopy = func(src, dst string, _ ...crane.Option) error {
		srcRef, dstRef = src, dst
		return nil
	}
	defer func() { craneCopy = old }()

	_ = promoteCmd.Flags().Set("source", "dev")
	_ = promoteCmd.Flags().Set("destination", "pp")
	_ = promoteCmd.Flags().Set("build-result-dir", dir)
	_ = promoteCmd.Flags().Set("image-name", "")

	err := promoteCmd.RunE(promoteCmd, nil)
	require.NoError(t, err)

	assert.Equal(t, "ghcr.io/acme/my-app:v1@sha256:abc", srcRef)
	assert.Equal(t, "europe-west1-docker.pkg.dev/proj/reg/my-app:v1@sha256:abc", dstRef)
}

func TestPromote_MultiArtifact_SelectsByName(t *testing.T) {
	dir := t.TempDir()
	writeBuildResultFile(t, dir, []util.BuildEntry{
		{ImageName: "op-base", Tag: "ghcr.io/acme/op-base:v1@sha256:aaa"},
		{ImageName: "op", Tag: "ghcr.io/acme/op:v1@sha256:bbb"},
	})

	t.Setenv("GOOGLE_GKE_IMAGE_REPOSITORY", "ghcr.io/acme")
	t.Setenv("GOOGLE_GKE_IMAGE_PP_REPOSITORY", "europe-west1-docker.pkg.dev/proj/reg")

	var srcRef, dstRef string
	old := craneCopy
	craneCopy = func(src, dst string, _ ...crane.Option) error {
		srcRef, dstRef = src, dst
		return nil
	}
	defer func() { craneCopy = old }()

	_ = promoteCmd.Flags().Set("source", "dev")
	_ = promoteCmd.Flags().Set("destination", "pp")
	_ = promoteCmd.Flags().Set("build-result-dir", dir)
	_ = promoteCmd.Flags().Set("image-name", "op")

	err := promoteCmd.RunE(promoteCmd, nil)
	require.NoError(t, err)

	assert.Equal(t, "ghcr.io/acme/op:v1@sha256:bbb", srcRef)
	assert.Equal(t, "europe-west1-docker.pkg.dev/proj/reg/op:v1@sha256:bbb", dstRef)
}

func TestPromote_MultiArtifact_DefaultsToLast(t *testing.T) {
	dir := t.TempDir()
	writeBuildResultFile(t, dir, []util.BuildEntry{
		{ImageName: "op-base", Tag: "ghcr.io/acme/op-base:v1@sha256:aaa"},
		{ImageName: "op", Tag: "ghcr.io/acme/op:v1@sha256:bbb"},
	})

	t.Setenv("GOOGLE_GKE_IMAGE_REPOSITORY", "ghcr.io/acme")
	t.Setenv("GOOGLE_GKE_IMAGE_PP_REPOSITORY", "europe-west1-docker.pkg.dev/proj/reg")

	var srcRef string
	old := craneCopy
	craneCopy = func(src, dst string, _ ...crane.Option) error {
		srcRef = src
		return nil
	}
	defer func() { craneCopy = old }()

	_ = promoteCmd.Flags().Set("source", "dev")
	_ = promoteCmd.Flags().Set("destination", "pp")
	_ = promoteCmd.Flags().Set("build-result-dir", dir)
	_ = promoteCmd.Flags().Set("image-name", "")

	err := promoteCmd.RunE(promoteCmd, nil)
	require.NoError(t, err)
	// Default is last entry = op, not op-base
	assert.Equal(t, "ghcr.io/acme/op:v1@sha256:bbb", srcRef)
}

func TestPromote_MissingBuildResult(t *testing.T) {
	t.Setenv("GOOGLE_GKE_IMAGE_REPOSITORY", "ghcr.io/acme")
	t.Setenv("GOOGLE_GKE_IMAGE_PP_REPOSITORY", "europe-west1-docker.pkg.dev/proj/reg")

	_ = promoteCmd.Flags().Set("source", "dev")
	_ = promoteCmd.Flags().Set("destination", "pp")
	_ = promoteCmd.Flags().Set("build-result-dir", t.TempDir()) // empty dir
	_ = promoteCmd.Flags().Set("image-name", "")

	err := promoteCmd.RunE(promoteCmd, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "build_result.json")
}

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractVersionTag(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"ghcr.io/org/op:v1.0.0@sha256:abc123", "v1.0.0"},
		{"ghcr.io/org/op:v1.0.0", "v1.0.0"},
		{"myrepo/image:latest", "latest"},
		{"image:sha-20240101", "sha-20240101"},
		{"nocolon", "nocolon"},        // no colon → return as-is
		{"a:b:c@sha256:xyz", "c"},     // last colon before @ is the tag separator
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, extractVersionTag(tc.input), "input=%q", tc.input)
	}
}

func TestWatchCmd_MatchesImmediately(t *testing.T) {
	dir := t.TempDir()
	data, _ := json.Marshal(util.BuildResult{Builds: []util.BuildEntry{
		{ImageName: "op", Tag: "ghcr.io/acme/op:v1.0.0@sha256:bbb"},
	}})
	require.NoError(t, os.WriteFile(filepath.Join(dir, util.BuildResultFilename), data, 0o644))

	t.Setenv("GOOGLE_GKE_IMAGE_REPOSITORY", "ghcr.io/acme")

	// Override the external commands
	oldFlux := watchFluxReconcile
	watchFluxReconcile = func(_, _ string) {}
	defer func() { watchFluxReconcile = oldFlux }()

	oldGet := watchGetDeploymentImage
	watchGetDeploymentImage = func(_, _ string) (string, error) {
		return "ghcr.io/acme/op:v1.0.0@sha256:bbb", nil
	}
	defer func() { watchGetDeploymentImage = oldGet }()

	// Override RunCommand (kubectl rollout status)
	oldRun := util.RunCommandFn
	util.RunCommandFn = func(_ string, _ ...string) error { return nil }
	defer func() { util.RunCommandFn = oldRun }()

	oldInterval := watchPollInterval
	watchPollInterval = 1 * time.Millisecond
	defer func() { watchPollInterval = oldInterval }()

	_ = watchCmd.Flags().Set("component", "my-deployment")
	_ = watchCmd.Flags().Set("environment", "dev")
	_ = watchCmd.Flags().Set("namespace", "default")
	_ = watchCmd.Flags().Set("timeout", "1m")
	_ = watchCmd.Flags().Set("build-result-dir", dir)
	_ = watchCmd.Flags().Set("image-name", "op")
	_ = watchCmd.Flags().Set("poll-timeout", "5s")

	err := watchCmd.RunE(watchCmd, nil)
	require.NoError(t, err)
}

func TestWatchCmd_PollTimeout(t *testing.T) {
	dir := t.TempDir()
	data, _ := json.Marshal(util.BuildResult{Builds: []util.BuildEntry{
		{ImageName: "op", Tag: "ghcr.io/acme/op:v2.0.0@sha256:ccc"},
	}})
	require.NoError(t, os.WriteFile(filepath.Join(dir, util.BuildResultFilename), data, 0o644))

	t.Setenv("GOOGLE_GKE_IMAGE_REPOSITORY", "ghcr.io/acme")

	oldFlux := watchFluxReconcile
	watchFluxReconcile = func(_, _ string) {}
	defer func() { watchFluxReconcile = oldFlux }()

	oldGet := watchGetDeploymentImage
	// Always return an old image tag — never matches
	watchGetDeploymentImage = func(_, _ string) (string, error) {
		return "ghcr.io/acme/op:v1.0.0@sha256:aaa", nil
	}
	defer func() { watchGetDeploymentImage = oldGet }()

	oldInterval := watchPollInterval
	watchPollInterval = 1 * time.Millisecond
	defer func() { watchPollInterval = oldInterval }()

	_ = watchCmd.Flags().Set("component", "my-deployment")
	_ = watchCmd.Flags().Set("environment", "dev")
	_ = watchCmd.Flags().Set("namespace", "default")
	_ = watchCmd.Flags().Set("timeout", "1m")
	_ = watchCmd.Flags().Set("build-result-dir", dir)
	_ = watchCmd.Flags().Set("image-name", "op")
	_ = watchCmd.Flags().Set("poll-timeout", "50ms")

	err := watchCmd.RunE(watchCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

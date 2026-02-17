package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestPrepareSkaffoldOptions(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected func(*testing.T, *cobra.Command)
	}{
		{
			name: "defaults",
			args: []string{},
			expected: func(t *testing.T, cmd *cobra.Command) {
				opts := prepareSkaffoldOptions(cmd, "/tmp")
				assert.Empty(t, opts.Platforms)
				assert.Nil(t, opts.PushImages.Value())
				assert.Equal(t, "manual", opts.Trigger)
			},
		},
		{
			name: "platform flag single",
			args: []string{"--platform", "linux/amd64"},
			expected: func(t *testing.T, cmd *cobra.Command) {
				opts := prepareSkaffoldOptions(cmd, "/tmp")
				assert.Equal(t, []string{"linux/amd64"}, opts.Platforms)
			},
		},
		{
			name: "platform flag multiple",
			args: []string{"--platform", "linux/amd64,linux/arm64"},
			expected: func(t *testing.T, cmd *cobra.Command) {
				opts := prepareSkaffoldOptions(cmd, "/tmp")
				assert.Equal(t, []string{"linux/amd64", "linux/arm64"}, opts.Platforms)
			},
		},
		{
			name: "push flag",
			args: []string{"--push"},
			expected: func(t *testing.T, cmd *cobra.Command) {
				opts := prepareSkaffoldOptions(cmd, "/tmp")
				assert.NotNil(t, opts.PushImages.Value())
				assert.True(t, *opts.PushImages.Value())
			},
		},
		{
			name: "repo flag",
			args: []string{"--repo", "ghcr.io/octopilot/test"},
			expected: func(t *testing.T, cmd *cobra.Command) {
				opts := prepareSkaffoldOptions(cmd, "/tmp")
				assert.NotNil(t, opts.DefaultRepo.Value())
				assert.Equal(t, "ghcr.io/octopilot/test", *opts.DefaultRepo.Value())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fresh command for each test to avoid flag pollution
			cmd := &cobra.Command{}
			// Manually add flags as they are added in init() usually
			cmd.Flags().String("repo", "", "Registry to push to (overrides defaults)")
			cmd.Flags().String("platform", "", "Target platforms (e.g. linux/amd64,linux/arm64)")
			cmd.Flags().Bool("push", false, "Push the built images to the registry")

			// Parse flags
			err := cmd.Flags().Parse(tt.args)
			assert.NoError(t, err)

			tt.expected(t, cmd)
		})
	}
}

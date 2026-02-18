package cmd

import (
	"context"
	"io"
	"testing"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/graph"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	schemaUtil "github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/util"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/octopilot/octopilot-pipeline-tools/internal/pack"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRunner implements Builder interface
type MockRunner struct {
	mock.Mock
}

func (m *MockRunner) Build(ctx context.Context, out io.Writer, artifacts []*latest.Artifact) ([]graph.Artifact, error) {
	args := m.Called(ctx, out, artifacts)
	return args.Get(0).([]graph.Artifact), args.Error(1)
}

func TestBuild_MixedArtifacts(t *testing.T) {
	// Backup and Restore
	oldGetAllConfigs := getAllConfigs
	oldGetRunContext := getRunContext
	oldNewRunner := newRunner
	oldPackBuild := packBuild
	oldRemoteHead := remoteHead
	oldResolveDefaultRepo := resolveDefaultRepo
	defer func() {
		getAllConfigs = oldGetAllConfigs
		getRunContext = oldGetRunContext
		newRunner = oldNewRunner
		packBuild = oldPackBuild
		remoteHead = oldRemoteHead
		resolveDefaultRepo = oldResolveDefaultRepo
	}()

	// Mocks
	mockRunner := new(MockRunner)

	// Artifacts
	artBuildpack := &latest.Artifact{
		ImageName: "buildpack-image",
		ArtifactType: latest.ArtifactType{
			BuildpackArtifact: &latest.BuildpackArtifact{},
		},
	}
	artDocker := &latest.Artifact{
		ImageName: "docker-image",
		ArtifactType: latest.ArtifactType{
			DockerArtifact: &latest.DockerArtifact{},
		},
	}

	// Mock Implementations
	getAllConfigs = func(ctx context.Context, opts config.SkaffoldOptions) ([]schemaUtil.VersionedConfig, error) {
		return []schemaUtil.VersionedConfig{}, nil
	}
	getRunContext = func(ctx context.Context, opts config.SkaffoldOptions, configs []schemaUtil.VersionedConfig) (*runcontext.RunContext, error) {
		// Use a hack to call the real one but passing a compatible config?
		// Since we can't easily construct a working RunContext from scratch (private fields),
		// we rely on the real `GetRunContext` to process our input.
		// `GetRunContext` takes `[]util.VersionedConfig`.
		// `latest.SkaffoldConfig` implements `util.VersionedConfig`.

		cfg := &latest.SkaffoldConfig{
			APIVersion: latest.Version,
			Kind:       "Config",
			Pipeline: latest.Pipeline{
				Build: latest.BuildConfig{
					Artifacts: []*latest.Artifact{artBuildpack, artDocker},
				},
			},
		}

		// Call real implementation with our crafted config
		return oldGetRunContext(ctx, opts, []schemaUtil.VersionedConfig{cfg})
	}

	newRunner = func(ctx context.Context, runCtx *runcontext.RunContext) (Builder, error) {
		return mockRunner, nil
	}

	packBuildCalled := false
	packBuild = func(ctx context.Context, opts pack.BuildOptions, out io.Writer) error {
		packBuildCalled = true
		assert.Equal(t, "test-repo/buildpack-image:latest", opts.ImageName)
		return nil
	}

	remoteHead = func(ref name.Reference, options ...remote.Option) (*v1.Descriptor, error) {
		// Return a dummy descriptor for digest resolution
		return &v1.Descriptor{
			Digest: v1.Hash{
				Algorithm: "sha256",
				Hex:       "0000000000000000000000000000000000000000000000000000000000000000",
			},
		}, nil
	}

	resolveDefaultRepo = func(string) string {
		return "test-repo"
	}

	// Setup Mock Expectation
	// Since Build is called with a slice containing just artDocker (a pointer),
	// we need to match it. assert.Equal or mock.Anything.
	// We can match broadly.
	mockRunner.On("Build", mock.Anything, mock.Anything, mock.MatchedBy(func(arts []*latest.Artifact) bool {
		return len(arts) == 1 && arts[0].ImageName == "docker-image"
	})).Return([]graph.Artifact{
		{ImageName: "docker-image", Tag: "test-repo/docker-image:latest"},
	}, nil)

	// Execute
	cmd := &cobra.Command{}
	cmd.Flags().Bool("push", true, "")
	// We need to set flags on the ACTUAL buildCmd, or use a new one?
	// buildCmd is global. We should use it but reset flags?
	// The `RunE` is defined on `buildCmd`.
	// We can execute `buildCmd.RunE(buildCmd, []string{})`.
	// But `buildCmd` has flags defined in `init()`.

	// Let's set the flag on `buildCmd`.
	buildCmd.Flags().Set("push", "true")
	buildCmd.Flags().Set("repo", "") // Let mock resolve it

	err := buildCmd.RunE(buildCmd, []string{})
	// If the real GetRunContext fails (e.g. valid file check), we might get error.
	// But we passed empty opts.ConfigurationFile (default skaffold.yaml).
	// GetRunContext might try to read it.
	// We mocked getAllConfigs to return empty.
	// But we call real GetRunContext inside our mock with OUR config.
	// Real GetRunContext typically trusts the passed config list.

	assert.NoError(t, err)

	// Verify
	assert.True(t, packBuildCalled, "pack.Build should be called for buildpack artifact")
	mockRunner.AssertExpectations(t)
}

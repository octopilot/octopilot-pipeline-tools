package cmd

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	schemaUtil "github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/util"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/octopilot/octopilot-pipeline-tools/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runBuildWithImageRegistry runs `op build --push` over the given buildpack artifacts with registry as
// --image-registry (or OP_IMAGE_REGISTRY when viaEnv) and returns the pack options per image name.
func runBuildWithImageRegistry(t *testing.T, registry string, viaEnv bool, arts ...*latest.Artifact) map[string]pack.BuildOptions {
	t.Helper()
	oldGetAllConfigs, oldGetRunContext, oldNewRunner := getAllConfigs, getRunContext, newRunner
	oldPackBuild, oldRemoteHead, oldResolveDefaultRepo, oldLookup := packBuild, remoteHead, resolveDefaultRepo, builderRunImageLookup
	t.Cleanup(func() {
		getAllConfigs, getRunContext, newRunner = oldGetAllConfigs, oldGetRunContext, oldNewRunner
		packBuild, remoteHead, resolveDefaultRepo, builderRunImageLookup = oldPackBuild, oldRemoteHead, oldResolveDefaultRepo, oldLookup
		_ = buildCmd.Flags().Set("image-registry", "")
	})

	getAllConfigs = func(context.Context, config.SkaffoldOptions) ([]schemaUtil.VersionedConfig, error) {
		return []schemaUtil.VersionedConfig{}, nil
	}
	getRunContext = func(ctx context.Context, opts config.SkaffoldOptions, _ []schemaUtil.VersionedConfig) (*runcontext.RunContext, error) {
		cfg := &latest.SkaffoldConfig{APIVersion: latest.Version, Kind: "Config",
			Pipeline: latest.Pipeline{Build: latest.BuildConfig{Artifacts: arts}}}
		return oldGetRunContext(ctx, opts, []schemaUtil.VersionedConfig{cfg})
	}
	newRunner = func(context.Context, *runcontext.RunContext) (Builder, error) { return new(MockRunner), nil }
	got := map[string]pack.BuildOptions{}
	packBuild = func(_ context.Context, opts pack.BuildOptions, _ io.Writer) error {
		got[opts.ImageName] = opts
		return nil
	}
	remoteHead = func(name.Reference, ...remote.Option) (*v1.Descriptor, error) {
		return &v1.Descriptor{Digest: v1.Hash{Algorithm: "sha256", Hex: "0000000000000000000000000000000000000000000000000000000000000000"}}, nil
	}
	resolveDefaultRepo = func(string) string { return "test-repo" }
	builderRunImageLookup = func(builder string, _ []string) (string, error) {
		// what the builder's metadata names, as published upstream
		return "paketobuildpacks/run-jammy-base:latest", nil
	}

	t.Setenv("OP_IMAGE_REGISTRY", "")
	if viaEnv {
		t.Setenv("OP_IMAGE_REGISTRY", registry)
	} else {
		require.NoError(t, buildCmd.Flags().Set("image-registry", registry))
	}
	_ = buildCmd.Flags().Set("push", "true")
	_ = buildCmd.Flags().Set("repo", "")
	_ = buildCmd.Flags().Set("artifact", "")
	require.NoError(t, buildCmd.RunE(buildCmd, []string{}))
	return got
}

func buildpackArtifact(image, builder, runImage string) *latest.Artifact {
	return &latest.Artifact{ImageName: image, ArtifactType: latest.ArtifactType{
		BuildpackArtifact: &latest.BuildpackArtifact{Builder: builder, RunImage: runImage}}}
}

func TestBuildPullsBuildersAndRunImagesThroughImageRegistry(t *testing.T) {
	for _, viaEnv := range []bool{false, true} {
		got := runBuildWithImageRegistry(t, "us-docker.pkg.dev/pw-ctl/ci", viaEnv,
			buildpackArtifact("app", "ghcr.io/octopilot/builder-jammy-base:rust", ""),
			buildpackArtifact("worker", "ghcr.io/octopilot/builder-jammy-base:rust", "quay.io/org/run:2"),
		)
		app, worker := got["test-repo/app:latest"], got["test-repo/worker:latest"]
		assert.Equal(t, "us-docker.pkg.dev/pw-ctl/ci/octopilot/builder-jammy-base:rust", app.Builder, "viaEnv=%v", viaEnv)
		assert.Equal(t, "us-docker.pkg.dev/pw-ctl/ci/paketobuildpacks/run-jammy-base:latest", app.RunImage,
			"the builder's default run image is named and pulled through the registry (viaEnv=%v)", viaEnv)
		assert.Equal(t, "us-docker.pkg.dev/pw-ctl/ci/org/run:2", worker.RunImage, "viaEnv=%v", viaEnv)
	}
}

func TestBuildWithoutImageRegistryUsesImagesAsWritten(t *testing.T) {
	got := runBuildWithImageRegistry(t, "", false,
		buildpackArtifact("app", "ghcr.io/octopilot/builder-jammy-base:rust", ""),
		buildpackArtifact("worker", "ghcr.io/octopilot/builder-jammy-base:rust", "quay.io/org/run:2"),
	)
	assert.Equal(t, "ghcr.io/octopilot/builder-jammy-base:rust", got["test-repo/app:latest"].Builder)
	assert.Equal(t, "", got["test-repo/app:latest"].RunImage, "pack picks the builder's run image itself")
	assert.Equal(t, "quay.io/org/run:2", got["test-repo/worker:latest"].RunImage)
}

func TestBuildNeverRewritesImagesTheConfigBuilds(t *testing.T) {
	got := runBuildWithImageRegistry(t, "us-docker.pkg.dev/pw-ctl/ci", false,
		buildpackArtifact("ghcr.io/octopilot/app-base", "ghcr.io/octopilot/builder-jammy-base:rust", "ubuntu:jammy"),
		buildpackArtifact("ghcr.io/octopilot/app", "ghcr.io/octopilot/builder-jammy-base:rust", "ghcr.io/octopilot/app-base"),
	)
	seen := 0
	for ref, opts := range got {
		switch {
		case strings.HasSuffix(ref, "/app:latest"):
			seen++
			assert.Contains(t, opts.RunImage, "app-base", "the base built in this run, not the mirror")
			assert.NotContains(t, opts.RunImage, "pkg.dev")
		case strings.HasSuffix(ref, "/app-base:latest"):
			seen++
			assert.Equal(t, "us-docker.pkg.dev/pw-ctl/ci/library/ubuntu:jammy", opts.RunImage)
		}
	}
	assert.Equal(t, 2, seen, "both artifacts built: %v", got)
	assert.Len(t, got, 2)
}

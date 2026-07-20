package pack

import (
	"context"
	"fmt"
	"io"

	"os"

	"github.com/buildpacks/pack/pkg/cache"
	"github.com/buildpacks/pack/pkg/client"
	"github.com/buildpacks/pack/pkg/logging"
)

// BuildOptions mimics the options we need for `pack build --publish`
type BuildOptions struct {
	ImageName  string
	Builder    string
	Path       string
	RunImage   string
	Publish    bool
	ClearCache bool
	Env        map[string]string
	SBOMDir    string
	// Buildpacks: when non-empty, only these buildpacks are used (DisableSystemBuildpacks true).
	Buildpacks []string
	// Registry handling if needed (insecure, etc.)
	Target             string
	InsecureRegistries []string
	Volumes            []string
}

// Build performs a pack build using the library.
func Build(ctx context.Context, opts BuildOptions, out io.Writer) error {
	logger := logging.NewLogWithWriters(out, out)
	if os.Getenv("OP_DEBUG") == "true" {
		logger.WantVerbose(true)
	}
	packClient, err := client.NewClient(client.WithLogger(logger))
	if err != nil {
		return fmt.Errorf("failed to create pack client: %w", err)
	}

	buildOpts := client.BuildOptions{
		Image:                   opts.ImageName,
		Builder:                 opts.Builder,
		RunImage:                opts.RunImage,
		AppPath:                 opts.Path,
		Publish:                 opts.Publish,
		ClearCache:              opts.ClearCache,
		TrustBuilder:            func(s string) bool { return true }, // Always trust for now (internal tool)
		Env:                     opts.Env,
		SBOMDestinationDir:      opts.SBOMDir,
		Buildpacks:              opts.Buildpacks,
		DisableSystemBuildpacks: len(opts.Buildpacks) > 0, // When explicit list, use only those
		Platform:                opts.Target,
		InsecureRegistries:      opts.InsecureRegistries,
		ContainerConfig: client.ContainerConfig{
			Network: os.Getenv("OP_PACK_NETWORK"),
			Volumes: opts.Volumes,
		},
	}

	// Build-cache-as-registry-image: ephemeral CI runners lose pack's local
	// volume cache between runs, which forces cold builds (e.g. full Rust
	// workspace recompiles). OP_CACHE_IMAGE names a registry image (typically
	// <registry>/<image>-buildcache) that the lifecycle restores from and
	// exports to, so warm caches survive runner recycling. Only meaningful for
	// per-artifact invocations (CI passes --artifact); multi-arch or
	// multi-artifact local builds should leave it unset (volume cache default).
	if cacheImage := os.Getenv("OP_CACHE_IMAGE"); cacheImage != "" {
		buildOpts.Cache = cache.CacheOpts{
			Build: cache.CacheInfo{Format: cache.CacheImage, Source: cacheImage},
		}
	}

	// We might need to handle fetch logic if relying on daemon.

	// Log build options for debugging
	fmt.Printf("[pack] BuildOptions: Image=%s Builder=%s RunImage=%s Target=%s Network=%s Publish=%v InsecureRegistries=%v CacheImage=%s\n",
		opts.ImageName, opts.Builder, opts.RunImage, opts.Target, os.Getenv("OP_PACK_NETWORK"), opts.Publish, opts.InsecureRegistries, os.Getenv("OP_CACHE_IMAGE"))

	_, _ = fmt.Fprintf(out, "Building %s using builder %s (publish=%v)...\n", opts.ImageName, opts.Builder, opts.Publish)
	if err := packClient.Build(ctx, buildOpts); err != nil {
		return fmt.Errorf("pack build failed: %w", err)
	}
	return nil
}

package pack

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/buildpacks/pack/pkg/cache"
	"github.com/buildpacks/pack/pkg/client"
	"github.com/buildpacks/pack/pkg/logging"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
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
	// The lifecycle's output is kept (its tail) so a failed export can be classified: a dropped connection is
	// exported again, a refused credential is not.
	tail := util.NewTailWriter(tailKeep)
	logger := logging.NewLogWithWriters(io.MultiWriter(out, tail), io.MultiWriter(out, tail))
	if os.Getenv("OP_DEBUG") == "true" {
		logger.WantVerbose(true)
	}
	// util.Keychain adds Google credentials (Workload Identity) to the docker config, so the lifecycle's
	// CNB_REGISTRY_AUTH covers Artifact Registry builders, run images and cache images too.
	packClient, err := client.NewClient(client.WithLogger(logger), client.WithKeychain(util.Keychain))
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
		ExportRetry: exportRetry(tail),
	}

	// Build-cache-as-registry-image: ephemeral CI runners lose pack's local
	// volume cache between runs, which forces cold builds (e.g. full Rust
	// workspace recompiles). OP_CACHE_IMAGE names a registry image (typically
	// <registry>/<image>-buildcache) that the lifecycle restores from and
	// exports to, so warm caches survive runner recycling. Only meaningful for
	// per-artifact invocations (CI passes --artifact); multi-arch or
	// multi-artifact local builds should leave it unset (volume cache default).
	if cacheImage := ensureCacheImage(ctx, os.Getenv("OP_CACHE_IMAGE"), opts.InsecureRegistries); cacheImage != "" {
		buildOpts.Cache = cache.CacheOpts{
			Build: cache.CacheInfo{Format: cache.CacheImage, Source: cacheImage},
		}
		// ALSO set the legacy field: pack's lifecycle builds CNB_REGISTRY_AUTH
		// from opts.CacheImage only (lifecycle_execution.go passes it to
		// auth.BuildEnvVar; Cache.Build.Source is used for the cache object but
		// NOT for auth). With only CacheOpts set, the analyzer hits the cache
		// registry anonymously and fails its read/write validation with
		// UNAUTHORIZED. The cache object itself prefers opts.CacheImage and
		// falls back to Cache.Build.Source, so setting both is coherent.
		buildOpts.CacheImage = cacheImage
	}

	// We might need to handle fetch logic if relying on daemon.

	// Log build options for debugging
	fmt.Printf("[pack] BuildOptions: Image=%s Builder=%s RunImage=%s Target=%s Network=%s Publish=%v InsecureRegistries=%v CacheImage=%s\n",
		opts.ImageName, opts.Builder, opts.RunImage, opts.Target, os.Getenv("OP_PACK_NETWORK"), opts.Publish, opts.InsecureRegistries, buildOpts.CacheImage)

	_, _ = fmt.Fprintf(out, "Building %s using builder %s (publish=%v)...\n", opts.ImageName, opts.Builder, opts.Publish)
	if err := packClient.Build(ctx, buildOpts); err != nil {
		return fmt.Errorf("pack build failed: %w", err)
	}
	return nil
}

// tailKeep is how much lifecycle output is kept for classifying a failed export.
const tailKeep = 64 << 10

// exportRetry is the lifecycle's export retry policy. The lifecycle pushes the image itself, and a registry that drops
// the connection mid-upload (Artifact Registry: "http2: server sent GOAWAY ... server_shutting_down") fails the
// export after the whole build is done. pack then runs the exporter again against the layers already built, so a
// retry costs a re-push of what the registry does not have yet, not a rebuild. Retried only when the output of the
// failed export reads as transient (util.TransientPushText); a refused credential (401/403) fails at once.
func exportRetry(tail *util.TailWriter) func(attempt int, err error) (time.Duration, bool) {
	return func(attempt int, err error) (time.Duration, bool) {
		if attempt >= util.PushAttempts {
			return 0, false
		}
		export := exportSection(tail.String())
		if !util.TransientPushText(export) {
			return 0, false
		}
		wait := util.PushBackoffFor(attempt)
		fmt.Printf("::warning::image export failed on a transient registry error (attempt %d of %d); exporting again in %s without rebuilding\n", attempt, util.PushAttempts, wait)
		return wait, true
	}
}

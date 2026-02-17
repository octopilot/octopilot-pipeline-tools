package pack

import (
	"context"
	"fmt"
	"io"

	"github.com/buildpacks/pack/pkg/client"
	"github.com/buildpacks/pack/pkg/logging"
)

// BuildOptions mimics the options we need for `pack build --publish`
type BuildOptions struct {
	ImageName    string
	Builder      string
	Path         string
	Publish      bool
	ClearCache   bool
	TrustBuilder bool
	Env          map[string]string
	// Registry handling if needed (insecure, etc.)
}

// Build performs a pack build using the library.
func Build(ctx context.Context, opts BuildOptions, out io.Writer) error {
	logger := logging.NewLogWithWriters(out, out)
	packClient, err := client.NewClient(client.WithLogger(logger))
	if err != nil {
		return fmt.Errorf("failed to create pack client: %w", err)
	}

	buildOpts := client.BuildOptions{
		Image:        opts.ImageName,
		Builder:      opts.Builder,
		AppPath:      opts.Path,
		Publish:      opts.Publish,
		ClearCache:   opts.ClearCache,
		TrustBuilder: func(s string) bool { return true }, // Always trust for now (internal tool)
		Env:          opts.Env,
		// Additional options can be mapped here
	}

	// We might need to handle pulling the builder?
	// The client handles it usually.

	fmt.Fprintf(out, "Building %s using builder %s (publish=%v)...\n", opts.ImageName, opts.Builder, opts.Publish)
	if err := packClient.Build(ctx, buildOpts); err != nil {
		return fmt.Errorf("pack build failed: %w", err)
	}
	return nil
}

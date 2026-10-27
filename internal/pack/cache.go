package pack

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
)

// ensureCacheImage makes sure the build-cache image exists before the lifecycle restores from it, and returns the
// image to use, or "" to build without a cache.
//
// GHCR chicken-and-egg: the lifecycle restores (pulls) the cache image before it ever exports one, and GITHUB_TOKEN
// is DENIED (not 404) pulling a package that has never existed, which is fatal to the build. So a missing cache image
// is created as a one-file placeholder; the lifecycle reads an image without cache metadata as an empty cache and
// overwrites it on its first export. A package created this way by a workflow's GITHUB_TOKEN is linked to that
// repository.
//
// This used to be a `docker push` in the workflow. On runners whose Docker daemon pulls through a caching proxy that
// refuses pushes (405 on POST .../blobs/uploads/), that push always failed and every build ran without a cache. op
// talks to the registry itself, like the lifecycle does, so the daemon's proxy is out of the path.
//
// Best-effort: any failure is a warning and a build without the cache, never a failed build.
func ensureCacheImage(ctx context.Context, image string, insecureRegistries []string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	ref, err := name.ParseReference(image, insecureOpts(image, insecureRegistries)...)
	if err != nil {
		fmt.Printf("::warning::build cache %s: invalid reference (%v); building without cache\n", image, err)
		return ""
	}
	ropts := []remote.Option{remote.WithAuthFromKeychain(util.Keychain), remote.WithContext(ctx)}
	_, err = remoteHead(ref, ropts...)
	if err == nil {
		return image
	}
	var terr *transport.Error
	if !errors.As(err, &terr) || (terr.StatusCode != http.StatusNotFound &&
		terr.StatusCode != http.StatusUnauthorized && terr.StatusCode != http.StatusForbidden) {
		fmt.Printf("::warning::build cache %s: cannot check it (%v); building without cache\n", image, err)
		return ""
	}
	// Not there (404), or not visible yet (GHCR: 401/403 for a package that does not exist).
	fmt.Printf("Creating build-cache image (first run): %s\n", image)
	placeholder, err := cachePlaceholder()
	if err != nil {
		fmt.Printf("::warning::build cache %s: placeholder: %v; building without cache\n", image, err)
		return ""
	}
	if err := util.RetryPush(ctx, image, func() error { return remoteWrite(ref, placeholder, ropts...) }); err != nil {
		fmt.Printf("::warning::build cache %s could not be created: %v; building without cache this run\n", image, err)
		return ""
	}
	return image
}

// remoteHead and remoteWrite are variables for tests.
var (
	remoteHead  = remote.Head
	remoteWrite = remote.Write
)

func insecureOpts(image string, insecureRegistries []string) []name.Option {
	for _, reg := range insecureRegistries {
		if strings.HasPrefix(image, reg) {
			return []name.Option{name.Insecure}
		}
	}
	return nil
}

// cachePlaceholder is a one-layer image holding a marker file and no cache metadata.
func cachePlaceholder() (v1.Image, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("octopilot buildcache bootstrap\n")
	if err := tw.WriteHeader(&tar.Header{Name: ".octopilot-buildcache-bootstrap", Mode: 0o644, Size: int64(len(content))}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf.Bytes())), nil
	})
	if err != nil {
		return nil, err
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return nil, err
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.OS = "linux"
	cfg.Architecture = runtime.GOARCH
	return mutate.ConfigFile(img, cfg)
}

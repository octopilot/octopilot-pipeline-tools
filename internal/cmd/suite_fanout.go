package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
)

// suiteImage is one line of suite-images.txt: the cargo bin (process type)
// and the HelmRelease image repository.
type suiteImage struct {
	Bin   string
	Image string
}

// parseSuiteImages reads bin and image pairs. Blank lines and # comments are
// ignored. The image is a repository, not a tag; pushTarget applies the tag.
func parseSuiteImages(path string) ([]suiteImage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()
	var out []suiteImage
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want %q, got %q", path, lineNo, "bin image", line)
		}
		out = append(out, suiteImage{Bin: fields[0], Image: fields[1]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no images", path)
	}
	return out, nil
}

// imageWithEntrypoint returns img with the CNB process for bin as the
// entrypoint and no Cmd, so a chart that sets no command starts that bin.
func imageWithEntrypoint(img v1.Image, bin string) (v1.Image, error) {
	cf, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg := cf.Config
	cfg.Entrypoint = []string{"/cnb/process/" + bin}
	cfg.Cmd = nil
	return mutate.Config(img, cfg)
}

// suiteImagesPath is the fan-out map for a suite build: BP_SUITE_IMAGES
// (relative to the artifact's context) when the artifact names one, so
// several suites in one repository each publish their own services, else
// suite-images.txt at the context root.
func suiteImagesPath(workspace string, env map[string]string) string {
	if p := strings.TrimSpace(env["BP_SUITE_IMAGES"]); p != "" {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(workspace, p)
	}
	return filepath.Join(workspace, "suite-images.txt")
}

// suiteVariants is the binary variants a suite was built with: BP_RUST_VARIANTS=true makes the Rust buildpack ship
// every bin as <bin>-debug (release-optimised, full debug info) and <bin>-release (debug info stripped).
func suiteVariants(env map[string]string) []string {
	switch strings.ToLower(strings.TrimSpace(env["BP_RUST_VARIANTS"])) {
	case "true", "1", "yes":
		return []string{"debug", "release"}
	}
	return nil
}

// expandVariants turns each map line into one line per variant: bin <bin>-<v> published as <image>-<v>. Without
// variants the map is returned as is.
func expandVariants(images []suiteImage, variants []string) []suiteImage {
	if len(variants) == 0 {
		return images
	}
	out := make([]suiteImage, 0, len(images)*len(variants))
	for _, item := range images {
		for _, v := range variants {
			out = append(out, suiteImage{Bin: item.Bin + "-" + v, Image: item.Image + "-" + v})
		}
	}
	return out
}

// fanoutSuite publishes one image per line of the suite's map
// (suiteImagesPath): the suite image with an app layer holding only that
// binary (suite_slim.go) and its process as the entrypoint. The compile
// happens once and the publish does not run cargo. With variants (suiteVariants) each line publishes one image per
// variant, <image>-debug and <image>-release.
//
// Up to parallel images are slimmed and pushed at once (fewer than 1 means 1): each push is mostly the one slim app
// layer (the other layers are mounted from the suite), so one at a time left the uplink and the CPU idle between
// layers, and ~60 variant images took ~40 minutes. The result keeps the map's order whatever order the pushes finish
// in, and the first failure stops images not yet started.
func fanoutSuite(mapPath, suiteRef, repo, tag, ttlUUID string, variants []string, insecure []string, propagation time.Duration, parallel int) ([]util.Build, error) {
	images, err := parseSuiteImages(mapPath)
	if err != nil {
		return nil, err
	}
	images = expandVariants(images, variants)
	srcRef, err := parseReferenceForRemote(suiteRef, insecure)
	if err != nil {
		return nil, fmt.Errorf("parsing suite image %s: %w", suiteRef, err)
	}
	ropts := remoteOptionsFor(suiteRef, insecure)
	desc, err := remote.Get(srcRef, ropts...)
	if err != nil {
		return nil, fmt.Errorf("pulling suite image %s: %w", suiteRef, err)
	}
	if desc.MediaType.IsIndex() {
		return nil, fmt.Errorf("suite image %s is a manifest list; fan-out publishes one platform", suiteRef)
	}
	base, err := desc.Image()
	if err != nil {
		return nil, fmt.Errorf("reading suite image %s: %w", suiteRef, err)
	}
	slim, err := newSlimSuite(base)
	if err != nil {
		return nil, fmt.Errorf("suite image %s: %w", suiteRef, err)
	}
	if slim != nil {
		defer slim.Close()
	}
	publish := func(item suiteImage) (util.Build, error) {
		return publishSuiteImage(item, base, slim, repo, tag, ttlUUID, insecure, propagation)
	}
	return fanoutEach(images, parallel, publish)
}

// fanoutEach runs publish for every image with at most parallel running at once and returns the results in the
// images' order. After the first error no further image is started; the error of the earliest failed image in map
// order is returned, once the pushes already running have finished.
func fanoutEach(images []suiteImage, parallel int, publish func(suiteImage) (util.Build, error)) ([]util.Build, error) {
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(images) {
		parallel = len(images)
	}
	start := time.Now()
	fmt.Printf("Suite fan-out: %d images, %d at a time\n", len(images), parallel)
	results := make([]util.Build, len(images))
	errs := make([]error, len(images))
	var (
		mu     sync.Mutex
		failed bool
		wg     sync.WaitGroup
	)
	sem := make(chan struct{}, parallel)
	for i, item := range images {
		sem <- struct{}{}
		mu.Lock()
		stop := failed
		mu.Unlock()
		if stop {
			<-sem
			break
		}
		wg.Add(1)
		go func(i int, item suiteImage) {
			defer wg.Done()
			defer func() { <-sem }()
			b, err := publish(item)
			if err != nil {
				fmt.Printf("Suite fan-out %s: FAILED: %v\n", item.Bin, err)
				mu.Lock()
				failed = true
				mu.Unlock()
				errs[i] = err
				return
			}
			results[i] = b
		}(i, item)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	fmt.Printf("Suite fan-out: %d images published in %s\n", len(images), time.Since(start).Round(time.Second))
	return results, nil
}

// publishSuiteImage slims the suite image down to item's binary, pushes it and waits until the registry serves it.
func publishSuiteImage(item suiteImage, base v1.Image, slim *slimSuite, repo, tag, ttlUUID string, insecure []string, propagation time.Duration) (util.Build, error) {
	began := time.Now()
	dest, err := pushTarget(item.Image, repo, tag, ttlUUID, imageBasename(item.Image))
	if err != nil {
		return util.Build{}, err
	}
	var img v1.Image
	var dropped int64
	if slim != nil {
		img, dropped, err = slim.image(item.Bin)
	} else {
		img, err = imageWithEntrypoint(base, item.Bin)
	}
	if err != nil {
		return util.Build{}, fmt.Errorf("image for %s: %w", item.Bin, err)
	}
	destRef, err := parseReferenceForRemote(dest, insecure)
	if err != nil {
		return util.Build{}, fmt.Errorf("parsing %s: %w", dest, err)
	}
	if slim != nil {
		fmt.Printf("Suite fan-out %s -> %s (the other binaries left out: %.1f MiB)\n", item.Bin, dest, float64(dropped)/(1<<20))
	} else {
		fmt.Printf("Suite fan-out %s -> %s\n", item.Bin, dest)
	}
	push := func() error { return remoteWrite(destRef, img, remoteOptionsFor(dest, insecure)...) }
	if err := util.RetryPush(context.Background(), dest, push); err != nil {
		if classifyRegistryError(err) == registryAuth {
			return util.Build{}, fmt.Errorf("pushing %s: %w: %v", dest, errRegistryAuth, err)
		}
		return util.Build{}, fmt.Errorf("pushing %s: %w", dest, err)
	}
	if err := waitForImage(dest, propagation, insecure, remoteOptionsFor(dest, insecure)...); err != nil {
		return util.Build{}, err
	}
	head, err := remote.Head(destRef, remoteOptionsFor(dest, insecure)...)
	if err != nil {
		return util.Build{}, fmt.Errorf("digest for %s: %w", dest, err)
	}
	fmt.Printf("Suite fan-out %s: pushed %s in %s\n", item.Bin, head.Digest, time.Since(began).Round(100*time.Millisecond))
	return util.Build{
		ImageName: item.Image,
		Tag:       fmt.Sprintf("%s@%s", dest, head.Digest),
	}, nil
}

func imageBasename(ref string) string {
	base := ref
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, ":"); i > 0 && !strings.Contains(base[i:], "/") {
		base = base[:i]
	}
	return base
}

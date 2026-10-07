package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// fanoutSuite publishes one image per suite-images.txt line. Each image is
// the suite image with a different entrypoint, so the compile happens once
// and the publish does not run cargo.
func fanoutSuite(workspace, suiteRef, repo, tag, ttlUUID string, insecure []string, propagation time.Duration) ([]util.Build, error) {
	mapPath := filepath.Join(workspace, "suite-images.txt")
	images, err := parseSuiteImages(mapPath)
	if err != nil {
		return nil, err
	}
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
	var built []util.Build
	for _, item := range images {
		dest, err := pushTarget(item.Image, repo, tag, ttlUUID, imageBasename(item.Image))
		if err != nil {
			return nil, err
		}
		img, err := imageWithEntrypoint(base, item.Bin)
		if err != nil {
			return nil, fmt.Errorf("entrypoint for %s: %w", item.Bin, err)
		}
		destRef, err := parseReferenceForRemote(dest, insecure)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", dest, err)
		}
		fmt.Printf("Suite fan-out %s -> %s\n", item.Bin, dest)
		if err := remote.Write(destRef, img, remoteOptionsFor(dest, insecure)...); err != nil {
			return nil, fmt.Errorf("pushing %s: %w", dest, err)
		}
		if err := waitForImage(dest, propagation, insecure, remoteOptionsFor(dest, insecure)...); err != nil {
			return nil, err
		}
		head, err := remote.Head(destRef, remoteOptionsFor(dest, insecure)...)
		if err != nil {
			return nil, fmt.Errorf("digest for %s: %w", dest, err)
		}
		built = append(built, util.Build{
			ImageName: item.Image,
			Tag:       fmt.Sprintf("%s@%s", dest, head.Digest),
		})
	}
	return built, nil
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

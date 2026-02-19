package util

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const BuildResultFilename = "build_result.json"

// BuildEntry is a single artifact record in build_result.json.
type BuildEntry struct {
	ImageName string `json:"imageName"`
	Tag       string `json:"tag"` // fully-qualified ref: registry/image:tag@sha256:digest
}

// BuildResult is the contract written by `op build --push` and consumed by
// promote-image, watch-deployment, and attestation steps.
type BuildResult struct {
	Builds []BuildEntry `json:"builds"`
}

// Build is the internal struct used during the build phase before writing.
type Build struct {
	ImageName string
	Tag       string
}

// ReadBuildResult reads build_result.json from the given directory (or cwd if empty).
func ReadBuildResult(dir string) (*BuildResult, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	path := filepath.Join(dir, BuildResultFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var res BuildResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(res.Builds) == 0 {
		return nil, fmt.Errorf("%s: no builds found", path)
	}
	return &res, nil
}

// GetFirstTag returns the tag of the first artifact in build_result.json.
// For multi-artifact builds prefer GetTagForImage to select by name.
func GetFirstTag(res *BuildResult) (string, error) {
	if len(res.Builds) == 0 {
		return "", fmt.Errorf("no builds found")
	}
	return res.Builds[0].Tag, nil
}

// GetTagForImage returns the fully-qualified tag for the named artifact.
// Returns an error if the image name is not present in the result.
func GetTagForImage(res *BuildResult, imageName string) (string, error) {
	for _, b := range res.Builds {
		if b.ImageName == imageName {
			return b.Tag, nil
		}
	}
	names := make([]string, len(res.Builds))
	for i, b := range res.Builds {
		names[i] = b.ImageName
	}
	return "", fmt.Errorf("image %q not found in build_result.json (available: %v)", imageName, names)
}

// SelectTag returns the tag for imageName when set, otherwise falls back to
// the last entry in builds (the application image, not the base image).
// This is the recommended selector for commands that need to pick one artifact.
func SelectTag(res *BuildResult, imageName string) (string, error) {
	if imageName != "" {
		return GetTagForImage(res, imageName)
	}
	// Default: last entry (application image — base images come first by convention).
	if len(res.Builds) == 0 {
		return "", fmt.Errorf("no builds found")
	}
	return res.Builds[len(res.Builds)-1].Tag, nil
}

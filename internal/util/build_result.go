package util

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const BuildResultFilename = "build_result.json"

type BuildResult struct {
	Builds []interface{} `json:"builds"`
}

type BuildEntry struct {
	Tag string `json:"tag"`
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
		return nil, err
	}

	var res BuildResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// GetFirstTag returns the first tag from the build result.
// It handles both string entries and object entries with "tag" field.
func GetFirstTag(res *BuildResult) (string, error) {
	if len(res.Builds) == 0 {
		return "", fmt.Errorf("no builds found")
	}

	first := res.Builds[0]

	// Case 1: String
	if s, ok := first.(string); ok {
		return s, nil
	}

	// Case 2: Object
	if m, ok := first.(map[string]interface{}); ok {
		if t, ok := m["tag"].(string); ok {
			return t, nil
		}
	}

	return "", fmt.Errorf("invalid build entry format")
}

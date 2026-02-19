package util

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const RegistryFilename = ".registry"

type registryFile struct {
	Local        string   `yaml:"local"`
	CI           []string `yaml:"ci"`
	Destinations []string `yaml:"destinations"` // Legacy alias for CI
}

// GetDefaultRepoFromRegistry reads the .registry file from repoRoot and returns
// the most appropriate registry for the current environment.
// In CI (GITHUB_ACTIONS=true) it returns the first CI entry; otherwise local.
// Strings are interpolated with environment variables, including ${VAR:-default} syntax.
func GetDefaultRepoFromRegistry(repoRoot string) string {
	path := filepath.Join(repoRoot, RegistryFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var raw registryFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return ""
	}

	inCI := os.Getenv("GITHUB_ACTIONS") == "true"

	if inCI {
		ciList := raw.CI
		if len(ciList) == 0 {
			ciList = raw.Destinations
		}
		if len(ciList) > 0 {
			return interpolate(ciList[0])
		}
	} else {
		if raw.Local != "" {
			return interpolate(raw.Local)
		}
	}
	return ""
}

// reVarDefault matches ${VAR:-default} or ${VAR:default} (without dash).
var reVarDefault = regexp.MustCompile(`\$\{([^}:]+):-([^}]*)\}`)

// interpolate expands environment variable references in s:
//   - ${VAR}           → value of VAR, empty if unset
//   - $VAR             → value of VAR, empty if unset
//   - ${VAR:-default}  → value of VAR if set and non-empty, else "default"
//   - $$               → literal $
func interpolate(s string) string {
	// Handle ${VAR:-default} first (os.ExpandEnv doesn't support this).
	result := reVarDefault.ReplaceAllStringFunc(s, func(match string) string {
		sub := reVarDefault.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		key, defaultVal := sub[1], sub[2]
		if v := os.Getenv(key); v != "" {
			return v
		}
		return defaultVal
	})
	// Then handle plain $VAR and ${VAR}.
	result = os.ExpandEnv(result)
	return strings.TrimSuffix(strings.TrimSpace(result), "/")
}

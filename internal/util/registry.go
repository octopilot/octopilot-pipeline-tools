package util

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const RegistryFilename = ".registry"

type registryFile struct {
	Local        string   `yaml:"local"`
	CI           []string `yaml:"ci"`
	Destinations []string `yaml:"destinations"` // Legacy alias for CI
}

// LoadRegistryFile reads .registry from repoRoot and returns local and ci registries.
// Strings are interpolated with environment variables.
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

	// Logic: auto destination (CI if GITHUB_ACTIONS, else local)
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

// interpolate replaces ${VAR} and $VAR.
// TODO: Support ${VAR:-default} if needed (currently using os.ExpandEnv which usually leaves empty if unset)
func interpolate(s string) string {
	// standard os.ExpandEnv handles $VAR and ${VAR}
	expanded := os.ExpandEnv(s)
	// If we need defaults support, we'd add regex here.
	// Python: \$\{([^}:]+)(?::-([^}]*))?\}
	// For now, simple expansion.
	return strings.TrimSuffix(strings.TrimSpace(expanded), "/")
}

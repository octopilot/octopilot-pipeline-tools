package util

import (
	"os"

	"github.com/spf13/viper"
)

// ResolveDefaultRepo determines the registry to push to.
// Order:
// 1. Env var SKAFFOLD_DEFAULT_REPO
// 2. Config "default_repo" (viper)
// 3. .registry file (local or ci based on GITHUB_ACTIONS)
// 4. Fallback: localhost:5001
func ResolveDefaultRepo(cwd string) string {
	if repo := os.Getenv("SKAFFOLD_DEFAULT_REPO"); repo != "" {
		return repo
	}

	if repo := viper.GetString("default_repo"); repo != "" {
		return repo
	}

	if repo := GetDefaultRepoFromRegistry(cwd); repo != "" {
		return repo
	}

	return "localhost:5001"
}

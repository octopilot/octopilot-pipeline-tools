package util

import (
	"os"

	"github.com/spf13/viper"
)

// GetWatchDestinationRepository resolves the destination repo for watch-deployment.
// Priority:
// 1. Env: GOOGLE_GKE_IMAGE_<ENV>_REPOSITORY (e.g. GOOGLE_GKE_IMAGE_PROD_REPOSITORY)
// 2. Env: WATCH_DESTINATION_REPOSITORY
// 3. Config: Same keys via viper
func GetWatchDestinationRepository(env string) string {
	// Env mapping
	var envKey string
	switch env {
	case "dev":
		envKey = "GOOGLE_GKE_IMAGE_REPOSITORY"
	case "pp":
		envKey = "GOOGLE_GKE_IMAGE_PP_REPOSITORY"
	case "prod":
		envKey = "GOOGLE_GKE_IMAGE_PROD_REPOSITORY"
	}

	if val := os.Getenv(envKey); val != "" {
		return val
	}
	if val := viper.GetString(envKey); val != "" {
		return val
	}

	if val := os.Getenv("WATCH_DESTINATION_REPOSITORY"); val != "" {
		return val
	}
	return viper.GetString("WATCH_DESTINATION_REPOSITORY")
}

// GetPromoteRepositories resolves source and dest repos for promote-image.
func GetPromoteRepositories(sourceEnv, destEnv string) (string, string) {
	src := getRepoForEnv(sourceEnv)
	if src == "" {
		src = os.Getenv("PROMOTE_SOURCE_REPOSITORY")
	}
	if src == "" {
		src = viper.GetString("PROMOTE_SOURCE_REPOSITORY")
	}

	dest := getRepoForEnv(destEnv)
	if dest == "" {
		dest = os.Getenv("PROMOTE_DESTINATION_REPOSITORY")
	}
	if dest == "" {
		dest = viper.GetString("PROMOTE_DESTINATION_REPOSITORY")
	}

	return src, dest
}

func getRepoForEnv(env string) string {
	var key string
	switch env {
	case "dev":
		key = "GOOGLE_GKE_IMAGE_REPOSITORY"
	case "pp":
		key = "GOOGLE_GKE_IMAGE_PP_REPOSITORY"
	case "prod":
		key = "GOOGLE_GKE_IMAGE_PROD_REPOSITORY"
	}
	if val := os.Getenv(key); val != "" {
		return val
	}
	return viper.GetString(key)
}

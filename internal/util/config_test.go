package util

import (
	"os"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestGetWatchDestinationRepository(t *testing.T) {
	// Reset viper and env
	viper.Reset()
	os.Clearenv()

	// Case 1: viper default
	viper.Set("WATCH_DESTINATION_REPOSITORY", "default-repo")
	assert.Equal(t, "default-repo", GetWatchDestinationRepository("dev"))

	// Case 2: viper specific env
	viper.Set("GOOGLE_GKE_IMAGE_REPOSITORY", "dev-repo")
	assert.Equal(t, "dev-repo", GetWatchDestinationRepository("dev"))
	assert.Equal(t, "default-repo", GetWatchDestinationRepository("prod"))

	// Case 3: Env var override
	os.Setenv("GOOGLE_GKE_IMAGE_PROD_REPOSITORY", "prod-env-repo")
	assert.Equal(t, "prod-env-repo", GetWatchDestinationRepository("prod"))
}

func TestGetPromoteRepositories(t *testing.T) {
	viper.Reset()
	os.Clearenv()

	viper.Set("GOOGLE_GKE_IMAGE_REPOSITORY", "dev-repo")
	viper.Set("GOOGLE_GKE_IMAGE_PROD_REPOSITORY", "prod-repo")

	src, dest := GetPromoteRepositories("dev", "prod")
	assert.Equal(t, "dev-repo", src)
	assert.Equal(t, "prod-repo", dest)

	// Fallback
	viper.Reset()
	viper.Set("PROMOTE_SOURCE_REPOSITORY", "src-fallback")
	viper.Set("PROMOTE_DESTINATION_REPOSITORY", "dest-fallback")

	src, dest = GetPromoteRepositories("dev", "prod")
	assert.Equal(t, "src-fallback", src)
	assert.Equal(t, "dest-fallback", dest)
}

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSuiteImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suite-images.txt")
	err := os.WriteFile(path, []byte(`
# cargo bin, then the HelmRelease repository
pricewhisperer_orders ghcr.io/microscaler/pricewhisperer-orders
traderBFF ghcr.io/microscaler/pricewhisperer-trader-bff
`), 0o644)
	require.NoError(t, err)

	got, err := parseSuiteImages(path)
	require.NoError(t, err)
	require.Equal(t, []suiteImage{
		{Bin: "pricewhisperer_orders", Image: "ghcr.io/microscaler/pricewhisperer-orders"},
		{Bin: "traderBFF", Image: "ghcr.io/microscaler/pricewhisperer-trader-bff"},
	}, got)
}

func TestParseSuiteImagesRejectsAShortLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suite-images.txt")
	require.NoError(t, os.WriteFile(path, []byte("only-one-field\n"), 0o644))
	_, err := parseSuiteImages(path)
	require.Error(t, err)
}

func TestImageWithEntrypointSelectsTheProcess(t *testing.T) {
	img, err := random.Image(1024, 1)
	require.NoError(t, err)
	out, err := imageWithEntrypoint(img, "pricewhisperer_orders")
	require.NoError(t, err)
	cf, err := out.ConfigFile()
	require.NoError(t, err)
	assert.Equal(t, []string{"/cnb/process/pricewhisperer_orders"}, cf.Config.Entrypoint)
	assert.Empty(t, cf.Config.Cmd)
}

func TestImageBasename(t *testing.T) {
	assert.Equal(t, "pricewhisperer-orders", imageBasename("ghcr.io/microscaler/pricewhisperer-orders"))
	assert.Equal(t, "pricewhisperer-orders", imageBasename("ghcr.io/microscaler/pricewhisperer-orders:1.2.3"))
}

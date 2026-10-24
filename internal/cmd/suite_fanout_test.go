package cmd

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestSuiteImagesPathFollowsBPSuiteImages(t *testing.T) {
	ws := "/repo"
	if got := suiteImagesPath(ws, map[string]string{}); got != "/repo/suite-images.txt" {
		t.Fatalf("default: got %s", got)
	}
	if got := suiteImagesPath(ws, map[string]string{"BP_SUITE_IMAGES": "suites/crm.txt"}); got != "/repo/suites/crm.txt" {
		t.Fatalf("relative: got %s", got)
	}
	if got := suiteImagesPath(ws, map[string]string{"BP_SUITE_IMAGES": " /abs/a.txt "}); got != "/abs/a.txt" {
		t.Fatalf("absolute: got %s", got)
	}
}

func TestSuiteVariantsFollowsBPRustVariants(t *testing.T) {
	if v := suiteVariants(map[string]string{}); v != nil {
		t.Fatalf("unset: %v", v)
	}
	if v := suiteVariants(map[string]string{"BP_RUST_VARIANTS": "false"}); v != nil {
		t.Fatalf("false: %v", v)
	}
	if v := suiteVariants(map[string]string{"BP_RUST_VARIANTS": "True"}); !reflect.DeepEqual(v, []string{"debug", "release"}) {
		t.Fatalf("true: %v", v)
	}
}

func TestExpandVariantsPublishesEachBinPerVariant(t *testing.T) {
	in := []suiteImage{{Bin: "news", Image: "ghcr.io/o/pricewhisperer-news"}, {Bin: "social_worker", Image: "ghcr.io/o/pw-social"}}
	if got := expandVariants(in, nil); !reflect.DeepEqual(got, in) {
		t.Fatalf("no variants changed the map: %v", got)
	}
	want := []suiteImage{
		{Bin: "news-debug", Image: "ghcr.io/o/pricewhisperer-news-debug"},
		{Bin: "news-release", Image: "ghcr.io/o/pricewhisperer-news-release"},
		{Bin: "social_worker-debug", Image: "ghcr.io/o/pw-social-debug"},
		{Bin: "social_worker-release", Image: "ghcr.io/o/pw-social-release"},
	}
	if got := expandVariants(in, []string{"debug", "release"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestAVariantKeepsOnlyItsOwnBinary(t *testing.T) {
	assert.False(t, isOtherBinary("workspace/bin/news-debug", "news-debug"))
	assert.True(t, isOtherBinary("workspace/bin/news-release", "news-debug"))
	assert.True(t, isOtherBinary("workspace/bin/news", "news-debug"))
	assert.True(t, isOtherBinary("workspace/bin/news-debug", "news"))
}

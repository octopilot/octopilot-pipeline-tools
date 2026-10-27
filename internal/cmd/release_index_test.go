package cmd

import (
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func archImage(t *testing.T, arch string) v1.Image {
	t.Helper()
	img, err := random.Image(256, 1)
	require.NoError(t, err)
	cf, err := img.ConfigFile()
	require.NoError(t, err)
	cf = cf.DeepCopy()
	cf.OS, cf.Architecture = "linux", arch
	img, err = mutate.ConfigFile(img, cf)
	require.NoError(t, err)
	return img
}

func TestPlatformTagIsThisRunsOwn(t *testing.T) {
	t.Setenv("GITHUB_RUN_ID", "123")
	t.Setenv("GITHUB_RUN_ATTEMPT", "2")
	assert.Equal(t, "r/op:latest-linux-arm64-123-2", platformTag("r/op:latest", "linux/arm64", 2))
	assert.Equal(t, "r/op:latest", platformTag("r/op:latest", "linux/arm64", 1), "a single platform goes straight to the tag")

	t.Setenv("GITHUB_RUN_ID", "")
	a := platformTag("r/op:latest", "linux/amd64", 2)
	assert.Regexp(t, `^r/op:latest-linux-amd64-[0-9a-f]{8}$`, a)
	assert.Equal(t, a, platformTag("r/op:latest", "linux/amd64", 2), "stable within one process")
}

func TestPublishImageIndexCarriesEachPlatform(t *testing.T) {
	fastWait(t)
	host := fakeRegistry(t, nil)
	full := host + "/octopilot/op:latest"
	require.NoError(t, remote.Write(mustRef(t, full+"-a"), archImage(t, "amd64")))
	require.NoError(t, remote.Write(mustRef(t, full+"-b"), archImage(t, "arm64")))

	_, err := publishImage(full, []string{full + "-a", full + "-b"}, []string{"linux/amd64", "linux/arm64"}, nil, 0)
	require.NoError(t, err)

	idx, err := remote.Index(mustRef(t, full))
	require.NoError(t, err)
	m, err := idx.IndexManifest()
	require.NoError(t, err)
	require.Len(t, m.Manifests, 2)
	var arches []string
	for _, d := range m.Manifests {
		require.NotNil(t, d.Platform, "every index entry names its platform")
		arches = append(arches, d.Platform.OS+"/"+d.Platform.Architecture)
	}
	assert.Equal(t, []string{"linux/amd64", "linux/arm64"}, arches)
}

func TestPublishImageRefusesAnImageOfTheWrongPlatform(t *testing.T) {
	fastWait(t)
	host := fakeRegistry(t, nil)
	full := host + "/octopilot/op:latest"
	require.NoError(t, remote.Write(mustRef(t, full+"-a"), archImage(t, "amd64")))
	// What an amd64-only builder produces when asked for arm64.
	require.NoError(t, remote.Write(mustRef(t, full+"-b"), archImage(t, "amd64")))

	_, err := publishImage(full, []string{full + "-a", full + "-b"}, []string{"linux/amd64", "linux/arm64"}, nil, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was built as linux/arm64 but the image is linux/amd64")
	_, err = remote.Head(mustRef(t, full))
	assert.Error(t, err, "nothing is published at the tag")
}

func TestTagAliasPointsAtTheDigestThisRunPushed(t *testing.T) {
	fastWait(t)
	host := fakeRegistry(t, nil)
	tag := host + "/octopilot/op:latest"
	mine := archImage(t, "amd64")
	require.NoError(t, remote.Write(mustRef(t, tag), mine))
	d, err := mine.Digest()
	require.NoError(t, err)
	// Another run pushes to the same tag before this run aliases it.
	require.NoError(t, remote.Write(mustRef(t, tag), archImage(t, "amd64")))

	require.NoError(t, tagAlias(tag+"@"+d.String(), "v9.9.9", nil))
	got, err := remote.Head(mustRef(t, host+"/octopilot/op:v9.9.9"))
	require.NoError(t, err)
	assert.Equal(t, d, got.Digest)
}

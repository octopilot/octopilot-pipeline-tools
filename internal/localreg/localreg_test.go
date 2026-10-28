package localreg

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func start(t *testing.T) *Registry {
	t.Helper()
	r, err := Start(t.TempDir(), "127.0.0.1", "127.0.0.1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestPushAndPullRoundTrip(t *testing.T) {
	r := start(t)
	img, err := random.Image(3<<20, 3)
	require.NoError(t, err)
	ref, err := name.ParseReference(r.Addr+"/pw/pricewhisperer-suite:latest", name.Insecure)
	require.NoError(t, err)
	require.NoError(t, remote.Write(ref, img))

	got, err := remote.Image(ref)
	require.NoError(t, err)
	want, err := img.Digest()
	require.NoError(t, err)
	gotD, err := got.Digest()
	require.NoError(t, err)
	assert.Equal(t, want, gotD)
	ls, err := got.Layers()
	require.NoError(t, err)
	for _, l := range ls {
		rc, err := l.Compressed() // verifies the digest as it reads
		require.NoError(t, err)
		var b bytes.Buffer
		_, err = b.ReadFrom(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
	}

	// By digest too, and a second repository mounts the blobs instead of uploading them.
	_, err = remote.Image(ref.Context().Digest(want.String()))
	require.NoError(t, err)
	other, err := name.ParseReference(r.Addr+"/pw/pricewhisperer-orders:latest", name.Insecure)
	require.NoError(t, err)
	require.NoError(t, remote.Write(other, got))
}

func TestUnknownManifestAndBlob(t *testing.T) {
	r := start(t)
	ref, err := name.ParseReference(r.Addr+"/pw/none:latest", name.Insecure)
	require.NoError(t, err)
	_, err = remote.Image(ref)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MANIFEST_UNKNOWN")

	resp, err := http.Head("http://" + r.Addr + "/v2/pw/none/blobs/sha256:" + string(bytes.Repeat([]byte("0"), 64)))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAWrongDigestIsRefused(t *testing.T) {
	r := start(t)
	resp, err := http.Post("http://"+r.Addr+"/v2/pw/x/blobs/uploads/?digest=sha256:"+string(bytes.Repeat([]byte("1"), 64)), "application/octet-stream", bytes.NewReader([]byte("hello")))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestPlace(t *testing.T) {
	t.Setenv("OP_LOCAL_REGISTRY_HOST", "10.1.2.3")
	p, err := Place("")
	require.NoError(t, err)
	assert.Equal(t, Placement{Listen: "", Host: "10.1.2.3"}, p)

	t.Setenv("OP_LOCAL_REGISTRY_HOST", "")
	p, err = Place("host")
	require.NoError(t, err)
	assert.Equal(t, Placement{Listen: "127.0.0.1", Host: "127.0.0.1", Network: "host"}, p)
}

func TestOwnContainerIDFromMountInfo(t *testing.T) {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Skip("not in a Docker container")
	}
	old := mountInfo
	t.Cleanup(func() { mountInfo = old })
	id := strings.Repeat("ab", 32)
	mountInfo = func() ([]byte, error) {
		return []byte("1 2 0:1 / / rw - overlay overlay rw\n" +
			"3 2 8:1 /var/lib/docker/containers/" + id + "/hostname /etc/hostname rw - ext4 /dev/sda1 rw\n"), nil
	}
	assert.Equal(t, id, ownContainerID())
}

// On an ARC runner the dind daemon's /var/lib/docker sits in a Kubernetes volume, so the bind source names the pod's
// container first and this container second (PriceWhisperer run 40610603655). The second is the one dind knows.
func TestOwnContainerIDTakesTheInnermostID(t *testing.T) {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Skip("not in a Docker container")
	}
	old := mountInfo
	t.Cleanup(func() { mountInfo = old })
	pod := "b8f0c825090746a06f670f2baad961c64854ca65bd243404ca47f2ab0a81b699"
	own := "7f4edfab8e762bd7f5d56d0132873f33d7527ed8d9fdaa9ddce03e36f962043f"
	mountInfo = func() ([]byte, error) {
		return []byte("1 2 0:1 / / rw - overlay overlay rw\n" +
			"388 378 8:1 /var/lib/rancher/k3s/agent/containerd/io.containerd.grpc.v1.cri/containers/" + pod +
			"/volumes/167dd6fd26995d60834622d81e55e93c6acba29b4a197b0cdde0e81bbc2810e5/containers/" + own +
			"/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw\n"), nil
	}
	assert.Equal(t, own, ownContainerID())
}

package cmd

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type entry struct {
	name, link string
	body       string
	dir        bool
}

func layerTar(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755}
		switch {
		case e.dir:
			hdr.Typeflag = tar.TypeDir
		case e.link != "":
			hdr.Typeflag, hdr.Linkname = tar.TypeLink, e.link
		default:
			hdr.Typeflag, hdr.Size = tar.TypeReg, int64(len(e.body))
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if hdr.Typeflag == tar.TypeReg {
			_, err := tw.Write([]byte(e.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

func names(t *testing.T, l v1.Layer) []string {
	t.Helper()
	rc, err := l.Uncompressed()
	require.NoError(t, err)
	defer rc.Close()
	var out []string
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out = append(out, hdr.Name)
	}
}

func layerOf(t *testing.T, b []byte) v1.Layer {
	t.Helper()
	l, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil },
		tarball.WithMediaType(types.DockerLayer))
	require.NoError(t, err)
	return l
}

// suiteImage is a CNB-shaped suite image: a run-image layer, the app layer with two binaries and their docs, and a
// launcher layer, with the lifecycle metadata pointing at the app layer.
func suiteImageFixture(t *testing.T) (v1.Image, v1.Hash) {
	t.Helper()
	run := layerOf(t, layerTar(t, entry{name: "etc/os-release", body: "ID=distroless"}))
	app := layerOf(t, layerTar(t,
		entry{name: "/workspace", dir: true},
		entry{name: "/workspace/bin", dir: true},
		entry{name: "/workspace/bin/orders", body: "orders-binary-0123456789"},
		entry{name: "/workspace/bin/traderBFF", body: "bff-binary-0123456789-0123456789"},
		entry{name: "/workspace/.rust-binaries/traderBFF", link: "/workspace/bin/traderBFF"},
		entry{name: "/workspace/microservices/trader/orders/gen/doc/openapi.yaml", body: "openapi: 3.1.0"},
		entry{name: "/workspace/microservices/trader/traderBFF/impl/config/config.yaml", body: "port: 8080"},
	))
	launcher := layerOf(t, layerTar(t, entry{name: "cnb/process/orders", link: "cnb/lifecycle/launcher"}))
	appID, err := app.DiffID()
	require.NoError(t, err)

	img := mutate.MediaType(empty.Image, types.DockerManifestSchema2)
	img, err = mutate.Append(img,
		mutate.Addendum{Layer: run, History: v1.History{CreatedBy: "run image"}},
		mutate.Addendum{Layer: app, History: v1.History{CreatedBy: "Application Layer"}},
		mutate.Addendum{Layer: launcher, History: v1.History{CreatedBy: "Buildpacks Launcher Config"}},
	)
	require.NoError(t, err)
	cf, err := img.ConfigFile()
	require.NoError(t, err)
	cf = cf.DeepCopy()
	cf.Config.Labels = map[string]string{
		lifecycleMetadataLabel:         fmt.Sprintf(`{"app":[{"sha":%q}],"launcher":{"sha":"sha256:abc"}}`, appID.String()),
		"io.buildpacks.build.metadata": `{"processes":[{"type":"orders","command":["bin/orders"]}]}`,
	}
	cf.History = append(cf.History, v1.History{CreatedBy: "Buildpacks Process Types", EmptyLayer: true})
	img, err = mutate.ConfigFile(img, cf)
	require.NoError(t, err)
	return img, appID
}

func TestSlimLayerTarKeepsOnlyTheBinaryAndTheRest(t *testing.T) {
	src := layerTar(t,
		entry{name: "/workspace/bin", dir: true},
		entry{name: "/workspace/bin/orders", body: "o"},
		entry{name: "/workspace/bin/orders_extra", body: "prefix, not the same binary"},
		entry{name: "/workspace/bin/traderBFF", body: "bff"},
		entry{name: "/workspace/.rust-binaries/traderBFF", link: "/workspace/bin/traderBFF"},
		entry{name: "/workspace/microservices/trader/orders/gen/doc/openapi.yaml", body: "x"},
	)
	var out bytes.Buffer
	dropped, err := slimLayerTar(bytes.NewReader(src), &out, "orders")
	require.NoError(t, err)
	assert.Equal(t, int64(len("prefix, not the same binary")+len("bff")), dropped)

	var got []string
	tr := tar.NewReader(&out)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		got = append(got, hdr.Name)
	}
	assert.Equal(t, []string{"/workspace/bin", "/workspace/bin/orders", "/workspace/microservices/trader/orders/gen/doc/openapi.yaml"}, got)
}

func TestSlimLayerTarRefusesALayerWithoutTheBinary(t *testing.T) {
	src := layerTar(t, entry{name: "/workspace/bin/traderBFF", body: "bff"})
	_, err := slimLayerTar(bytes.NewReader(src), io.Discard, "orders")
	require.ErrorContains(t, err, "no /workspace/bin/orders")
}

func TestSlimSuiteImageReplacesOnlyTheAppLayer(t *testing.T) {
	base, appID := suiteImageFixture(t)
	s, err := newSlimSuite(base)
	require.NoError(t, err)
	require.NotNil(t, s)
	defer s.Close()

	img, dropped, err := s.image("orders")
	require.NoError(t, err)
	assert.Equal(t, int64(len("bff-binary-0123456789-0123456789")), dropped)

	before, err := base.Layers()
	require.NoError(t, err)
	after, err := img.Layers()
	require.NoError(t, err)
	require.Len(t, after, len(before))
	for i := range before {
		bd, _ := before[i].Digest()
		ad, _ := after[i].Digest()
		if i == 1 {
			assert.NotEqual(t, bd, ad, "the app layer is the slim one")
			continue
		}
		assert.Equal(t, bd, ad, "layer %d is shared with the suite image", i)
	}
	assert.Equal(t, []string{
		"/workspace", "/workspace/bin", "/workspace/bin/orders",
		"/workspace/microservices/trader/orders/gen/doc/openapi.yaml",
		"/workspace/microservices/trader/traderBFF/impl/config/config.yaml",
	}, names(t, after[1]))

	mt, err := img.MediaType()
	require.NoError(t, err)
	assert.Equal(t, types.DockerManifestSchema2, mt)

	cf, err := img.ConfigFile()
	require.NoError(t, err)
	assert.Equal(t, []string{"/cnb/process/orders"}, cf.Config.Entrypoint)
	baseCf, err := base.ConfigFile()
	require.NoError(t, err)
	assert.Equal(t, baseCf.History, cf.History)
	slimID, err := after[1].DiffID()
	require.NoError(t, err)
	assert.Equal(t, slimID, cf.RootFS.DiffIDs[1])
	ids, err := appLayerDiffIDs(cf)
	require.NoError(t, err)
	assert.Equal(t, []v1.Hash{slimID}, ids, "the lifecycle metadata names the slim app layer")
	assert.NotContains(t, cf.Config.Labels[lifecycleMetadataLabel], appID.String())
	assert.Equal(t, baseCf.Config.Labels["io.buildpacks.build.metadata"], cf.Config.Labels["io.buildpacks.build.metadata"])
}

func TestNewSlimSuiteNeedsTheLifecycleLabel(t *testing.T) {
	img := mutate.MediaType(empty.Image, types.DockerManifestSchema2)
	img, err := mutate.Append(img, mutate.Addendum{Layer: static.NewLayer([]byte("x"), types.DockerLayer)})
	require.NoError(t, err)
	_, err = newSlimSuite(img)
	require.ErrorContains(t, err, lifecycleMetadataLabel)
}

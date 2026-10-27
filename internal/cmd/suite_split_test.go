package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/octopilot/octopilot-pipeline-tools/internal/localreg"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
)

// splitFixture is a CNB-shaped suite with two services in two variants. bodies overrides a binary's content.
func splitFixture(t *testing.T, bodies map[string]string) v1.Image {
	t.Helper()
	body := func(bin string) string {
		if b, ok := bodies[bin]; ok {
			return b
		}
		return strings.Repeat(bin+"-binary ", 64)
	}
	run := layerOf(t, layerTar(t, entry{name: "etc/os-release", body: "ID=distroless"}))
	app := layerOf(t, layerTar(t,
		entry{name: "/workspace", dir: true},
		entry{name: "/workspace/.rust-binaries", body: "orders-debug\norders-release\ntraderBFF-debug\ntraderBFF-release\n"},
		entry{name: "/workspace/bin", dir: true},
		entry{name: "/workspace/bin/orders-debug", body: body("orders-debug")},
		entry{name: "/workspace/bin/orders-release", body: body("orders-release")},
		entry{name: "/workspace/bin/traderBFF-debug", body: body("traderBFF-debug")},
		entry{name: "/workspace/bin/traderBFF-release", body: body("traderBFF-release")},
		entry{name: "/workspace/microservices/trader/orders/gen/doc/openapi.yaml", body: "openapi: 3.1.0"},
		entry{name: "/workspace/microservices/trader/traderBFF/impl/config/config.yaml", body: "port: 8080"},
	))
	launcher := layerOf(t, layerTar(t, entry{name: "cnb/process/orders-release", link: "cnb/lifecycle/launcher"}))
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
		"io.buildpacks.build.metadata": `{"processes":[{"type":"orders-release"}]}`,
	}
	img, err = mutate.ConfigFile(img, cf)
	require.NoError(t, err)
	return img
}

func digests(t *testing.T, img v1.Image) []string {
	t.Helper()
	ls, err := img.Layers()
	require.NoError(t, err)
	var out []string
	for _, l := range ls {
		d, err := l.Digest()
		require.NoError(t, err)
		out = append(out, d.String())
	}
	return out
}

func TestSplitSuiteImageMakesOneLayerPerBinary(t *testing.T) {
	img, err := splitSuiteImage(splitFixture(t, nil), t.TempDir())
	require.NoError(t, err)
	ls, err := img.Layers()
	require.NoError(t, err)
	// run, rest, four binaries (name order), launcher
	require.Len(t, ls, 7)
	assert.Equal(t, []string{"etc/os-release"}, names(t, ls[0]))
	assert.Equal(t, []string{
		"/workspace", "/workspace/.rust-binaries", "/workspace/bin",
		"/workspace/microservices/trader/orders/gen/doc/openapi.yaml",
		"/workspace/microservices/trader/traderBFF/impl/config/config.yaml",
	}, names(t, ls[1]))
	assert.Equal(t, []string{"/workspace/bin/orders-debug"}, names(t, ls[2]))
	assert.Equal(t, []string{"/workspace/bin/traderBFF-release"}, names(t, ls[5]))
	assert.Equal(t, []string{"cnb/process/orders-release"}, names(t, ls[6]))

	cf, err := img.ConfigFile()
	require.NoError(t, err)
	m, err := readSuiteLayerMap(cf)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Len(t, m.Bins, 4)
	ids, err := appLayerDiffIDs(cf)
	require.NoError(t, err)
	require.Len(t, ids, 5, "the lifecycle metadata names the rest and every binary layer")
	assert.Equal(t, m.Rest, ids[0].String())
	assert.Equal(t, `{"processes":[{"type":"orders-release"}]}`, cf.Config.Labels["io.buildpacks.build.metadata"])
	assert.Contains(t, cf.Config.Labels[lifecycleMetadataLabel], `"launcher":{"sha":"sha256:abc"}`)
	require.Len(t, cf.History, 7)
	assert.Equal(t, "run image", cf.History[0].CreatedBy)
	assert.Equal(t, "Buildpacks Launcher Config", cf.History[6].CreatedBy)
}

// The point of the split: a binary that did not change makes a byte-identical layer, which the registry has already.
func TestSplitLayersAreReproducibleAndOnlyAChangedBinaryChanges(t *testing.T) {
	a, err := splitSuiteImage(splitFixture(t, nil), t.TempDir())
	require.NoError(t, err)
	b, err := splitSuiteImage(splitFixture(t, nil), t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, digests(t, a), digests(t, b), "same input, same layers")

	c, err := splitSuiteImage(splitFixture(t, map[string]string{"orders-release": "a new orders build"}), t.TempDir())
	require.NoError(t, err)
	da, dc := digests(t, a), digests(t, c)
	for i := range da {
		if i == 3 { // orders-release
			assert.NotEqual(t, da[i], dc[i])
			continue
		}
		assert.Equal(t, da[i], dc[i], "layer %d", i)
	}
}

func TestSplitRefusesAHardLinkAcrossLayers(t *testing.T) {
	base, _ := suiteImageFixture(t) // /workspace/.rust-binaries/traderBFF is a hard link to /workspace/bin/traderBFF
	_, err := splitSuiteImage(base, t.TempDir())
	require.ErrorIs(t, err, errSplitUnsupported)
}

func TestServiceFromSplitKeepsOnlyItsBinary(t *testing.T) {
	suite, err := splitSuiteImage(splitFixture(t, nil), t.TempDir())
	require.NoError(t, err)
	cf, err := suite.ConfigFile()
	require.NoError(t, err)
	m, err := readSuiteLayerMap(cf)
	require.NoError(t, err)

	img, err := serviceFromSplit(suite, m, "traderBFF-debug")
	require.NoError(t, err)
	ls, err := img.Layers()
	require.NoError(t, err)
	require.Len(t, ls, 4) // run, rest, traderBFF-debug, launcher
	assert.Equal(t, []string{"/workspace/bin/traderBFF-debug"}, names(t, ls[2]))
	sd := digests(t, suite)
	assert.Equal(t, []string{sd[0], sd[1], sd[4], sd[6]}, digests(t, img), "every layer is one of the suite's")

	scf, err := img.ConfigFile()
	require.NoError(t, err)
	assert.Equal(t, []string{"/cnb/process/traderBFF-debug"}, scf.Config.Entrypoint)
	assert.NotContains(t, scf.Config.Labels, suiteLayersLabel)
	ids, err := appLayerDiffIDs(scf)
	require.NoError(t, err)
	assert.Equal(t, []string{m.Rest, m.Bins["traderBFF-debug"]}, []string{ids[0].String(), ids[1].String()})
	assert.Len(t, scf.History, 4)

	_, err = serviceFromSplit(suite, m, "risk_engine-debug")
	require.ErrorContains(t, err, "no layer for /workspace/bin/risk_engine-debug")
}

// End to end against a registry: the second build changes one binary and uploads that layer (plus configs) only;
// the fan-out mounts every layer from the suite repository and uploads nothing but configs.
func TestSplitSuitePushSendsOnlyWhatChanged(t *testing.T) {
	fastWait(t)
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	dest := host + "/pw/pricewhisperer-suite:latest"
	ref := mustRef(t, dest)

	push := func(img v1.Image) util.UploadStats {
		before := util.Uploads.Snapshot()
		require.NoError(t, remote.Write(ref, img, remoteOptionsFor(dest, nil)...))
		after := util.Uploads.Snapshot()
		return util.UploadStats{Bytes: after.Bytes - before.Bytes, Uploads: after.Uploads - before.Uploads, Mounts: after.Mounts - before.Mounts, Existing: after.Existing - before.Existing}
	}
	first, err := splitSuiteImage(splitFixture(t, nil), t.TempDir())
	require.NoError(t, err)
	s1 := push(first)
	assert.Equal(t, int64(8), s1.Uploads, "7 layers and the config")

	second, err := splitSuiteImage(splitFixture(t, map[string]string{"orders-release": strings.Repeat("new orders ", 50)}), t.TempDir())
	require.NoError(t, err)
	s2 := push(second)
	assert.Equal(t, int64(2), s2.Uploads, "the changed binary's layer and the config")
	assert.Equal(t, int64(6), s2.Existing)
	changed, err := second.Layers()
	require.NoError(t, err)
	size, err := changed[3].Size()
	require.NoError(t, err)
	cfgSize, err := second.RawConfigFile()
	require.NoError(t, err)
	assert.Equal(t, size+int64(len(cfgSize)), s2.Bytes)

	before := util.Uploads.Snapshot()
	images := []suiteImage{{Bin: "orders", Image: "pricewhisperer/pricewhisperer-orders"}, {Bin: "traderBFF", Image: "pricewhisperer/pricewhisperer-trader-bff"}}
	out, err := fanoutSuite(writeMap(t, images), dest, host+"/pw", "latest", "", []string{"debug", "release"}, nil, time.Second, 2)
	require.NoError(t, err)
	require.Len(t, out, 4)
	after := util.Uploads.Snapshot()
	assert.Equal(t, int64(4), after.Uploads-before.Uploads, "one config per image, no layer")
	// A registry mounts each layer from the suite repository, or (like this test registry, which keeps one blob store)
	// already has it: either way none is sent.
	assert.Equal(t, int64(16), (after.Mounts-before.Mounts)+(after.Existing-before.Existing), "four layers per image, none uploaded")
	assert.Equal(t, int64(4), after.Manifests-before.Manifests)
}

func writeMap(t *testing.T, images []suiteImage) string {
	t.Helper()
	var b bytes.Buffer
	for _, i := range images {
		fmt.Fprintf(&b, "%s %s\n", i.Bin, i.Image)
	}
	p := t.TempDir() + "/suite-images.txt"
	require.NoError(t, os.WriteFile(p, b.Bytes(), 0o644))
	return p
}

// The whole op path after the lifecycle: the suite as published to op's local registry is split and pushed to the
// target; the second build, with one binary changed, sends that binary's layer and the config only.
func TestPushSplitSuiteFromTheLocalRegistry(t *testing.T) {
	fastWait(t)
	local, err := localreg.Start(t.TempDir(), "127.0.0.1", "127.0.0.1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = local.Close() })
	target := httptest.NewServer(registry.New())
	t.Cleanup(target.Close)
	host := strings.TrimPrefix(target.URL, "http://")
	dest := host + "/pw/pricewhisperer-suite:latest"

	publish := func(img v1.Image) string {
		ref := local.Addr + "/pw/pricewhisperer-suite:latest"
		r, err := name.ParseReference(ref, name.Insecure)
		require.NoError(t, err)
		require.NoError(t, remote.Write(r, img))
		return ref
	}
	require.NoError(t, pushSplitSuite(context.Background(), publish(splitFixture(t, nil)), dest, "", nil))
	got, err := remote.Image(mustRef(t, dest))
	require.NoError(t, err)
	cf, err := got.ConfigFile()
	require.NoError(t, err)
	m, err := readSuiteLayerMap(cf)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Len(t, m.Bins, 4)

	before := util.Uploads.Snapshot()
	require.NoError(t, pushSplitSuite(context.Background(), publish(splitFixture(t, map[string]string{"traderBFF-debug": "changed"})), dest, "", nil))
	after := util.Uploads.Snapshot()
	assert.Equal(t, int64(2), after.Uploads-before.Uploads, "the changed binary's layer and the config")
}

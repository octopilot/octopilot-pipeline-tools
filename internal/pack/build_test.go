package pack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
)

const goawayExport = "===> EXPORTING\nSaving us-docker.pkg.dev/pw-ctl/integration/pricewhisperer/pricewhisperer-suite:latest...\n" +
	`ERROR: failed to export: failed to write image to the following tags: [us-docker.pkg.dev/pw-ctl/integration/pricewhisperer/pricewhisperer-suite:latest: ` +
	`Patch "https://us-docker.pkg.dev/artifacts-uploads/namespaces/pw-ctl/repositories/integration/uploads/pnN9weUEvm9tzARD": ` +
	`http2: server sent GOAWAY and closed the connection; LastStreamID=41, ErrCode=NO_ERROR, debug="server_shutting_down"]` + "\n"

func lifecycleOutput(s string) *util.TailWriter {
	tail := util.NewTailWriter(tailKeep)
	_, _ = tail.Write([]byte(s))
	return tail
}

func TestExportRetryRetriesTheV0_1_148Failure(t *testing.T) {
	policy := exportRetry(lifecycleOutput("===> BUILDING\n   Compiling pw v0.1.148\n" + goawayExport))
	wait, retry := policy(1, nil)
	assert.True(t, retry)
	assert.Equal(t, util.PushBackoffFor(1), wait)
}

func TestExportRetryIsBounded(t *testing.T) {
	policy := exportRetry(lifecycleOutput(goawayExport))
	for attempt := 1; attempt < util.PushAttempts; attempt++ {
		_, retry := policy(attempt, nil)
		assert.True(t, retry, "retry %d", attempt)
	}
	_, retry := policy(util.PushAttempts, nil)
	assert.False(t, retry, "%d attempts in all", util.PushAttempts)
}

func TestExportRetryFailsAtOnceOnARefusedCredential(t *testing.T) {
	for _, out := range []string{
		"===> EXPORTING\nERROR: failed to export: UNAUTHORIZED: authentication required\n",
		"===> EXPORTING\nERROR: failed to export: DENIED: Permission \"artifactregistry.repositories.uploadArtifacts\" denied\n",
		"===> EXPORTING\nERROR: failed to export: unexpected status code 403 Forbidden\n",
	} {
		_, retry := exportRetry(lifecycleOutput(out))(1, nil)
		assert.False(t, retry, out)
	}
}

func TestExportRetryReadsOnlyTheLatestExport(t *testing.T) {
	// Build output mentioning "denied" does not stop a GOAWAY from being retried...
	_, retry := exportRetry(lifecycleOutput("test auth::rejects_denied_token ... ok\n" + goawayExport))(1, nil)
	assert.True(t, retry)
	// ...and a GOAWAY from an earlier export does not make a refused credential in the latest one retried.
	out := goawayExport + "===> EXPORTING\nERROR: failed to export: UNAUTHORIZED: authentication required\n"
	_, retry = exportRetry(lifecycleOutput(out))(2, nil)
	assert.False(t, retry)
}

func TestExportRetryIgnoresAnUnknownFailure(t *testing.T) {
	_, retry := exportRetry(lifecycleOutput("===> EXPORTING\nERROR: failed to export: layer is not a tar\n"))(1, nil)
	assert.False(t, retry)
}

func TestExportSectionWithoutTheMarker(t *testing.T) {
	long := strings.Repeat("x", 20<<10) + "GOAWAY"
	got := exportSection(long)
	assert.Len(t, got, 8<<10)
	assert.True(t, strings.HasSuffix(got, "GOAWAY"))
}

// cacheRegistry is an in-memory registry; hook may answer a request itself.
func cacheRegistry(t *testing.T, hook func(w http.ResponseWriter, r *http.Request) bool) string {
	t.Helper()
	reg := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hook != nil && hook(w, r) {
			return
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func fastRetry(t *testing.T) {
	t.Helper()
	old := util.PushBackoff
	util.PushBackoff = time.Millisecond
	t.Cleanup(func() { util.PushBackoff = old })
}

func TestEnsureCacheImageKeepsAnExistingCache(t *testing.T) {
	var writes atomic.Int32
	host := cacheRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/") {
			writes.Add(1)
		}
		return false
	})
	image := host + "/microscaler/pricewhisperer-suite-buildcache"
	img, err := random.Image(64, 1)
	require.NoError(t, err)
	require.NoError(t, remote.Write(mustRef(t, image), img))
	writes.Store(0)

	assert.Equal(t, image, ensureCacheImage(context.Background(), image, nil))
	assert.Equal(t, int32(0), writes.Load(), "an existing cache is not overwritten")
}

func TestEnsureCacheImageCreatesAMissingCache(t *testing.T) {
	host := cacheRegistry(t, nil)
	image := host + "/microscaler/pricewhisperer-suite-buildcache"

	assert.Equal(t, image, ensureCacheImage(context.Background(), image, nil))

	got, err := remote.Image(mustRef(t, image))
	require.NoError(t, err)
	layers, err := got.Layers()
	require.NoError(t, err)
	assert.Len(t, layers, 1)
	cfg, err := got.ConfigFile()
	require.NoError(t, err)
	assert.Equal(t, "linux", cfg.OS)
	_, hasCacheMetadata := cfg.Config.Labels["io.buildpacks.lifecycle.cache.metadata"]
	assert.False(t, hasCacheMetadata, "the lifecycle must read the placeholder as an empty cache")
}

// GHCR answers 403 (not 404) for a package that does not exist yet; that is the case the bootstrap is for.
func TestEnsureCacheImageCreatesACacheGHCRHidesBehind403(t *testing.T) {
	host := cacheRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/manifests/") {
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		return false
	})
	image := host + "/microscaler/x-buildcache"
	assert.Equal(t, image, ensureCacheImage(context.Background(), image, nil))
}

// The runners' Docker daemon proxy answered 405 to every upload; any refused write means a build without the cache,
// never a failed build.
func TestEnsureCacheImageBuildsWithoutCacheWhenTheWriteIsRefused(t *testing.T) {
	fastRetry(t)
	for _, code := range []int{http.StatusMethodNotAllowed, http.StatusForbidden, http.StatusUnauthorized} {
		host := cacheRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
				w.WriteHeader(code)
				return true
			}
			return false
		})
		assert.Equal(t, "", ensureCacheImage(context.Background(), host+"/microscaler/x-buildcache", nil), "status %d", code)
	}
}

func TestEnsureCacheImageBuildsWithoutCacheWhenTheRegistryIsDown(t *testing.T) {
	host := cacheRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusBadGateway)
		return true
	})
	assert.Equal(t, "", ensureCacheImage(context.Background(), host+"/microscaler/x-buildcache", nil))
}

func TestEnsureCacheImageWithoutACacheImage(t *testing.T) {
	assert.Equal(t, "", ensureCacheImage(context.Background(), "  ", nil))
}

func mustRef(t *testing.T, s string) name.Reference {
	t.Helper()
	ref, err := name.ParseReference(s)
	require.NoError(t, err)
	return ref
}

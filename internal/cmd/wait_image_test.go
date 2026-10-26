package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
)

// fastWait makes waitForImage poll every few milliseconds for the test.
func fastWait(t *testing.T) {
	t.Helper()
	oldInitial, oldMax, oldHead := waitInitialInterval, waitMaxInterval, remoteHead
	waitInitialInterval, waitMaxInterval, remoteHead = 5*time.Millisecond, 20*time.Millisecond, remote.Head
	t.Cleanup(func() { waitInitialInterval, waitMaxInterval, remoteHead = oldInitial, oldMax, oldHead })
}

// fakeRegistry is an in-memory registry behind a hook that can answer manifest requests itself.
func fakeRegistry(t *testing.T, hook func(w http.ResponseWriter, r *http.Request) bool) string {
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

func mustRef(t *testing.T, tag string) name.Reference {
	t.Helper()
	ref, err := name.ParseReference(tag)
	require.NoError(t, err)
	return ref
}

func isManifest(r *http.Request) bool { return strings.Contains(r.URL.Path, "/manifests/") }

func TestWaitForImageFindsAnImageAlreadyThere(t *testing.T) {
	fastWait(t)
	host := fakeRegistry(t, nil)
	tag := host + "/suite/orders:latest"
	img, err := random.Image(256, 1)
	require.NoError(t, err)
	require.NoError(t, remote.Write(mustRef(t, tag), img))

	require.NoError(t, waitForImage(tag, time.Second, nil))
}

func TestWaitForImageRetriesNotFoundUntilTheImageAppears(t *testing.T) {
	fastWait(t)
	var heads atomic.Int32
	host := fakeRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
		if isManifest(r) && r.Method == http.MethodHead {
			heads.Add(1)
		}
		return false
	})
	tag := host + "/suite/orders:latest"
	img, err := random.Image(256, 1)
	require.NoError(t, err)
	go func() {
		for heads.Load() < 3 {
			time.Sleep(time.Millisecond)
		}
		_ = remote.Write(mustRef(t, tag), img)
	}()

	require.NoError(t, waitForImage(tag, 5*time.Second, nil))
	assert.GreaterOrEqual(t, heads.Load(), int32(3))
}

func TestWaitForImageFailsAtOnceWhenCredentialsAreRefused(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			fastWait(t)
			var heads atomic.Int32
			host := fakeRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/v2/" {
					// A challenge the anonymous client can answer, so the manifest request is what is refused.
					w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
					w.WriteHeader(http.StatusUnauthorized)
					return true
				}
				if isManifest(r) {
					heads.Add(1)
					w.WriteHeader(code)
					return true
				}
				return false
			})
			start := time.Now()
			err := waitForImage(host+"/suite/orders:latest", time.Minute, nil)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errRegistryAuth), "want errRegistryAuth, got %v", err)
			assert.Contains(t, err.Error(), "registry credentials expired or refused")
			assert.Contains(t, err.Error(), fmt.Sprint(code))
			assert.Less(t, time.Since(start), 5*time.Second, "a refused credential must not wait for the timeout")
			assert.Equal(t, int32(1), heads.Load(), "a refused credential is not retried")
		})
	}
}

func TestWaitForImageRetriesTransientErrors(t *testing.T) {
	fastWait(t)
	var heads atomic.Int32
	host := fakeRegistry(t, func(w http.ResponseWriter, r *http.Request) bool {
		if isManifest(r) && r.Method == http.MethodHead && heads.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return true
		}
		return false
	})
	tag := host + "/suite/orders:latest"
	img, err := random.Image(256, 1)
	require.NoError(t, err)
	require.NoError(t, remote.Write(mustRef(t, tag), img))

	// go-containerregistry retries a 503 itself too; either way the wait must come through it.
	require.NoError(t, waitForImage(tag, 30*time.Second, nil))
	assert.GreaterOrEqual(t, heads.Load(), int32(3))
}

func TestWaitForImageTimeoutCarriesTheLastError(t *testing.T) {
	fastWait(t)
	host := fakeRegistry(t, nil)
	err := waitForImage(host+"/suite/never:latest", 100*time.Millisecond, nil)
	require.Error(t, err)
	assert.False(t, errors.Is(err, errRegistryAuth))
	assert.Contains(t, err.Error(), "timeout waiting for image")
	assert.Contains(t, err.Error(), "last error")
	assert.Contains(t, err.Error(), "404")
}

func TestClassifyRegistryError(t *testing.T) {
	cases := map[int]registryErrorKind{
		401: registryAuth, 403: registryAuth, 404: registryNotFound,
		429: registryTransient, 500: registryTransient, 503: registryTransient, 400: registryFatal,
	}
	for code, want := range cases {
		err := fmt.Errorf("wrapped: %w", &transport.Error{StatusCode: code})
		assert.Equal(t, want, classifyRegistryError(err), "HTTP %d", code)
	}
	assert.Equal(t, registryTransient, classifyRegistryError(errors.New("connection reset by peer")))
}

func TestFanoutEachBoundsConcurrencyAndKeepsMapOrder(t *testing.T) {
	var images []suiteImage
	for i := 0; i < 20; i++ {
		images = append(images, suiteImage{Bin: fmt.Sprintf("bin%02d", i), Image: fmt.Sprintf("ghcr.io/x/img%02d", i)})
	}
	var inFlight, peak atomic.Int32
	publish := func(item suiteImage) (util.Build, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		// Later images finish first, so the result order cannot come from completion order.
		time.Sleep(time.Duration(20-len(item.Bin)%7) * time.Millisecond)
		inFlight.Add(-1)
		return util.Build{ImageName: item.Image, Tag: item.Image + ":latest@sha256:" + item.Bin}, nil
	}

	got, err := fanoutEach(images, 4, publish)
	require.NoError(t, err)
	require.Len(t, got, len(images))
	for i, item := range images {
		assert.Equal(t, item.Image, got[i].ImageName, "result %d out of map order", i)
	}
	assert.LessOrEqual(t, peak.Load(), int32(4))
	assert.Greater(t, peak.Load(), int32(1), "the fan-out should run images concurrently")
}

func TestFanoutEachStopsStartingImagesAfterAFailure(t *testing.T) {
	var images []suiteImage
	for i := 0; i < 30; i++ {
		images = append(images, suiteImage{Bin: fmt.Sprintf("bin%02d", i), Image: fmt.Sprintf("ghcr.io/x/img%02d", i)})
	}
	var mu sync.Mutex
	started := map[string]bool{}
	boom := fmt.Errorf("pushing: %w", errRegistryAuth)
	publish := func(item suiteImage) (util.Build, error) {
		mu.Lock()
		started[item.Bin] = true
		mu.Unlock()
		if item.Bin == "bin02" {
			return util.Build{}, boom
		}
		time.Sleep(5 * time.Millisecond)
		return util.Build{ImageName: item.Image}, nil
	}

	got, err := fanoutEach(images, 3, publish)
	require.ErrorIs(t, err, errRegistryAuth)
	assert.Nil(t, got)
	mu.Lock()
	defer mu.Unlock()
	assert.Less(t, len(started), len(images), "images after the failure should not all start")
}

func TestFanoutEachOneAtATimeBelowOne(t *testing.T) {
	images := []suiteImage{{Bin: "a", Image: "x/a"}, {Bin: "b", Image: "x/b"}}
	var inFlight, peak atomic.Int32
	got, err := fanoutEach(images, 0, func(item suiteImage) (util.Build, error) {
		if n := inFlight.Add(1); n > peak.Load() {
			peak.Store(n)
		}
		time.Sleep(2 * time.Millisecond)
		inFlight.Add(-1)
		return util.Build{ImageName: item.Image}, nil
	})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, int32(1), peak.Load())
}

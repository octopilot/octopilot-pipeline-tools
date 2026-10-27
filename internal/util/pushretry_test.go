package util

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fastPushRetry(t *testing.T) {
	t.Helper()
	oldBackoff, oldMax := PushBackoff, PushMaxBackoff
	PushBackoff, PushMaxBackoff = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { PushBackoff, PushMaxBackoff = oldBackoff, oldMax })
}

// goaway is the error Artifact Registry gave release v0.1.148 mid-layer.
var goaway = errors.New(`Patch "https://us-docker.pkg.dev/artifacts-uploads/namespaces/pw-ctl/repositories/integration/uploads/x": ` +
	`http2: server sent GOAWAY and closed the connection; LastStreamID=41, ErrCode=NO_ERROR, debug="server_shutting_down"`)

func TestTransientPushError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"http2 GOAWAY", goaway, true},
		{"connection reset", errors.New("write tcp 10.0.0.1:443: connection reset by peer"), true},
		{"EOF", errors.New(`Put "https://ghcr.io/v2/x/blobs/uploads/y": EOF`), true},
		{"unexpected EOF", errors.New("unexpected EOF"), true},
		{"503", &transport.Error{StatusCode: http.StatusServiceUnavailable}, true},
		{"500", &transport.Error{StatusCode: http.StatusInternalServerError}, true},
		{"429", &transport.Error{StatusCode: http.StatusTooManyRequests}, true},
		{"408", &transport.Error{StatusCode: http.StatusRequestTimeout}, true},
		{"401", &transport.Error{StatusCode: http.StatusUnauthorized}, false},
		{"403", &transport.Error{StatusCode: http.StatusForbidden}, false},
		{"400", &transport.Error{StatusCode: http.StatusBadRequest}, false},
		{"404", &transport.Error{StatusCode: http.StatusNotFound}, false},
		{"wrapped 503", fmt.Errorf("pushing x: %w", &transport.Error{StatusCode: 503}), true},
		{"wrapped 401", fmt.Errorf("pushing x: %w", &transport.Error{StatusCode: 401}), false},
		{"denied text with EOF", errors.New("DENIED: permission_denied: write_package; EOF"), false},
		{"canceled", context.Canceled, false},
		{"unknown", errors.New("manifest references a missing blob"), false},
		{"marked", MarkTransient(errors.New("exit status 1")), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, TransientPushError(tc.err))
		})
	}
}

func TestTransientPushTextFromLifecycleExport(t *testing.T) {
	export := "===> EXPORTING\nSaving us-docker.pkg.dev/pw-ctl/integration/pricewhisperer/pricewhisperer-suite:latest...\n" +
		"ERROR: failed to export: failed to write image to the following tags: [...: " + goaway.Error() + "]\n"
	assert.True(t, TransientPushText(export))
	assert.False(t, TransientPushText("ERROR: failed to export: UNAUTHORIZED: authentication required"))
	assert.False(t, TransientPushText("ERROR: failed to export: unexpected status code 403 Forbidden"))
	assert.True(t, TransientPushText("ERROR: failed to export: unexpected status code 502 Bad Gateway"))
	assert.False(t, TransientPushText("ERROR: failed to build: exit status 101"))
}

func TestPushBackoffDoublesToTheCap(t *testing.T) {
	assert.Equal(t, 5*time.Second, PushBackoffFor(1))
	assert.Equal(t, 10*time.Second, PushBackoffFor(2))
	assert.Equal(t, 20*time.Second, PushBackoffFor(3))
	assert.Equal(t, 40*time.Second, PushBackoffFor(4))
	assert.Equal(t, 40*time.Second, PushBackoffFor(9))
}

func TestRetryPushRetriesTransientErrorsUntilItSucceeds(t *testing.T) {
	fastPushRetry(t)
	calls := 0
	err := RetryPush(context.Background(), "x", func() error {
		calls++
		if calls < 3 {
			return goaway
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestRetryPushIsBounded(t *testing.T) {
	fastPushRetry(t)
	calls := 0
	err := RetryPush(context.Background(), "x", func() error {
		calls++
		return &transport.Error{StatusCode: http.StatusBadGateway}
	})
	require.Error(t, err)
	assert.Equal(t, PushAttempts, calls)
}

func TestRetryPushFailsAtOnceOnARefusedCredential(t *testing.T) {
	fastPushRetry(t)
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		calls := 0
		err := RetryPush(context.Background(), "x", func() error {
			calls++
			return &transport.Error{StatusCode: code}
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls, "status %d", code)
	}
}

func TestRetryPushStopsWhenTheContextEnds(t *testing.T) {
	old := PushBackoff
	PushBackoff = time.Hour
	t.Cleanup(func() { PushBackoff = old })
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	err := RetryPush(ctx, "x", func() error { calls++; return goaway })
	require.ErrorIs(t, err, goaway)
	assert.Equal(t, 1, calls)
}

// A registry that drops the connection in the middle of the first blob upload, like Artifact Registry's GOAWAY: the
// retried push completes.
func TestRetryPushCompletesAPushTheRegistryDropped(t *testing.T) {
	fastPushRetry(t)
	reg := registry.New()
	var dropped, uploads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/blobs/uploads/") && (r.Method == http.MethodPatch || r.Method == http.MethodPut) {
			uploads.Add(1)
			if dropped.CompareAndSwap(0, 1) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	ref, err := name.ParseReference(strings.TrimPrefix(srv.URL, "http://") + "/pw/suite:latest")
	require.NoError(t, err)
	img, err := random.Image(1024, 3)
	require.NoError(t, err)

	err = RetryPush(context.Background(), ref.String(), func() error {
		return remote.Write(ref, img, remote.WithRetryBackoff(remote.Backoff{Steps: 1}))
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), dropped.Load())

	got, err := remote.Image(ref)
	require.NoError(t, err)
	want, _ := img.Digest()
	have, _ := got.Digest()
	assert.Equal(t, want, have)
	assert.Positive(t, uploads.Load())
}

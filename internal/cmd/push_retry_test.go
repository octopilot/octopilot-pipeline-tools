package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
)

func fastPushRetry(t *testing.T) {
	t.Helper()
	old := util.PushBackoff
	util.PushBackoff = time.Millisecond
	t.Cleanup(func() { util.PushBackoff = old })
}

var errGoaway = errors.New(`Patch "https://us-docker.pkg.dev/artifacts-uploads/x": http2: server sent GOAWAY and closed the connection; LastStreamID=41, ErrCode=NO_ERROR, debug="server_shutting_down"`)

func TestTagAliasRetriesADroppedConnection(t *testing.T) {
	fastPushRetry(t)
	fastWait(t)
	host := fakeRegistry(t, nil)
	tag := host + "/pw/suite:latest"
	img, err := random.Image(256, 1)
	require.NoError(t, err)
	require.NoError(t, remote.Write(mustRef(t, tag), img))

	old := remoteTag
	t.Cleanup(func() { remoteTag = old })
	calls := 0
	remoteTag = func(tag name.Tag, t remote.Taggable, opts ...remote.Option) error {
		calls++
		if calls == 1 {
			return errGoaway
		}
		return remote.Tag(tag, t, opts...)
	}

	require.NoError(t, tagAlias(tag, "v0.1.149", nil))
	assert.Equal(t, 2, calls)
	_, err = remote.Head(mustRef(t, host+"/pw/suite:v0.1.149"))
	require.NoError(t, err)
}

func TestTagAliasFailsAtOnceOnARefusedCredential(t *testing.T) {
	fastPushRetry(t)
	host := fakeRegistry(t, nil)
	tag := host + "/pw/suite:latest"
	img, err := random.Image(256, 1)
	require.NoError(t, err)
	require.NoError(t, remote.Write(mustRef(t, tag), img))

	old := remoteTag
	t.Cleanup(func() { remoteTag = old })
	calls := 0
	remoteTag = func(name.Tag, remote.Taggable, ...remote.Option) error {
		calls++
		return &transport.Error{StatusCode: http.StatusForbidden}
	}
	require.Error(t, tagAlias(tag, "v0.1.149", nil))
	assert.Equal(t, 1, calls)
}

func TestPublishSuiteImageRetriesADroppedPush(t *testing.T) {
	fastPushRetry(t)
	fastWait(t)
	host := fakeRegistry(t, nil)
	base, err := random.Image(256, 2)
	require.NoError(t, err)

	old := remoteWrite
	t.Cleanup(func() { remoteWrite = old })
	calls := 0
	remoteWrite = func(ref name.Reference, img v1.Image, opts ...remote.Option) error {
		calls++
		if calls == 1 {
			return errGoaway
		}
		return remote.Write(ref, img, opts...)
	}

	b, err := publishSuiteImage(suiteImage{Bin: "orders", Image: "pricewhisperer/orders"}, base, nil, host+"/pw", "latest", "", nil, time.Second)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Contains(t, b.Tag, host+"/pw/orders:latest@sha256:")
}

func TestPublishSuiteImageKeepsFailingFastOnARefusedCredential(t *testing.T) {
	fastPushRetry(t)
	host := fakeRegistry(t, nil)
	base, err := random.Image(256, 1)
	require.NoError(t, err)

	old := remoteWrite
	t.Cleanup(func() { remoteWrite = old })
	calls := 0
	remoteWrite = func(name.Reference, v1.Image, ...remote.Option) error {
		calls++
		return &transport.Error{StatusCode: http.StatusUnauthorized}
	}
	_, err = publishSuiteImage(suiteImage{Bin: "orders", Image: "pricewhisperer/orders"}, base, nil, host+"/pw", "latest", "", nil, time.Second)
	require.ErrorIs(t, err, errRegistryAuth)
	assert.Equal(t, 1, calls)
}

func TestDockerBuildPushRetriesATransientPushFailure(t *testing.T) {
	fastPushRetry(t)
	old := runDocker
	t.Cleanup(func() { runDocker = old })
	calls := 0
	runDocker = func(_ context.Context, _, _ []string, _, stderr io.Writer) error {
		calls++
		if calls == 1 {
			_, _ = fmt.Fprintln(stderr, "ERROR: failed to push ghcr.io/o/x:1: unexpected status from PUT request: 502 Bad Gateway")
			return errors.New("exit status 1")
		}
		return nil
	}
	require.NoError(t, dockerBuildPush(context.Background(), []string{"build"}, nil))
	assert.Equal(t, 2, calls)
}

func TestDockerBuildPushDoesNotRetryABuildFailureOrRefusedCredential(t *testing.T) {
	fastPushRetry(t)
	old := runDocker
	t.Cleanup(func() { runDocker = old })
	for _, out := range []string{
		"ERROR: failed to solve: process \"/bin/sh -c make\" did not complete successfully: exit code: 2",
		"ERROR: failed to push ghcr.io/o/x:1: unexpected status from POST request: 403 Forbidden",
	} {
		calls := 0
		runDocker = func(_ context.Context, _, _ []string, _, stderr io.Writer) error {
			calls++
			_, _ = fmt.Fprintln(stderr, out)
			return errors.New("exit status 1")
		}
		require.Error(t, dockerBuildPush(context.Background(), []string{"build"}, nil))
		assert.Equal(t, 1, calls, out)
	}
}

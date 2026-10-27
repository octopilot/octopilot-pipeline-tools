package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	opID    = "dd6e2f3d1f88f75894301e94c0f75cd2682046036f85daa4d997daf4ee84ccac"
	outerID = "35653f4b539cf8068962d5be2066902d676e1d211db2a39fc8be6b5d1ddea7d2"
)

// fakeDaemon knows the containers in byID; a short id resolves by prefix, as Docker does.
func fakeDaemon(t *testing.T, host string, byID map[string]container.InspectResponse) {
	t.Helper()
	oldInspect, oldHost := inspectContainer, hostname
	t.Cleanup(func() { inspectContainer, hostname = oldInspect, oldHost })
	hostname = func() (string, error) { return host, nil }
	inspectContainer = func(_ context.Context, id string) (container.InspectResponse, error) {
		for full, c := range byID {
			if len(id) > 0 && len(id) <= len(full) && full[:len(id)] == id {
				c.ID = full
				return c, nil
			}
		}
		return container.InspectResponse{}, errors.New("No such container: " + id)
	}
}

func running(host string) container.InspectResponse {
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Running: true}},
		Config:            &container.Config{Hostname: host},
	}
}

func TestOwnContainerOnDaemonTakesAKnownMountinfoID(t *testing.T) {
	fakeDaemon(t, opID[:12], map[string]container.InspectResponse{opID: running(opID[:12])})
	id, err := ownContainerOnDaemon(context.Background(), opID)
	require.NoError(t, err)
	assert.Equal(t, opID, id)
}

func TestOwnContainerOnDaemonFallsBackToTheHostname(t *testing.T) {
	// ARC: mountinfo names a container the dind daemon does not have; the hostname is op's short id.
	fakeDaemon(t, opID[:12], map[string]container.InspectResponse{opID: running(opID[:12])})
	id, err := ownContainerOnDaemon(context.Background(), outerID)
	require.NoError(t, err)
	assert.Equal(t, opID, id)
}

func TestOwnContainerOnDaemonRefusesWhatItCannotVerify(t *testing.T) {
	fakeDaemon(t, "runner-pod-7xk2p", map[string]container.InspectResponse{opID: running(opID[:12])})
	_, err := ownContainerOnDaemon(context.Background(), outerID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not know op's container")

	// A container the daemon knows, but with another hostname, is not op's.
	fakeDaemon(t, "op-host", map[string]container.InspectResponse{outerID: running("someone-else")})
	_, err = ownContainerOnDaemon(context.Background(), outerID)
	require.Error(t, err)
}

package util

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindFreePort(t *testing.T) {
	port, err := FindFreePort(8080, 100)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, port, 8080)
	assert.Less(t, port, 8180)

	// Returned port must be immediately bindable
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln.Close()
}

func TestFindFreePort_ExhaustedRange(t *testing.T) {
	// maxTries = 0 should fail immediately
	_, err := FindFreePort(8080, 0)
	assert.Error(t, err)
}

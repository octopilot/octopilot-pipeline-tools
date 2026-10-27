package pack

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func fastWatchdog(t *testing.T) {
	t.Helper()
	tick, beat := watchdogTick, heartbeatAfter
	t.Cleanup(func() { watchdogTick, heartbeatAfter = tick, beat })
	watchdogTick, heartbeatAfter = 5*time.Millisecond, 20*time.Millisecond
}

func TestWatchdogExplainsAQuietCacheExportAndStopsAtTheIdleLimit(t *testing.T) {
	fastWatchdog(t)
	a := newActivity()
	_, _ = a.Write([]byte("*** Images (sha256:d013e642):\nAdding cache layer 'octopilot/rust:rust-target'\n"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out syncBuf
	stopped := watch(ctx, a, 200*time.Millisecond, false, &out, cancel)
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog did not stop the build")
	}
	err := stopped()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Adding cache layer 'octopilot/rust:rust-target'")
	assert.Contains(t, err.Error(), "local Docker volume")
	assert.Contains(t, out.String(), "[op] pack: still running")
}

func TestWatchdogWithoutLimitOnlyReports(t *testing.T) {
	fastWatchdog(t)
	a := newActivity()
	_, _ = a.Write([]byte("Saving us-docker.pkg.dev/pw-ctl/integration/pricewhisperer/pricewhisperer-suite:latest...\n"))
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuf
	stopped := watch(ctx, a, 0, true, &out, cancel)
	time.Sleep(100 * time.Millisecond)
	assert.NoError(t, ctx.Err(), "no idle limit: the build is not stopped")
	cancel()
	assert.NoError(t, stopped())
	assert.Contains(t, out.String(), "pushing the image to the registry")
}

func TestActivityKeepsTheLastLine(t *testing.T) {
	a := newActivity()
	_, _ = a.Write([]byte("one\ntw"))
	_, _ = a.Write([]byte("o\n\n"))
	_, line := a.state()
	assert.Equal(t, "two", line)
}

func TestIdleTimeoutDefaults(t *testing.T) {
	t.Setenv("OP_PACK_IDLE_TIMEOUT", "")
	assert.Equal(t, time.Duration(0), idleTimeout(true))
	assert.Equal(t, 30*time.Minute, idleTimeout(false))
	t.Setenv("OP_PACK_IDLE_TIMEOUT", "5m")
	assert.Equal(t, 5*time.Minute, idleTimeout(true))
}

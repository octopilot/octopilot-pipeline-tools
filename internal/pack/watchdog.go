package pack

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// The lifecycle goes quiet for long stretches that are not hangs: pushing a GB-sized layer prints nothing until it is
// done, and after the image is saved it writes the build cache (a Rust target layer of several GB) to a local Docker
// volume, also silently. PriceWhisperer v0.1.155 attempt 1 was cancelled as "hung" ~2 minutes into that cache write,
// with the uplink idle, which is what a local cache write looks like. The watchdog says what the lifecycle is doing
// while it is quiet, and stops a build that stays silent past OP_PACK_IDLE_TIMEOUT with an error naming the step.

var (
	watchdogTick   = 30 * time.Second
	heartbeatAfter = 2 * time.Minute
)

// idleTimeout: an export that stays on this machine (op's local registry, the Docker daemon) has no upload in it, so 30 silent minutes is a stuck build. A
// registry push can be silent for as long as the uplink needs, so it has no limit unless one is set.
func idleTimeout(publish bool) time.Duration {
	if v := strings.TrimSpace(os.Getenv("OP_PACK_IDLE_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		fmt.Printf("::warning::OP_PACK_IDLE_TIMEOUT=%q is not a duration (e.g. 30m); using the default\n", v)
	}
	if publish {
		return 0
	}
	return 30 * time.Minute
}

// activity records when output was last written and its last non-empty line.
type activity struct {
	mu      sync.Mutex
	last    time.Time
	line    string
	partial []byte
}

func newActivity() *activity { return &activity{last: time.Now()} }

func (a *activity) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = time.Now()
	buf := append(a.partial, p...)
	if i := strings.LastIndexByte(string(buf), '\n'); i >= 0 {
		for _, l := range strings.Split(string(buf[:i]), "\n") {
			if s := strings.TrimSpace(l); s != "" {
				a.line = s
			}
		}
		buf = buf[i+1:]
	}
	if len(buf) > 4096 {
		buf = buf[len(buf)-4096:]
	}
	a.partial = append([]byte(nil), buf...)
	return len(p), nil
}

func (a *activity) state() (time.Duration, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Since(a.last), a.line
}

// quietHint says what a lifecycle that has gone quiet after line is most likely doing.
func quietHint(line string, publish bool) string {
	switch {
	case strings.Contains(line, "Adding cache layer"), strings.Contains(line, "Reusing cache layer"):
		return "the lifecycle is writing the build cache to a local Docker volume (nothing is uploaded; a multi-GB Rust target layer takes minutes)"
	case strings.HasPrefix(line, "Saving ") && publish:
		return "the lifecycle is pushing the image to the registry (it prints nothing until the push is done; a GB-sized layer takes tens of minutes on a slow uplink)"
	case strings.HasPrefix(line, "Saving "):
		return "the lifecycle is writing the image locally (nothing is uploaded)"
	case strings.Contains(line, "Compiling") || strings.Contains(line, "Linking"):
		return "the compiler is still working"
	}
	return "no known step"
}

// watch prints a heartbeat while the lifecycle is quiet, and cancels the build (stop) once it has been silent for
// idle (0: never). It returns when ctx is done; the returned func reports the silence that stopped the build, if any.
func watch(ctx context.Context, a *activity, idle time.Duration, publish bool, out io.Writer, stop context.CancelFunc) func() error {
	var mu sync.Mutex
	var stopped error
	go func() {
		t := time.NewTicker(watchdogTick)
		defer t.Stop()
		var lastBeat time.Duration
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			quiet, line := a.state()
			if idle > 0 && quiet >= idle {
				mu.Lock()
				stopped = fmt.Errorf("pack printed nothing for %s after %q (%s); stopped by OP_PACK_IDLE_TIMEOUT=%s", quiet.Round(time.Second), line, quietHint(line, publish), idle)
				mu.Unlock()
				_, _ = fmt.Fprintf(out, "::error::%v\n", stopped)
				stop()
				return
			}
			if quiet < heartbeatAfter {
				lastBeat = 0
				continue
			}
			if quiet-lastBeat >= time.Minute || lastBeat == 0 {
				lastBeat = quiet
				_, _ = fmt.Fprintf(out, "[op] pack: still running, no output for %s; last: %q: %s\n", quiet.Round(time.Second), line, quietHint(line, publish))
			}
		}
	}()
	return func() error {
		mu.Lock()
		defer mu.Unlock()
		return stopped
	}
}

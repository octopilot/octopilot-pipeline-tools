package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// Polling for a pushed image: start at waitInitialInterval and back off to waitMaxInterval. Variables so tests run in
// milliseconds.
var (
	waitInitialInterval = 2 * time.Second
	waitMaxInterval     = 15 * time.Second
)

// errRegistryAuth marks a registry that refused the credentials (HTTP 401/403). Waiting longer cannot fix it.
var errRegistryAuth = errors.New("registry credentials expired or refused")

// registryErrorKind sorts an error from a registry request into what waitForImage does with it.
type registryErrorKind int

const (
	registryNotFound  registryErrorKind = iota // not there (yet): keep polling
	registryTransient                          // 429, 5xx, network: keep polling
	registryAuth                               // 401/403: fail now
	registryFatal                              // any other 4xx: fail now
)

func classifyRegistryError(err error) registryErrorKind {
	var terr *transport.Error
	if !errors.As(err, &terr) {
		return registryTransient // connection reset, DNS, timeouts
	}
	switch code := terr.StatusCode; {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return registryAuth
	case code == http.StatusNotFound:
		return registryNotFound
	case code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500:
		return registryTransient
	case code >= 400:
		return registryFatal
	}
	return registryTransient
}

// waitForImage polls the registry until tag resolves or timeout passes. Not found and transient errors (429, 5xx,
// network) are retried with backoff; the last one is logged and returned with the timeout. A refused credential
// (401/403) fails at once with errRegistryAuth: the registry is reachable and saying no, so waiting would only hide
// the cause behind a timeout (a static access token outliving its 1-hour lifetime looked exactly like a slow
// registry).
func waitForImage(tag string, timeout time.Duration, insecureRegistries []string, opts ...remote.Option) error {
	fmt.Printf("Waiting for image propagation: %s (timeout: %s)\n", tag, timeout)

	ref, err := parseReferenceForRemote(tag, insecureRegistries)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	interval := waitInitialInterval
	var lastErr error
	for attempt := 1; ; attempt++ {
		_, err := remoteHead(ref, opts...)
		if err == nil {
			if attempt > 1 {
				fmt.Printf("Image found: %s (after %d checks)\n", tag, attempt)
			} else {
				fmt.Printf("Image found: %s\n", tag)
			}
			return nil
		}
		switch classifyRegistryError(err) {
		case registryAuth:
			return fmt.Errorf("%w checking %s: %v", errRegistryAuth, tag, err)
		case registryFatal:
			return fmt.Errorf("checking %s: %w", tag, err)
		}
		if lastErr == nil || lastErr.Error() != err.Error() {
			fmt.Printf("  %s not resolvable yet (check %d): %v\n", tag, attempt, err)
		}
		lastErr = err

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timeout waiting for image %s after %s (%d checks; last error: %v)", tag, timeout, attempt, lastErr)
		}
		sleep := interval
		if sleep > remaining {
			sleep = remaining
		}
		time.Sleep(sleep)
		interval = interval * 3 / 2
		if interval > waitMaxInterval {
			interval = waitMaxInterval
		}
	}
}

package util

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// Registry pushes are tried up to PushAttempts times in all. Between attempts op waits PushBackoff, doubling each
// time up to PushMaxBackoff (5s, 10s, 20s). Variables so tests run in milliseconds.
var (
	PushAttempts   = 4
	PushBackoff    = 5 * time.Second
	PushMaxBackoff = 40 * time.Second
)

// PushBackoffFor is the wait before retry n (1 for the first retry).
func PushBackoffFor(retry int) time.Duration {
	wait := PushBackoff
	for i := 1; i < retry; i++ {
		wait *= 2
		if wait >= PushMaxBackoff {
			return PushMaxBackoff
		}
	}
	return wait
}

// TransientPushError reports whether a failed push is worth trying again: the registry or the network dropped it
// (HTTP/2 GOAWAY, connection reset, EOF, timeouts), or the registry answered 429, 408 or 5xx. A refused credential
// (401/403) and any other 4xx are permanent: pushing again cannot fix them.
func TransientPushError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var marked *transientError
	if errors.As(err, &marked) {
		return true
	}
	var terr *transport.Error
	if errors.As(err, &terr) {
		code := terr.StatusCode
		return code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500
	}
	return TransientPushText(err.Error())
}

// MarkTransient marks a failure as transient when the caller knows better than its text, e.g. a tool whose exit
// status says only "failed" while its output names a dropped connection.
func MarkTransient(err error) error {
	if err == nil {
		return nil
	}
	return &transientError{err: err}
}

type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() + " (transient registry error)" }
func (e *transientError) Unwrap() error { return e.err }

// permanentPushMarkers win over transientPushMarkers: "unauthorized ... EOF" is still a refused credential.
var permanentPushMarkers = []string{
	"unauthorized", "denied", "forbidden", "status code 401", "status code 403",
	"manifest_invalid", "name_invalid", "name_unknown", "unsupported",
}

var transientPushMarkers = []string{
	"goaway", "server_shutting_down", "connection reset", "broken pipe", "unexpected eof", ": eof",
	"i/o timeout", "tls handshake timeout", "client connection lost", "timeout awaiting response headers",
	"use of closed network connection", "internal server error", "bad gateway", "service unavailable",
	"gateway timeout", "too many requests", "toomanyrequests",
	"status code 429", "status code 500", "status code 502", "status code 503", "status code 504",
}

// TransientPushText classifies a push failure from its text: an error string, or the output of a tool that pushed
// (the buildpack lifecycle's exporter, docker build --push). Unknown failures are not transient.
func TransientPushText(text string) bool {
	text = strings.ToLower(text)
	for _, m := range permanentPushMarkers {
		if strings.Contains(text, m) {
			return false
		}
	}
	for _, m := range transientPushMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// RetryPush runs push until it succeeds, fails with an error TransientPushError rejects, or PushAttempts attempts
// have been made. what names the push in the log. A push is safe to repeat: the registry skips blobs it already holds,
// so a retry uploads only what the dropped attempt did not finish.
func RetryPush(ctx context.Context, what string, push func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var err error
	for attempt := 1; ; attempt++ {
		if err = push(); err == nil {
			if attempt > 1 {
				fmt.Printf("Pushed %s on attempt %d\n", what, attempt)
			}
			return nil
		}
		if !TransientPushError(err) || attempt >= PushAttempts {
			return err
		}
		wait := PushBackoffFor(attempt)
		fmt.Printf("::warning::push of %s failed (attempt %d of %d): %v; retrying in %s\n", what, attempt, PushAttempts, err, wait)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

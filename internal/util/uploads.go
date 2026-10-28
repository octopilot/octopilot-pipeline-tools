package util

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// UploadCounter counts what op's own registry pushes cost: blob bytes sent, blobs mounted from another repository,
// blobs the registry already had (HEAD 200) and manifests written. The lifecycle's pushes (pack publish) are not seen
// here.
type UploadCounter struct {
	bytes, uploads, mounts, existing, manifests atomic.Int64
}

// Uploads counts every push made through remote options from Transport.
var Uploads = &UploadCounter{}

// UploadStats is a point-in-time copy of an UploadCounter.
type UploadStats struct {
	Bytes, Uploads, Mounts, Existing, Manifests int64
}

func (c *UploadCounter) Snapshot() UploadStats {
	return UploadStats{c.bytes.Load(), c.uploads.Load(), c.mounts.Load(), c.existing.Load(), c.manifests.Load()}
}

// Since describes what was pushed since an earlier snapshot.
func (c *UploadCounter) Since(s UploadStats) string {
	n := c.Snapshot()
	return fmt.Sprintf("uploaded %.1f MiB in %d blob(s); %d blob(s) mounted, %d already in the registry; %d manifest(s)",
		float64(n.Bytes-s.Bytes)/(1<<20), n.Uploads-s.Uploads, n.Mounts-s.Mounts, n.Existing-s.Existing, n.Manifests-s.Manifests)
}

// Transport wraps base (http.DefaultTransport when nil) so pushes through it are counted in Uploads.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &countingTransport{base: base, c: Uploads}
}

type countingTransport struct {
	base http.RoundTripper
	c    *UploadCounter
}

// RoundTrip counts by method and response, not by the upload URL's shape: a registry chooses where a blob upload
// continues (the Location of the POST), and Artifact Registry continues at /artifacts-uploads/..., not at
// /v2/<repo>/blobs/uploads/. Every PATCH, and every PUT or POST that is not a manifest, sends blob bytes.
func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p := req.URL.Path
	manifest := strings.Contains(p, "/manifests/")
	mount := req.Method == http.MethodPost && req.URL.Query().Get("mount") != ""
	blob := !manifest && !mount &&
		(req.Method == http.MethodPatch || req.Method == http.MethodPut || req.Method == http.MethodPost)
	if blob && req.Body != nil && req.Body != http.NoBody {
		req.Body = &countingBody{ReadCloser: req.Body, n: &t.c.bytes}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	switch {
	case req.Method == http.MethodHead && strings.Contains(p, "/blobs/sha256:") && resp.StatusCode == http.StatusOK:
		t.c.existing.Add(1)
	case mount && resp.StatusCode == http.StatusCreated:
		t.c.mounts.Add(1)
	case manifest && req.Method == http.MethodPut && resp.StatusCode < 300:
		t.c.manifests.Add(1)
	case blob && resp.StatusCode == http.StatusCreated && (req.Method == http.MethodPut || req.URL.Query().Get("digest") != ""):
		// The upload's last request: a PUT with ?digest=, or a monolithic POST with ?digest=.
		t.c.uploads.Add(1)
	}
	return resp, nil
}

type countingBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.Add(int64(n))
	return n, err
}

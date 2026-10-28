package util

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeRegistryRT func(*http.Request) int

func (f fakeRegistryRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{StatusCode: f(req), Body: http.NoBody, Request: req}, nil
}

func do(t *testing.T, rt http.RoundTripper, method, url, body string, status *int, want int) {
	t.Helper()
	var r io.Reader = http.NoBody
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	*status = want
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestUploadCounterCountsArtifactRegistryUploads(t *testing.T) {
	c := &UploadCounter{}
	status := 0
	rt := &countingTransport{base: fakeRegistryRT(func(*http.Request) int { return status }), c: c}
	const reg = "https://us-docker.pkg.dev"

	// Artifact Registry: POST starts the upload, the bytes go to /artifacts-uploads/..., and a PUT finishes it.
	do(t, rt, http.MethodPost, reg+"/v2/p/r/suite/blobs/uploads/", "", &status, http.StatusAccepted)
	do(t, rt, http.MethodPatch, reg+"/artifacts-uploads/namespaces/p/repositories/r/uploads/abc", strings.Repeat("x", 3000), &status, http.StatusAccepted)
	do(t, rt, http.MethodPut, reg+"/artifacts-uploads/namespaces/p/repositories/r/uploads/abc?digest=sha256:aa", "", &status, http.StatusCreated)
	// A registry that keeps the upload under /v2/.../blobs/uploads/.
	do(t, rt, http.MethodPatch, "https://ghcr.io/v2/o/i/blobs/uploads/u1", strings.Repeat("y", 1000), &status, http.StatusAccepted)
	do(t, rt, http.MethodPut, "https://ghcr.io/v2/o/i/blobs/uploads/u1?digest=sha256:bb", "", &status, http.StatusCreated)
	// A monolithic upload: POST with the body and ?digest=.
	do(t, rt, http.MethodPost, "https://ghcr.io/v2/o/i/blobs/uploads/?digest=sha256:cc", strings.Repeat("z", 500), &status, http.StatusCreated)
	// A mount, a blob the registry already has, and a manifest: no blob bytes.
	do(t, rt, http.MethodPost, reg+"/v2/p/r/svc/blobs/uploads/?mount=sha256:aa&from=p/r/suite", "", &status, http.StatusCreated)
	do(t, rt, http.MethodHead, reg+"/v2/p/r/svc/blobs/sha256:dd", "", &status, http.StatusOK)
	do(t, rt, http.MethodPut, reg+"/v2/p/r/svc/manifests/latest", strings.Repeat("m", 400), &status, http.StatusCreated)

	got := c.Snapshot()
	want := UploadStats{Bytes: 4500, Uploads: 3, Mounts: 1, Existing: 1, Manifests: 1}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if s := c.Since(UploadStats{}); !strings.HasPrefix(s, "uploaded 0.0 MiB in 3 blob(s); 1 blob(s) mounted, 1 already in the registry; 1 manifest(s)") {
		t.Fatalf("summary %q", s)
	}
}

// Package localreg is a small OCI distribution registry that op runs for the length of one build, so pack can
// publish a suite image without it leaving the machine. Blobs and uploads are kept on disk (a suite's app layer is
// GBs; the go-containerregistry test registry buffers uploads in memory), manifests in memory.
//
// It serves what the lifecycle and go-containerregistry use: the /v2/ ping, blob HEAD/GET, uploads (POST, PATCH,
// PUT, monolithic POST, cross-repository mount of a blob it has), manifest HEAD/GET/PUT by tag or digest.
package localreg

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Registry is a running local registry. Addr is host:port as other containers on the Docker network reach it.
type Registry struct {
	Addr string

	dir  string
	srv  *http.Server
	ln   net.Listener
	mu   sync.Mutex
	ups  map[string]*upload
	mans map[string]manifest // "<repo>@<digest>" and "<repo>:<tag>"
}

type upload struct {
	f *os.File
	h hash.Hash
	n int64
}

type manifest struct {
	body        []byte
	contentType string
	digest      string
}

// Start serves a registry from dir at a free port on listen ("" = every interface), advertised to clients as host.
func Start(dir, listen, host string) (*Registry, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "uploads"), 0o755); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(listen, "0"))
	if err != nil {
		return nil, err
	}
	r := &Registry{dir: dir, ln: ln, ups: map[string]*upload{}, mans: map[string]manifest{}}
	r.Addr = net.JoinHostPort(host, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	r.srv = &http.Server{Handler: r, ReadHeaderTimeout: time.Minute}
	go func() { _ = r.srv.Serve(ln) }()
	return r, nil
}

// Close stops the registry. The caller removes dir.
func (r *Registry) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.srv.Shutdown(ctx)
}

// Placement is where the local registry listens and how the lifecycle container reaches it.
type Placement struct {
	Listen  string // address to listen on
	Host    string // host part of the registry address the lifecycle is given
	Network string // network for the lifecycle container ("" = pack's default)
}

// Place works out how the lifecycle container reaches a registry in this process. pack runs the lifecycle on a
// network of its own, which cannot reach a sibling container's bridge address, and a host firewall may drop traffic
// from container networks to the host. So the lifecycle shares op's network namespace instead and the registry
// listens on loopback:
//   - op in a container (the CI runners: octopilot/actions starts the op image on the build's Docker daemon): the
//     lifecycle joins op's container network, container:<op container id>;
//   - op on a Linux host: the lifecycle uses the host network;
//   - Docker Desktop (macOS, Windows): the lifecycle reaches the host as host.docker.internal.
//
// packNetwork (OP_PACK_NETWORK) set by the caller is kept: "host" uses loopback, anything else the first non-loopback
// address. OP_LOCAL_REGISTRY_HOST overrides the address the lifecycle is given (the registry then listens on all
// interfaces).
func Place(packNetwork string) (Placement, error) {
	if h := strings.TrimSpace(os.Getenv("OP_LOCAL_REGISTRY_HOST")); h != "" {
		return Placement{Listen: "", Host: h, Network: packNetwork}, nil
	}
	switch {
	case packNetwork == "host":
		return Placement{Listen: "127.0.0.1", Host: "127.0.0.1", Network: "host"}, nil
	case packNetwork != "":
		ip, err := firstIPv4()
		return Placement{Listen: "", Host: ip, Network: packNetwork}, err
	case runtime.GOOS == "darwin" || runtime.GOOS == "windows":
		return Placement{Listen: "", Host: "host.docker.internal"}, nil
	}
	if id := ownContainerID(); id != "" {
		return Placement{Listen: "127.0.0.1", Host: "127.0.0.1", Network: "container:" + id}, nil
	}
	return Placement{Listen: "127.0.0.1", Host: "127.0.0.1", Network: "host"}, nil
}

func firstIPv4() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
				return n.IP.String(), nil
			}
		}
	}
	return "", errors.New("no IPv4 address to offer the lifecycle container (set OP_LOCAL_REGISTRY_HOST)")
}

// mountInfo is read by ownContainerID; a variable so tests can supply one.
var mountInfo = func() ([]byte, error) { return os.ReadFile("/proc/self/mountinfo") }

var containerIDInMount = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// ownContainerID is the id of the Docker container this process runs in ("" outside one): Docker bind-mounts the
// container's hostname, hosts and resolv.conf from /var/lib/docker/containers/<id>/.
func ownContainerID() string {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return ""
	}
	b, err := mountInfo()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, " /etc/hostname ") && !strings.Contains(line, " /etc/resolv.conf ") {
			continue
		}
		if m := containerIDInMount.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

type regErr struct {
	status  int
	code    string
	message string
}

func (r *Registry) fail(w http.ResponseWriter, e regErr) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": e.code, "message": e.message}}})
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p := req.URL.Path
	if p == "/v2" || p == "/v2/" {
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
		return
	}
	if !strings.HasPrefix(p, "/v2/") {
		r.fail(w, regErr{http.StatusNotFound, "UNSUPPORTED", "not a /v2/ path"})
		return
	}
	rest := strings.TrimPrefix(p, "/v2/")
	switch {
	case strings.Contains(rest, "/blobs/uploads"):
		i := strings.Index(rest, "/blobs/uploads")
		r.uploads(w, req, rest[:i], strings.TrimPrefix(strings.TrimPrefix(rest[i:], "/blobs/uploads"), "/"))
	case strings.Contains(rest, "/blobs/"):
		i := strings.LastIndex(rest, "/blobs/")
		r.blob(w, req, rest[i+len("/blobs/"):])
	case strings.Contains(rest, "/manifests/"):
		i := strings.LastIndex(rest, "/manifests/")
		r.manifest(w, req, rest[:i], rest[i+len("/manifests/"):])
	default:
		r.fail(w, regErr{http.StatusNotFound, "UNSUPPORTED", "unsupported path"})
	}
}

func validDigest(d string) bool {
	h, ok := strings.CutPrefix(d, "sha256:")
	if !ok || len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil
}

func (r *Registry) blobPath(d string) string {
	return filepath.Join(r.dir, "blobs", strings.TrimPrefix(d, "sha256:"))
}

func (r *Registry) blob(w http.ResponseWriter, req *http.Request, d string) {
	if !validDigest(d) {
		r.fail(w, regErr{http.StatusBadRequest, "DIGEST_INVALID", "bad digest"})
		return
	}
	switch req.Method {
	case http.MethodHead, http.MethodGet:
		f, err := os.Open(r.blobPath(d))
		if err != nil {
			r.fail(w, regErr{http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown"})
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			r.fail(w, regErr{http.StatusInternalServerError, "UNKNOWN", err.Error()})
			return
		}
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		w.Header().Set("Docker-Content-Digest", d)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if req.Method == http.MethodGet {
			_, _ = io.Copy(w, f)
		}
	default:
		r.fail(w, regErr{http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed"})
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (r *Registry) uploads(w http.ResponseWriter, req *http.Request, repo, id string) {
	q := req.URL.Query()
	switch {
	case req.Method == http.MethodPost && id == "":
		if from, d := q.Get("from"), q.Get("mount"); from != "" && d != "" && validDigest(d) {
			if _, err := os.Stat(r.blobPath(d)); err == nil {
				w.Header().Set("Location", "/v2/"+repo+"/blobs/"+d)
				w.Header().Set("Docker-Content-Digest", d)
				w.WriteHeader(http.StatusCreated)
				return
			}
		}
		id := newID()
		f, err := os.Create(filepath.Join(r.dir, "uploads", id))
		if err != nil {
			r.fail(w, regErr{http.StatusInternalServerError, "UNKNOWN", err.Error()})
			return
		}
		u := &upload{f: f, h: sha256.New()}
		if d := q.Get("digest"); d != "" { // monolithic upload
			if err := r.appendBody(u, req.Body); err != nil {
				u.f.Close()
				r.fail(w, regErr{http.StatusInternalServerError, "UNKNOWN", err.Error()})
				return
			}
			r.commit(w, repo, u, d)
			return
		}
		r.mu.Lock()
		r.ups[id] = u
		r.mu.Unlock()
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
		w.Header().Set("Range", "0-0")
		w.Header().Set("Docker-Upload-UUID", id)
		w.WriteHeader(http.StatusAccepted)
	case (req.Method == http.MethodPatch || req.Method == http.MethodPut) && id != "":
		r.mu.Lock()
		u, ok := r.ups[id]
		r.mu.Unlock()
		if !ok {
			r.fail(w, regErr{http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown"})
			return
		}
		if err := r.appendBody(u, req.Body); err != nil {
			r.fail(w, regErr{http.StatusInternalServerError, "UNKNOWN", err.Error()})
			return
		}
		if req.Method == http.MethodPatch {
			w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/"+id)
			w.Header().Set("Range", fmt.Sprintf("0-%d", max(u.n-1, 0)))
			w.Header().Set("Docker-Upload-UUID", id)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		r.mu.Lock()
		delete(r.ups, id)
		r.mu.Unlock()
		r.commit(w, repo, u, q.Get("digest"))
	default:
		r.fail(w, regErr{http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed"})
	}
}

func (r *Registry) appendBody(u *upload, body io.Reader) error {
	if body == nil {
		return nil
	}
	n, err := io.Copy(io.MultiWriter(u.f, u.h), body)
	u.n += n
	return err
}

// commit checks the upload against digest and moves it into the blob store.
func (r *Registry) commit(w http.ResponseWriter, repo string, u *upload, digest string) {
	name := u.f.Name()
	if err := u.f.Close(); err != nil {
		os.Remove(name)
		r.fail(w, regErr{http.StatusInternalServerError, "UNKNOWN", err.Error()})
		return
	}
	got := "sha256:" + hex.EncodeToString(u.h.Sum(nil))
	if !validDigest(digest) || got != digest {
		os.Remove(name)
		r.fail(w, regErr{http.StatusBadRequest, "DIGEST_INVALID", fmt.Sprintf("digest %s does not match the upload (%s)", digest, got)})
		return
	}
	if err := os.Rename(name, r.blobPath(digest)); err != nil {
		os.Remove(name)
		r.fail(w, regErr{http.StatusInternalServerError, "UNKNOWN", err.Error()})
		return
	}
	w.Header().Set("Location", "/v2/"+repo+"/blobs/"+digest)
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusCreated)
}

func (r *Registry) manifest(w http.ResponseWriter, req *http.Request, repo, ref string) {
	key := repo + ":" + ref
	if strings.HasPrefix(ref, "sha256:") {
		key = repo + "@" + ref
	}
	switch req.Method {
	case http.MethodHead, http.MethodGet:
		r.mu.Lock()
		m, ok := r.mans[key]
		r.mu.Unlock()
		if !ok {
			r.fail(w, regErr{http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown"})
			return
		}
		w.Header().Set("Content-Type", m.contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(m.body)))
		w.Header().Set("Docker-Content-Digest", m.digest)
		w.WriteHeader(http.StatusOK)
		if req.Method == http.MethodGet {
			_, _ = w.Write(m.body)
		}
	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(req.Body, 16<<20))
		if err != nil {
			r.fail(w, regErr{http.StatusBadRequest, "MANIFEST_INVALID", err.Error()})
			return
		}
		sum := sha256.Sum256(body)
		d := "sha256:" + hex.EncodeToString(sum[:])
		if strings.HasPrefix(ref, "sha256:") && ref != d {
			r.fail(w, regErr{http.StatusBadRequest, "DIGEST_INVALID", "manifest digest mismatch"})
			return
		}
		m := manifest{body: body, contentType: req.Header.Get("Content-Type"), digest: d}
		r.mu.Lock()
		r.mans[repo+"@"+d] = m
		if !strings.HasPrefix(ref, "sha256:") {
			r.mans[key] = m
		}
		r.mu.Unlock()
		w.Header().Set("Docker-Content-Digest", d)
		w.Header().Set("Location", "/v2/"+repo+"/manifests/"+d)
		w.WriteHeader(http.StatusCreated)
	default:
		r.fail(w, regErr{http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed"})
	}
}

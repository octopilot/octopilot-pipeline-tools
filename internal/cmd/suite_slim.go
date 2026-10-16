package cmd

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// A BRRTRouter suite builds every binary of the workspace in one pack build, so the suite image's app layer holds all
// of them under /workspace/bin (PriceWhisperer: 28 binaries, 243 MiB). Retagging that image per binary gave every
// service the whole suite. Each image published by the fan-out instead gets an app layer with only its own binary;
// everything else in the layer (each service's gen/doc, static_site and impl/config, a few KiB) stays, so a process's
// paths keep working. The run image, launcher and process-type layers are the suite's, shared by every service.

const lifecycleMetadataLabel = "io.buildpacks.lifecycle.metadata"

// cnbBinDir is where the Rust buildpack puts the binaries, relative to the layer root.
const cnbBinDir = "workspace/bin/"

// appLayerDiffIDs reads the app layers' diff ids from the lifecycle metadata label (`app: [{sha}]`).
func appLayerDiffIDs(cf *v1.ConfigFile) ([]v1.Hash, error) {
	raw, ok := cf.Config.Labels[lifecycleMetadataLabel]
	if !ok {
		return nil, fmt.Errorf("no %s label: not a buildpacks image", lifecycleMetadataLabel)
	}
	var md struct {
		App []struct {
			SHA string `json:"sha"`
		} `json:"app"`
	}
	if err := json.Unmarshal([]byte(raw), &md); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", lifecycleMetadataLabel, err)
	}
	var out []v1.Hash
	for _, a := range md.App {
		h, err := v1.NewHash(a.SHA)
		if err != nil {
			return nil, fmt.Errorf("app layer sha %q: %w", a.SHA, err)
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no app layer", lifecycleMetadataLabel)
	}
	return out, nil
}

// isOtherBinary reports whether a layer entry is a binary in /workspace/bin other than keep.
func isOtherBinary(name, keep string) bool {
	p := strings.TrimPrefix(path.Clean("/"+name), "/")
	if !strings.HasPrefix(p, cnbBinDir) {
		return false
	}
	rest := strings.TrimPrefix(p, cnbBinDir)
	return rest != "" && rest != keep && !strings.HasPrefix(rest, keep+"/")
}

// slimLayerTar copies the uncompressed layer tar src to dst without the binaries other than keep. A hard link to a
// dropped file is dropped with it. It returns the bytes of file content dropped, and fails if keep is not in the layer,
// which would publish an image that cannot start.
func slimLayerTar(src io.Reader, dst io.Writer, keep string) (int64, error) {
	tr := tar.NewReader(src)
	tw := tar.NewWriter(dst)
	dropped := map[string]bool{}
	var droppedBytes int64
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("reading app layer: %w", err)
		}
		clean := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
		if isOtherBinary(hdr.Name, keep) || (hdr.Typeflag == tar.TypeLink && dropped[strings.TrimPrefix(path.Clean("/"+hdr.Linkname), "/")]) {
			dropped[clean] = true
			droppedBytes += hdr.Size
			continue
		}
		if clean == cnbBinDir+keep {
			found = true
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return 0, err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return 0, err
		}
	}
	if !found {
		return 0, fmt.Errorf("the suite's app layer has no /%s%s", cnbBinDir, keep)
	}
	return droppedBytes, tw.Close()
}

// slimSuite is the suite image's app layer unpacked once to disk, so each binary's layer is written from a local file
// instead of pulling 200+ MiB from the registry per service.
type slimSuite struct {
	base      v1.Image
	appDiffID v1.Hash
	appTar    string // uncompressed
	dir       string
}

// newSlimSuite prepares the suite image for slimming. It returns nil (and no error) for an image with more than one
// app layer, which the fan-out then retags whole as before.
func newSlimSuite(base v1.Image) (*slimSuite, error) {
	cf, err := base.ConfigFile()
	if err != nil {
		return nil, err
	}
	ids, err := appLayerDiffIDs(cf)
	if err != nil {
		return nil, err
	}
	if len(ids) != 1 {
		fmt.Printf("Suite fan-out: %d app layers; publishing the suite image per binary unslimmed\n", len(ids))
		return nil, nil
	}
	layer, err := base.LayerByDiffID(ids[0])
	if err != nil {
		return nil, fmt.Errorf("suite app layer %s: %w", ids[0], err)
	}
	dir, err := os.MkdirTemp("", "op-suite-")
	if err != nil {
		return nil, err
	}
	s := &slimSuite{base: base, appDiffID: ids[0], appTar: path.Join(dir, "app.tar"), dir: dir}
	rc, err := layer.Uncompressed()
	if err != nil {
		s.Close()
		return nil, err
	}
	defer rc.Close()
	f, err := os.Create(s.appTar)
	if err != nil {
		s.Close()
		return nil, err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		s.Close()
		return nil, fmt.Errorf("reading suite app layer: %w", err)
	}
	if err := f.Close(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *slimSuite) Close() { _ = os.RemoveAll(s.dir) }

// image is the suite image with an app layer holding only bin, and bin's process as the entrypoint.
func (s *slimSuite) image(bin string) (v1.Image, int64, error) {
	src, err := os.Open(s.appTar)
	if err != nil {
		return nil, 0, err
	}
	defer src.Close()
	slimPath := path.Join(s.dir, bin+".tar")
	dst, err := os.Create(slimPath)
	if err != nil {
		return nil, 0, err
	}
	dropped, err := slimLayerTar(src, dst, bin)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, 0, fmt.Errorf("slimming the app layer for %s: %w", bin, err)
	}
	orig, err := s.base.LayerByDiffID(s.appDiffID)
	if err != nil {
		return nil, 0, err
	}
	mt, err := orig.MediaType()
	if err != nil {
		return nil, 0, err
	}
	slim, err := tarball.LayerFromFile(slimPath, tarball.WithMediaType(mt))
	if err != nil {
		return nil, 0, err
	}
	img, err := replaceLayer(s.base, s.appDiffID, slim)
	if err != nil {
		return nil, 0, err
	}
	img, err = imageWithEntrypoint(img, bin)
	return img, dropped, err
}

// replaceLayer returns base with the layer whose diff id is old replaced by layer, keeping every other layer (so the
// registry mounts them rather than uploading again), the config, the history and the manifest media type, and pointing
// the lifecycle metadata's app sha at the new layer.
func replaceLayer(base v1.Image, old v1.Hash, layer v1.Layer) (v1.Image, error) {
	layers, err := base.Layers()
	if err != nil {
		return nil, err
	}
	cf, err := base.ConfigFile()
	if err != nil {
		return nil, err
	}
	mt, err := base.MediaType()
	if err != nil {
		return nil, err
	}
	newID, err := layer.DiffID()
	if err != nil {
		return nil, err
	}
	start := cf.DeepCopy()
	history := start.History
	start.History = nil
	start.RootFS.DiffIDs = nil
	img, err := mutate.ConfigFile(empty.Image, start)
	if err != nil {
		return nil, err
	}
	img = mutate.MediaType(img, mt)
	adds := make([]mutate.Addendum, 0, len(layers))
	replaced := false
	for _, l := range layers {
		id, err := l.DiffID()
		if err != nil {
			return nil, err
		}
		if id == old {
			l = layer
			replaced = true
		}
		lmt, err := l.MediaType()
		if err != nil {
			return nil, err
		}
		adds = append(adds, mutate.Addendum{Layer: l, MediaType: lmt})
	}
	if !replaced {
		return nil, fmt.Errorf("layer %s is not in the image", old)
	}
	img, err = mutate.Append(img, adds...)
	if err != nil {
		return nil, err
	}
	out, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	out = out.DeepCopy()
	out.History = history // same layers in the same order, so the original history still lines up
	if raw, ok := out.Config.Labels[lifecycleMetadataLabel]; ok {
		out.Config.Labels[lifecycleMetadataLabel] = strings.ReplaceAll(raw, old.String(), newID.String())
	}
	return mutate.ConfigFile(img, out)
}

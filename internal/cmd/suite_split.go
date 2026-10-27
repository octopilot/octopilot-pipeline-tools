package cmd

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// A split suite image carries its app layer as one layer per binary plus one layer with everything else (gen/doc,
// static_site, impl/config: a few KiB). The suite image holds every layer; each image the fan-out publishes holds the
// run image, the launcher layers, the shared rest and its own binary's layer.
//
// What that buys on a slow uplink:
//   - a binary that did not change makes the same layer (same bytes in, the same tar headers and the same gzip out),
//     which the registry already has: the push HEADs it and sends nothing;
//   - the fan-out uploads no layer at all: every layer of a service image is in the suite repository already and is
//     mounted from it, so a service image costs a manifest and a config.
//
// Before, the suite's single 1.6 GB app layer (PriceWhisperer, both variants) was uploaded whole on every build, and
// then every binary went up a second time inside its own slimmed service layer.

// suiteLayersLabel names the layers of a split suite image: {"rest": diffID, "bins": {bin: diffID}}.
const suiteLayersLabel = "io.octopilot.suite.layers"

type suiteLayerMap struct {
	Rest string            `json:"rest"`
	Bins map[string]string `json:"bins"`
}

// errSplitUnsupported: the app layer cannot be split safely (e.g. a hard link from one binary to another); the caller
// publishes the suite the old way.
var errSplitUnsupported = errors.New("suite app layer cannot be split per binary")

// maxSuiteLayers keeps a split image under the 127 layers overlayfs and containerd accept, with room to spare.
const maxSuiteLayers = 120

// binGroup is the binary an app-layer entry belongs to ("" for the rest): the first path element under
// /workspace/bin, so a binary's own directory (if any) travels with it.
func binGroup(name string) string {
	p := strings.TrimPrefix(path.Clean("/"+name), "/")
	if !strings.HasPrefix(p, cnbBinDir) {
		return ""
	}
	rest := strings.TrimPrefix(p, cnbBinDir)
	if rest == "" {
		return ""
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// splitAppTar copies the uncompressed app layer tar src into dir: rest.tar with every entry outside /workspace/bin
// (and the bin directory itself) and bin-<name>.tar per binary. Headers are copied as they are, so the same input
// gives byte-identical output. It returns the rest tar's path and each binary's tar path.
func splitAppTar(src io.Reader, dir string) (string, map[string]string, error) {
	type out struct {
		f  *os.File
		tw *tar.Writer
	}
	outs := map[string]*out{}
	group := map[string]string{} // entry -> group, for hard links
	open := func(g string) (*out, error) {
		if o, ok := outs[g]; ok {
			return o, nil
		}
		name := "rest.tar"
		if g != "" {
			name = "bin-" + g + ".tar"
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		o := &out{f: f, tw: tar.NewWriter(f)}
		outs[g] = o
		return o, nil
	}
	closeAll := func() error {
		var first error
		for _, o := range outs {
			if err := o.tw.Close(); err != nil && first == nil {
				first = err
			}
			if err := o.f.Close(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	// The rest layer always exists, even if empty, so every split image has the same shape.
	if _, err := open(""); err != nil {
		return "", nil, err
	}
	tr := tar.NewReader(src)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = closeAll()
			return "", nil, fmt.Errorf("reading app layer: %w", err)
		}
		clean := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
		g := binGroup(hdr.Name)
		if hdr.Typeflag == tar.TypeLink {
			target := strings.TrimPrefix(path.Clean("/"+hdr.Linkname), "/")
			if tg, ok := group[target]; !ok || tg != g {
				_ = closeAll()
				return "", nil, fmt.Errorf("%w: hard link %s -> %s crosses layers", errSplitUnsupported, clean, target)
			}
		}
		group[clean] = g
		o, err := open(g)
		if err != nil {
			_ = closeAll()
			return "", nil, err
		}
		if err := o.tw.WriteHeader(hdr); err != nil {
			_ = closeAll()
			return "", nil, err
		}
		if _, err := io.Copy(o.tw, tr); err != nil {
			_ = closeAll()
			return "", nil, err
		}
	}
	if err := closeAll(); err != nil {
		return "", nil, err
	}
	bins := map[string]string{}
	for g, o := range outs {
		if g != "" {
			bins[g] = o.f.Name()
		}
	}
	if len(bins) == 0 {
		return "", nil, fmt.Errorf("%w: no binaries under /%s", errSplitUnsupported, cnbBinDir)
	}
	return outs[""].f.Name(), bins, nil
}

// splitSuiteImage returns base with its one app layer replaced by the rest layer and one layer per binary (in name
// order), the lifecycle metadata pointing at those layers, and suiteLayersLabel naming them. dir holds the layer tars
// and must outlive the returned image. It returns errSplitUnsupported (wrapped) when the image cannot be split.
func splitSuiteImage(base v1.Image, dir string) (v1.Image, error) {
	cf, err := base.ConfigFile()
	if err != nil {
		return nil, err
	}
	ids, err := appLayerDiffIDs(cf)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errSplitUnsupported, err)
	}
	if len(ids) != 1 {
		return nil, fmt.Errorf("%w: %d app layers", errSplitUnsupported, len(ids))
	}
	app, err := base.LayerByDiffID(ids[0])
	if err != nil {
		return nil, fmt.Errorf("suite app layer %s: %w", ids[0], err)
	}
	mt, err := app.MediaType()
	if err != nil {
		return nil, err
	}
	rc, err := app.Uncompressed()
	if err != nil {
		return nil, err
	}
	restPath, binPaths, err := splitAppTar(rc, dir)
	_ = rc.Close()
	if err != nil {
		return nil, err
	}
	if n := len(cf.RootFS.DiffIDs) + len(binPaths); n > maxSuiteLayers {
		return nil, fmt.Errorf("%w: %d binaries would make %d layers (max %d)", errSplitUnsupported, len(binPaths), n, maxSuiteLayers)
	}
	names := make([]string, 0, len(binPaths))
	for b := range binPaths {
		names = append(names, b)
	}
	sort.Strings(names)

	rest, err := tarball.LayerFromFile(restPath, tarball.WithMediaType(mt))
	if err != nil {
		return nil, err
	}
	m := suiteLayerMap{Bins: map[string]string{}}
	restID, err := rest.DiffID()
	if err != nil {
		return nil, err
	}
	m.Rest = restID.String()
	split := []v1.Layer{rest}
	appIDs := []string{restID.String()}
	for _, b := range names {
		l, err := tarball.LayerFromFile(binPaths[b], tarball.WithMediaType(mt))
		if err != nil {
			return nil, err
		}
		id, err := l.DiffID()
		if err != nil {
			return nil, err
		}
		m.Bins[b] = id.String()
		split = append(split, l)
		appIDs = append(appIDs, id.String())
	}
	layers, err := base.Layers()
	if err != nil {
		return nil, err
	}
	var out []v1.Layer
	for _, l := range layers {
		id, err := l.DiffID()
		if err != nil {
			return nil, err
		}
		if id == ids[0] {
			out = append(out, split...)
		} else {
			out = append(out, l)
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return rebuildImage(base, out, func(labels map[string]string) error {
		labels[suiteLayersLabel] = string(raw)
		return setAppLayers(labels, appIDs)
	})
}

// readSuiteLayerMap returns the split layout of a suite image, or nil for an image that was not split.
func readSuiteLayerMap(cf *v1.ConfigFile) (*suiteLayerMap, error) {
	raw, ok := cf.Config.Labels[suiteLayersLabel]
	if !ok {
		return nil, nil
	}
	var m suiteLayerMap
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", suiteLayersLabel, err)
	}
	if m.Rest == "" || len(m.Bins) == 0 {
		return nil, fmt.Errorf("%s names no layers", suiteLayersLabel)
	}
	return &m, nil
}

// serviceFromSplit is one service's image from a split suite: every layer except the other binaries, the lifecycle
// metadata pointing at the rest and this binary's layer, and bin's process as the entrypoint. No layer is created, so
// built from the suite as pulled from the registry, the push mounts every layer and uploads none.
func serviceFromSplit(suite v1.Image, m *suiteLayerMap, bin string) (v1.Image, error) {
	own, ok := m.Bins[bin]
	if !ok {
		return nil, fmt.Errorf("the suite has no layer for /%s%s", cnbBinDir, bin)
	}
	drop := map[string]bool{}
	for b, id := range m.Bins {
		if b != bin {
			drop[id] = true
		}
	}
	layers, err := suite.Layers()
	if err != nil {
		return nil, err
	}
	var keep []v1.Layer
	for _, l := range layers {
		id, err := l.DiffID()
		if err != nil {
			return nil, err
		}
		if !drop[id.String()] {
			keep = append(keep, l)
		}
	}
	img, err := rebuildImage(suite, keep, func(labels map[string]string) error {
		delete(labels, suiteLayersLabel)
		return setAppLayers(labels, []string{m.Rest, own})
	})
	if err != nil {
		return nil, err
	}
	return imageWithEntrypoint(img, bin)
}

// setAppLayers points the lifecycle metadata's app layers at ids, keeping every other field.
func setAppLayers(labels map[string]string, ids []string) error {
	raw, ok := labels[lifecycleMetadataLabel]
	if !ok {
		return nil
	}
	var md map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &md); err != nil {
		return fmt.Errorf("parsing %s: %w", lifecycleMetadataLabel, err)
	}
	app := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		app = append(app, map[string]string{"sha": id})
	}
	b, err := json.Marshal(app)
	if err != nil {
		return err
	}
	md["app"] = b
	out, err := json.Marshal(md)
	if err != nil {
		return err
	}
	labels[lifecycleMetadataLabel] = string(out)
	return nil
}

// rebuildImage is base with exactly layers (in order), base's config (labels edited by editLabels) and manifest media
// type. The history keeps one entry per layer when base's history lines up with its layers: a layer carried over keeps
// its entry and a new layer copies the entry of the layer it replaced, else the one before it.
func rebuildImage(base v1.Image, layers []v1.Layer, editLabels func(map[string]string) error) (v1.Image, error) {
	cf, err := base.ConfigFile()
	if err != nil {
		return nil, err
	}
	mt, err := base.MediaType()
	if err != nil {
		return nil, err
	}
	// History of base by layer diff id, when it lines up (empty-layer entries carry no layer).
	byID := map[v1.Hash]v1.History{}
	var nonEmpty []v1.History
	for _, h := range cf.History {
		if !h.EmptyLayer {
			nonEmpty = append(nonEmpty, h)
		}
	}
	lined := len(nonEmpty) == len(cf.RootFS.DiffIDs)
	if lined {
		for i, id := range cf.RootFS.DiffIDs {
			byID[id] = nonEmpty[i]
		}
	}

	start := cf.DeepCopy()
	start.History = nil
	start.RootFS.DiffIDs = nil
	img, err := mutate.ConfigFile(empty.Image, start)
	if err != nil {
		return nil, err
	}
	img = mutate.MediaType(img, mt)
	adds := make([]mutate.Addendum, 0, len(layers))
	var history []v1.History
	var last v1.History
	for _, l := range layers {
		lmt, err := l.MediaType()
		if err != nil {
			return nil, err
		}
		id, err := l.DiffID()
		if err != nil {
			return nil, err
		}
		if h, ok := byID[id]; ok {
			last = h
		}
		history = append(history, last)
		adds = append(adds, mutate.Addendum{Layer: l, MediaType: lmt})
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
	if lined {
		out.History = history
	} else {
		out.History = nil
	}
	if out.Config.Labels == nil {
		out.Config.Labels = map[string]string{}
	}
	if editLabels != nil {
		if err := editLabels(out.Config.Labels); err != nil {
			return nil, err
		}
	}
	return mutate.ConfigFile(img, out)
}

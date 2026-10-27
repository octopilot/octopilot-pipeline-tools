package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/octopilot/octopilot-pipeline-tools/internal/localreg"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
)

// Suite builds (BP_RUST_BRRTROUTER) publish to a registry op runs on this machine for the build (internal/localreg),
// not to the target registry, and op pushes the suite split per binary (suite_split.go). The lifecycle's own push
// sent the whole app layer every build: 1.6 GB for PriceWhisperer, and then the fan-out sent every binary again.
// OP_SUITE_SPLIT=false keeps the old path (the lifecycle publishes to the target registry).
//
// A local registry rather than the Docker daemon: exporting to a daemon with the containerd image store is slow and
// can fail on digests (pack#2272), and the runners' daemons use it.

// suiteSplitEnabled reports whether a suite build is published locally and pushed split. On unless OP_SUITE_SPLIT is
// false, and only for one platform: a multi-platform suite is published by the lifecycle as before.
func suiteSplitEnabled(env map[string]string, platforms int) bool {
	if env["BP_RUST_BRRTROUTER"] != "1" || platforms > 1 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OP_SUITE_SPLIT"))) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

// localSuite is the registry a suite is published to before op pushes it on, and the network the lifecycle needs
// to reach it.
type localSuite struct {
	reg     *localreg.Registry
	dir     string
	network string
}

func startLocalSuite() (*localSuite, error) {
	place, err := localreg.Place(os.Getenv("OP_PACK_NETWORK"))
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(os.Getenv("OP_TMPDIR"), "op-suite-registry-")
	if err != nil {
		return nil, err
	}
	reg, err := localreg.Start(dir, place.Listen, place.Host)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	network := place.Network
	if network == "" {
		network = "pack's own"
	}
	fmt.Printf("Suite: the lifecycle publishes to op's local registry %s (lifecycle network: %s); op pushes the suite on, split per binary. OP_SUITE_SPLIT=false publishes from the lifecycle instead\n", reg.Addr, network)
	return &localSuite{reg: reg, dir: dir, network: place.Network}, nil
}

func (l *localSuite) Close() {
	_ = l.reg.Close()
	_ = os.RemoveAll(l.dir)
}

// ref is where the suite for dest is published locally: the same repository path and tag on the local registry.
func (l *localSuite) ref(dest string) (string, error) {
	t, err := name.NewTag(dest, name.WeakValidation)
	if err != nil {
		return "", err
	}
	return l.reg.Addr + "/" + t.RepositoryStr() + ":" + t.TagStr(), nil
}

// preferRemoteLayers swaps every layer of img that one of the remote images also has (same diff id) for the remote
// one: the blob the target registry already holds, under the digest it holds it by, and mountable from its
// repository. Images that cannot be read are skipped.
func preferRemoteLayers(img v1.Image, refs []string, insecure []string) (v1.Image, error) {
	byID := map[v1.Hash]v1.Layer{}
	for _, r := range refs {
		if r == "" {
			continue
		}
		ref, err := parseReferenceForRemote(r, insecure)
		if err != nil {
			continue
		}
		rimg, err := remote.Image(ref, remoteOptionsFor(r, insecure)...)
		if err != nil {
			fmt.Printf("Suite: %s not read (%s); its layers are not reused\n", r, shortErr(err))
			continue
		}
		ls, err := rimg.Layers()
		if err != nil {
			continue
		}
		for _, l := range ls {
			if id, err := l.DiffID(); err == nil {
				if _, seen := byID[id]; !seen {
					byID[id] = l
				}
			}
		}
	}
	if len(byID) == 0 {
		return img, nil
	}
	layers, err := img.Layers()
	if err != nil {
		return nil, err
	}
	swapped := 0
	out := make([]v1.Layer, 0, len(layers))
	for _, l := range layers {
		id, err := l.DiffID()
		if err != nil {
			return nil, err
		}
		if r, ok := byID[id]; ok {
			out = append(out, r)
			swapped++
			continue
		}
		out = append(out, l)
	}
	if swapped == 0 {
		return img, nil
	}
	fmt.Printf("Suite: %d of %d layers are already in the registry (unchanged since the last build, or the run image)\n", swapped, len(layers))
	return rebuildImage(img, out, nil)
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

// pushSplitSuite reads the suite the lifecycle published at localRef, splits its app layer per binary and pushes it
// to dest. Layers dest's registry has (unchanged binaries, the run image, the launcher) are not sent. A suite that
// cannot be split is pushed as built.
func pushSplitSuite(ctx context.Context, localRef, dest, runImage string, insecure []string) error {
	dir, err := os.MkdirTemp(os.Getenv("OP_TMPDIR"), "op-suite-split-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	lref, err := name.ParseReference(localRef, name.Insecure)
	if err != nil {
		return err
	}
	img, err := remote.Image(lref, remoteOptionsFor(localRef, []string{lref.Context().RegistryStr()})...)
	if err != nil {
		return fmt.Errorf("reading the suite from the local registry: %w", err)
	}
	began := time.Now()
	split, err := splitSuiteImage(img, dir)
	switch {
	case errors.Is(err, errSplitUnsupported):
		fmt.Printf("::warning::suite pushed unsplit: %v\n", err)
		split = img
	case err != nil:
		return fmt.Errorf("splitting the suite image: %w", err)
	default:
		cf, _ := split.ConfigFile()
		if m, _ := readSuiteLayerMap(cf); m != nil {
			fmt.Printf("Suite: app layer split into %d binary layers and the rest in %s\n", len(m.Bins), time.Since(began).Round(time.Second))
		}
	}
	// The previous suite at dest and the run image: the layers the registry already holds.
	split, err = preferRemoteLayers(split, []string{dest, runImage}, insecure)
	if err != nil {
		return err
	}
	destRef, err := parseReferenceForRemote(dest, insecure)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", dest, err)
	}
	before := util.Uploads.Snapshot()
	fmt.Printf("Suite: pushing %s\n", dest)
	push := func() error { return remoteWrite(destRef, split, remoteOptionsFor(dest, insecure)...) }
	if err := util.RetryPush(ctx, dest, push); err != nil {
		if classifyRegistryError(err) == registryAuth {
			return fmt.Errorf("pushing %s: %w: %v", dest, errRegistryAuth, err)
		}
		return fmt.Errorf("pushing %s: %w", dest, err)
	}
	fmt.Printf("Suite: pushed %s in %s; %s\n", dest, time.Since(began).Round(time.Second), util.Uploads.Since(before))
	return nil
}

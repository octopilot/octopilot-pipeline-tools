package cmd

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
)

// ttlRegistry is the only registry with special naming: it has no namespaces, so an ephemeral image is named
// <run uuid>-<artifact key>. Everything else is <repo>/<basename>:<tag>.
const ttlRegistry = "ttl.sh"

// Registry writes op makes itself; variables so tests can drop connections.
var (
	remoteWrite      = remote.Write
	remoteWriteIndex = remote.WriteIndex
	remoteTag        = remote.Tag
)

// pushTarget is the single rule for where an artifact goes. No registry behaviour is inferred from the image name:
// the caller decides repo, tag and platforms. With repo == ttl.sh, ttlUUID and artifactKey are required.
func pushTarget(imageName, repo, tag, ttlUUID, artifactKey string) (string, error) {
	base := imageName
	if i := strings.LastIndex(base, ":"); i > 0 && !strings.Contains(base[i:], "/") {
		base = base[:i]
	}
	if repo == ttlRegistry {
		if ttlUUID == "" || artifactKey == "" {
			return "", fmt.Errorf("pushing %s to ttl.sh needs --ttl-uuid and --artifact-key (the pipeline's detect step supplies the key)", imageName)
		}
		return fmt.Sprintf("%s/%s-%s:%s", ttlRegistry, ttlUUID, artifactKey, tag), nil
	}
	if repo == "" {
		return fmt.Sprintf("%s:%s", base, tag), nil
	}
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return fmt.Sprintf("%s/%s:%s", strings.TrimSuffix(repo, "/"), base, tag), nil
}

// platformTag is where one platform's image is pushed before the index is assembled. A single-platform build goes
// straight to the final tag; no index is written over it. The staging tag belongs to this run (stagingRunID): two runs
// building the same tag at once (a main push and a release tag, say) must not read each other's platform images.
func platformTag(fullTag, platform string, platformCount int) string {
	if platformCount <= 1 || platform == "" {
		return fullTag
	}
	return fmt.Sprintf("%s-%s-%s", fullTag, strings.ReplaceAll(platform, "/", "-"), stagingRunID())
}

// stagingRunID names this run's staging tags: the GitHub Actions run and attempt, else a random id kept for the
// process.
func stagingRunID() string {
	if id := os.Getenv("GITHUB_RUN_ID"); id != "" {
		return id + "-" + cmp.Or(os.Getenv("GITHUB_RUN_ATTEMPT"), "1")
	}
	return stagingRandomID()
}

var stagingRandomID = sync.OnceValue(func() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%08x", os.Getpid())
	}
	return hex.EncodeToString(b)
})

// imagePlatform is the platform an image's config says it was built for. With want set (the platform it was built
// as, e.g. "linux/arm64") a different platform is an error: a builder without that platform builds the host's, and an
// index that labels an amd64 image arm64 is worse than an index without arm64.
func imagePlatform(img v1.Image, tag, want string) (*v1.Platform, error) {
	cf, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("reading the config of %s: %w", tag, err)
	}
	got := &v1.Platform{OS: cf.OS, Architecture: cf.Architecture, Variant: cf.Variant, OSVersion: cf.OSVersion}
	if want == "" {
		return got, nil
	}
	w, err := v1.ParsePlatform(want)
	if err != nil {
		return nil, fmt.Errorf("platform %q: %w", want, err)
	}
	if w.OS != got.OS || w.Architecture != got.Architecture || (w.Variant != "" && w.Variant != got.Variant) {
		return nil, fmt.Errorf("%s was built as %s but the image is %s; refusing to publish it under %s (is the builder published for %s?)",
			tag, want, got.String(), want, want)
	}
	return got, nil
}

func remoteOptionsFor(tag string, insecureRegistries []string) []remote.Option {
	// Every push through these options is counted (util.Uploads), so op can say what a push cost the uplink.
	var base http.RoundTripper = remote.DefaultTransport
	for _, reg := range insecureRegistries {
		if strings.HasPrefix(tag, reg) {
			base = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
			break
		}
	}
	return []remote.Option{remote.WithAuthFromKeychain(util.Keychain), remote.WithTransport(util.Transport(base))}
}

// publishImage is the one push path. Given the per-platform images already pushed (at platformTag, built as
// platforms[i]), it writes a manifest list at fullTag when there is more than one, each entry carrying the platform
// its image's config states (and refused if that is not the platform it was built as), waits until fullTag resolves
// from the registry, and returns fullTag@digest. A registry that never serves what was just pushed is an error here,
// not two steps later.
func publishImage(fullTag string, platformTags, platforms []string, insecureRegistries []string, propagation time.Duration) (string, error) {
	ropts := remoteOptionsFor(fullTag, insecureRegistries)
	var digest string
	if len(platformTags) > 1 {
		fmt.Printf("Creating manifest list %s from %v\n", fullTag, platformTags)
		var index v1.ImageIndex = empty.Index
		index = mutate.IndexMediaType(index, types.DockerManifestList)
		for i, pTag := range platformTags {
			pRef, err := parseReferenceForRemote(pTag, insecureRegistries)
			if err != nil {
				return "", fmt.Errorf("parsing platform tag %s: %w", pTag, err)
			}
			desc, err := remote.Get(pRef, ropts...)
			if err != nil {
				return "", fmt.Errorf("getting platform image %s: %w", pTag, err)
			}
			img, err := desc.Image()
			if err != nil {
				return "", fmt.Errorf("getting image content for %s: %w", pTag, err)
			}
			want := ""
			if i < len(platforms) {
				want = platforms[i]
			}
			plat, err := imagePlatform(img, pTag, want)
			if err != nil {
				return "", err
			}
			d := desc.Descriptor
			d.Platform = plat
			index = mutate.AppendManifests(index, mutate.IndexAddendum{Add: img, Descriptor: d})
		}
		ref, err := parseReferenceForRemote(fullTag, insecureRegistries)
		if err != nil {
			return "", fmt.Errorf("parsing full tag %s: %w", fullTag, err)
		}
		if err := util.RetryPush(context.Background(), fullTag, func() error { return remoteWriteIndex(ref, index, ropts...) }); err != nil {
			return "", fmt.Errorf("writing manifest list %s: %w", fullTag, err)
		}
		d, err := index.Digest()
		if err != nil {
			return "", fmt.Errorf("computing index digest: %w", err)
		}
		digest = d.String()
		fmt.Printf("Pushed manifest list %s (digest: %s)\n", fullTag, digest)
	} else {
		ref, err := parseReferenceForRemote(fullTag, insecureRegistries)
		if err != nil {
			return "", fmt.Errorf("parsing reference %q: %w", fullTag, err)
		}
		desc, err := remoteHead(ref, ropts...)
		if err != nil {
			return "", fmt.Errorf("getting image digest for %q: %w", fullTag, err)
		}
		digest = desc.Digest.String()
	}
	if err := waitForImage(fullTag, propagation, insecureRegistries, ropts...); err != nil {
		return "", fmt.Errorf("image %s was pushed but is not resolvable from the registry: %w", fullTag, err)
	}
	return fmt.Sprintf("%s@%s", fullTag, digest), nil
}

// tagAlias points an additional tag (e.g. the branch or version from DOCKER_METADATA_OUTPUT_VERSION) at the manifest
// already pushed at fullTag, in the same repository. Works for single images and manifest lists. Not used for ttl.sh,
// which cannot re-tag manifest lists and whose run UUID already identifies the build.
//
// fullTag may carry the digest publishImage returned (tag@sha256:...). The alias then points at exactly that manifest,
// not at whatever the tag resolves to by now: another run may have pushed to the same tag in between.
func tagAlias(fullTag, alias string, insecureRegistries []string) error {
	source, digest, _ := strings.Cut(fullTag, "@")
	ropts := remoteOptionsFor(source, insecureRegistries)
	ref, err := parseReferenceForRemote(source, insecureRegistries)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", source, err)
	}
	var get name.Reference = ref
	if digest != "" {
		get = ref.Context().Digest(digest)
	}
	desc, err := remote.Get(get, ropts...)
	if err != nil {
		return fmt.Errorf("getting %s: %w", fullTag, err)
	}
	aliasRef := ref.Context().Tag(alias)
	if err := util.RetryPush(context.Background(), aliasRef.String(), func() error { return remoteTag(aliasRef, desc, ropts...) }); err != nil {
		return fmt.Errorf("tagging %s as %s: %w", fullTag, aliasRef, err)
	}
	fmt.Printf("Tagged %s as %s\n", fullTag, aliasRef)
	return nil
}

// orderArtifacts sorts artifacts so that every artifact comes after the artifacts it depends on, as declared in
// skaffold.yaml: `requires` (Dependencies) and a buildpack `runImage` naming another artifact. Ties are broken by
// name so the order is deterministic. A cycle is an error.
func orderArtifacts(artifacts []*latest.Artifact) ([]*latest.Artifact, error) {
	byName := make(map[string]*latest.Artifact, len(artifacts))
	for _, a := range artifacts {
		byName[a.ImageName] = a
	}
	deps := func(a *latest.Artifact) []string {
		var out []string
		for _, d := range a.Dependencies {
			if _, ok := byName[d.ImageName]; ok {
				out = append(out, d.ImageName)
			}
		}
		if a.BuildpackArtifact != nil {
			if run := strings.SplitN(a.BuildpackArtifact.RunImage, ":", 2)[0]; run != "" {
				for name := range byName {
					if name == run || strings.SplitN(name, ":", 2)[0] == run {
						out = append(out, name)
					}
				}
			}
		}
		return out
	}
	sorted := make([]*latest.Artifact, 0, len(artifacts))
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(a *latest.Artifact) error
	visit = func(a *latest.Artifact) error {
		switch state[a.ImageName] {
		case 1:
			return fmt.Errorf("artifact dependency cycle through %s", a.ImageName)
		case 2:
			return nil
		}
		state[a.ImageName] = 1
		ds := deps(a)
		sort.Strings(ds)
		for _, d := range ds {
			if err := visit(byName[d]); err != nil {
				return err
			}
		}
		state[a.ImageName] = 2
		sorted = append(sorted, a)
		return nil
	}
	names := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		names = append(names, a.ImageName)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := visit(byName[n]); err != nil {
			return nil, err
		}
	}
	return sorted, nil
}

// loadBuiltImages seeds the image-name -> pushed-reference map from earlier builds: --from-build-result files (the
// pipeline downloads the previous wave's build_result.json) and --run-image-override name=ref pairs. This is how a
// buildpack artifact whose runImage is another artifact finds it when the two are built in separate jobs.
func loadBuiltImages(resultFiles []string, overrides []string) (map[string]string, error) {
	m := map[string]string{}
	for _, f := range resultFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		var res struct {
			Builds []util.Build `json:"builds"`
		}
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", f, err)
		}
		for _, b := range res.Builds {
			m[b.ImageName] = b.Tag
		}
	}
	for _, o := range overrides {
		name, ref, ok := strings.Cut(o, "=")
		if !ok || name == "" || ref == "" {
			return nil, fmt.Errorf("--run-image-override wants name=ref, got %q", o)
		}
		m[name] = ref
	}
	return m, nil
}

package cmd

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
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
// straight to the final tag; no index is written over it.
func platformTag(fullTag, platform string, platformCount int) string {
	if platformCount <= 1 || platform == "" {
		return fullTag
	}
	return fmt.Sprintf("%s-%s", fullTag, strings.ReplaceAll(platform, "/", "-"))
}

func remoteOptionsFor(tag string, insecureRegistries []string) []remote.Option {
	opts := []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain)}
	for _, reg := range insecureRegistries {
		if strings.HasPrefix(tag, reg) {
			opts = append(opts, remote.WithTransport(&http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}))
			break
		}
	}
	return opts
}

// publishImage is the one push path. Given the per-platform images already pushed (at platformTag), it writes a
// manifest list at fullTag when there is more than one, waits until fullTag resolves from the registry, and returns
// fullTag@digest. A registry that never serves what was just pushed is an error here, not two steps later.
func publishImage(fullTag string, platformTags []string, insecureRegistries []string, propagation time.Duration) (string, error) {
	ropts := remoteOptionsFor(fullTag, insecureRegistries)
	var digest string
	if len(platformTags) > 1 {
		fmt.Printf("Creating manifest list %s from %v\n", fullTag, platformTags)
		var index v1.ImageIndex = empty.Index
		index = mutate.IndexMediaType(index, types.DockerManifestList)
		for _, pTag := range platformTags {
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
			index = mutate.AppendManifests(index, mutate.IndexAddendum{Add: img, Descriptor: desc.Descriptor})
		}
		ref, err := parseReferenceForRemote(fullTag, insecureRegistries)
		if err != nil {
			return "", fmt.Errorf("parsing full tag %s: %w", fullTag, err)
		}
		if err := remote.WriteIndex(ref, index, ropts...); err != nil {
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

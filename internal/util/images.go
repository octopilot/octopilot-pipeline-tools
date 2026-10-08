package util

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// ImageRegistryEnv sets --image-registry when the flag is not given, so a CI wrapper can configure it once.
const ImageRegistryEnv = "OP_IMAGE_REGISTRY"

// BuilderMetadataLabel is the label on a buildpacks builder image that names its default run image.
const BuilderMetadataLabel = "io.buildpacks.builder.metadata"

// ImageSource decides where op pulls the images it uses but does not build: buildpack builders and run images.
//
// With no registry configured, every reference is used exactly as written (in skaffold.yaml or in a builder's
// metadata). With a registry, typically a pull-through mirror such as an Artifact Registry virtual repository
// (us-docker.pkg.dev/<project>/<repo>), the reference's registry is replaced and its repository path is kept:
//
//	ghcr.io/octopilot/builder-jammy-base:x  ->  <registry>/octopilot/builder-jammy-base:x
//	paketobuildpacks/run-jammy-base:latest  ->  <registry>/paketobuildpacks/run-jammy-base:latest
//	ubuntu:jammy                            ->  <registry>/library/ubuntu:jammy
//
// Left untouched: images this skaffold config produces (they are not upstream images), local and ephemeral
// registries (localhost, 127.0.0.1, host.docker.internal, ttl.sh), and references already on the registry's host.
type ImageSource struct {
	registry string
	own      map[string]bool
}

// NewImageSource returns an ImageSource for registry ("" disables rewriting). ownImages are the image names of the
// artifacts in the skaffold config; references to them are never rewritten.
func NewImageSource(registry string, ownImages []string) ImageSource {
	s := ImageSource{registry: strings.TrimSuffix(strings.TrimSpace(registry), "/"), own: map[string]bool{}}
	for _, img := range ownImages {
		if r, err := name.ParseReference(strings.TrimSpace(img), name.WeakValidation); err == nil {
			s.own[r.Context().Name()] = true
		}
	}
	return s
}

// Registry is the configured registry, or "" when references are used as written.
func (s ImageSource) Registry() string { return s.registry }

// Resolve returns the reference to pull for ref (see ImageSource).
func (s ImageSource) Resolve(ref string) string {
	ref = strings.TrimSpace(ref)
	if s.registry == "" || ref == "" {
		return ref
	}
	r, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		return ref
	}
	repo := r.Context()
	if s.own[repo.Name()] {
		return ref
	}
	host := repo.RegistryStr()
	if isLocalOrEphemeralRegistry(host) || host == registryHost(s.registry) {
		return ref
	}
	return s.registry + "/" + repo.RepositoryStr() + referenceSuffix(ref)
}

// referenceSuffix is the ":tag", "@digest" or ":tag@digest" part of ref as written ("" for an implicit latest).
func referenceSuffix(ref string) string {
	suffix := ""
	if at := strings.Index(ref, "@"); at >= 0 {
		suffix = ref[at:]
		ref = ref[:at]
	}
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		suffix = ref[colon:] + suffix
	}
	return suffix
}

func registryHost(registry string) string {
	if slash := strings.Index(registry, "/"); slash >= 0 {
		return registry[:slash]
	}
	return registry
}

func isLocalOrEphemeralRegistry(host string) bool {
	h := strings.ToLower(host)
	for _, p := range []string{"localhost", "127.0.0.1", "host.docker.internal"} {
		if h == p || strings.HasPrefix(h, p+":") {
			return true
		}
	}
	return h == "ttl.sh"
}

// BuilderRunImage reads the default run image from a builder's io.buildpacks.builder.metadata label: runImages[0]
// (platform API 0.12+), else stack.runImage (older builders).
func BuilderRunImage(metadataLabel string) (string, error) {
	if strings.TrimSpace(metadataLabel) == "" {
		return "", fmt.Errorf("builder has no %s label", BuilderMetadataLabel)
	}
	var md struct {
		RunImages []struct {
			Image string `json:"image"`
		} `json:"runImages"`
		Stack struct {
			RunImage struct {
				Image string `json:"image"`
			} `json:"runImage"`
		} `json:"stack"`
	}
	if err := json.Unmarshal([]byte(metadataLabel), &md); err != nil {
		return "", fmt.Errorf("parsing %s: %w", BuilderMetadataLabel, err)
	}
	for _, ri := range md.RunImages {
		if ri.Image != "" {
			return ri.Image, nil
		}
	}
	if md.Stack.RunImage.Image != "" {
		return md.Stack.RunImage.Image, nil
	}
	return "", fmt.Errorf("%s names no run image", BuilderMetadataLabel)
}

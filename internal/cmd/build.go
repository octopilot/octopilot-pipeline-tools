package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/graph"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/parser"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/octopilot/octopilot-pipeline-tools/internal/pack"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	packBuild          = pack.Build
	getAllConfigs      = parser.GetAllConfigs
	getRunContext      = runcontext.GetRunContext
	remoteHead         = remote.Head
	remoteImage        = remote.Image
	remoteWrite        = remote.Write
	resolveDefaultRepo = util.ResolveDefaultRepo
)

// Builder defines the interface for building artifacts (subset of runner.Runner)
// We define this locally to make testing easier (mocking only Build method)
type Builder interface {
	Build(ctx context.Context, out io.Writer, artifacts []*latest.Artifact) ([]graph.Artifact, error)
}

// Wrap runner.NewForConfig to return our Builder interface
var newRunner func(context.Context, *runcontext.RunContext) (Builder, error) = func(ctx context.Context, rc *runcontext.RunContext) (Builder, error) {
	return runner.NewForConfig(ctx, rc)
}

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build with Skaffold. Use 'op build' for full build.",
	Long:  `Build with Skaffold. Wraps 'skaffold build' using the Go library.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("error getting cwd: %w", err)
		}

		// Clean environment variables that might contain platform suffixes
		// This ensures that we are targeting the "manifest list" tag (clean) rather than a specific platform tag
		// which might be passed by CI.
		cleanEnvVars := []string{"DOCKER_METADATA_OUTPUT_VERSION", "SKAFFOLD_TAG", "VERSION", "TAG", "IMAGE_TAG"}
		var targetVersion string

		for _, key := range cleanEnvVars {
			if val := os.Getenv(key); val != "" {
				// Clean it if it looks like it has a platform suffix
				// We expect formats like: v0.0.34_linux_arm64, v0.0.34_linux_amd64
				if strings.Contains(val, "_linux_") {
					parts := strings.Split(val, "_linux_")
					if len(parts) > 0 {
						newVal := parts[0]
						fmt.Printf("Stripping platform suffix from %s: %s -> %s\n", key, val, newVal)
						os.Setenv(key, newVal)
						if targetVersion == "" {
							targetVersion = newVal
						}
					}
				} else if targetVersion == "" {
					targetVersion = val
				}
			}
		}

		opts := prepareSkaffoldOptions(cmd, cwd)

		// Force the tag to be the clean version if we found one
		// This ensures op-base (built by Skaffold) uses the clean tag (multi-arch index)
		// instead of the platform-suffixed tag.
		if targetVersion != "" && opts.CustomTag == "" {
			fmt.Printf("Forcing Skaffold CustomTag to: %s\n", targetVersion)
			opts.CustomTag = targetVersion
		}

		repo := ""
		if v := opts.DefaultRepo.Value(); v != nil {
			repo = *v
		}

		ttlUUID, _ := cmd.Flags().GetString("ttl-uuid")
		ttlTag, _ := cmd.Flags().GetString("ttl-tag")
		if ttlTag == "" {
			ttlTag = "1h"
		}
		if ttlUUID != "" {
			repo = "ttl.sh"
			// Ephemeral ttl.sh builds: single platform
			if len(opts.Platforms) == 0 {
				opts.Platforms = []string{"linux/amd64"}
			} else if len(opts.Platforms) > 1 {
				opts.Platforms = opts.Platforms[:1]
			}
		}

		ctx := context.Background()

		// 1. Parse Config
		configs, err := getAllConfigs(ctx, opts)
		if err != nil {
			return fmt.Errorf("error parsing skaffold config: %w", err)
		}

		// 2. Create RunContext
		runCtx, err := getRunContext(ctx, opts, configs)
		if err != nil {
			return fmt.Errorf("error creating run context: %w", err)
		}

		// Optional: filter to a single artifact (for matrix/fan-out integration builds)
		artifactsToRun := runCtx.Artifacts()
		if onlyArtifact, _ := cmd.Flags().GetString("artifact"); onlyArtifact != "" {
			var filtered []*latest.Artifact
			for _, a := range artifactsToRun {
				if a.ImageName == onlyArtifact {
					filtered = append(filtered, a)
					break
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("--artifact %q not found in skaffold config (available: %v)",
					onlyArtifact, artifactImageNames(artifactsToRun))
			}
			artifactsToRun = filtered
			fmt.Printf("Building single artifact: %s\n", onlyArtifact)
		}

		// 3. Create Runner
		r, err := newRunner(ctx, runCtx)
		if err != nil {
			return fmt.Errorf("error creating runner: %w", err)
		}

		// 4. Build
		// If push is enabled and we are using buildpacks, we might want to use our direct pack integration
		// to bypass Skaffold's daemon export issues on multi-arch.

		useDirectPack := false
		// opts.PushImages check might be unreliable if not set in config, so check flag directly
		if push, _ := cmd.Flags().GetBool("push"); push {
			useDirectPack = true
		}

		if useDirectPack {
			fmt.Printf("Building with direct Pack integration (repo: %s, push: true)....\n", repo)

			var built []util.Build
			// Track built images for dependency resolution (imageName -> fullTag with digest)
			builtImages := make(map[string]string)

			for _, art := range artifactsToRun {
				if art.BuildpackArtifact != nil {
					// It's a buildpack artifact
					imageName := art.ImageName

					// Construct tag (ttl.sh ephemeral or repo)
					var fullTag string
					if ttlUUID != "" {
						suffix := deriveTTLSuffix(imageName)
						fullTag = fmt.Sprintf("ttl.sh/%s-%s:%s", ttlUUID, suffix, ttlTag)
					} else if repo != "" {
						if strings.HasSuffix(repo, "/") {
							fullTag = fmt.Sprintf("%s%s:latest", repo, imageName)
						} else {
							fullTag = fmt.Sprintf("%s/%s:latest", repo, imageName)
						}
					} else {
						fullTag = fmt.Sprintf("%s:latest", imageName)
					}

				fmt.Printf("Building artifact %s -> %s\n", imageName, fullTag)

				// Chart artifacts (image name ends with "-chart"): use Publish=false so the
				// buildpack's helm push is the only push. The buildpack pushes a proper Helm OCI
				// artifact (application/vnd.cncf.helm.chart.content.v1.tar+gzip) and writes the
				// ref to BP_HELM_OCI_OUTPUT for us to consume.
				if strings.HasSuffix(imageName, "-chart") {
					helmOutDir, err := os.MkdirTemp("", "op-helm-out-")
					if err != nil {
						return fmt.Errorf("creating helm output dir: %w", err)
					}
					defer os.RemoveAll(helmOutDir)

					// BP_HELM_OCI_REF is the OCI repo (no tag); helm push adds chart version as tag
					refBase := fullTag
					if idx := strings.LastIndex(fullTag, ":"); idx > 0 {
						refBase = fullTag[:idx]
					}
					packEnv := map[string]string{
						"BP_GO_PRIVATE":       "github.com/octopilot/*",
						"BP_HELM_OCI_REF":     refBase,
						"BP_HELM_OCI_OUTPUT":  "/out",
					}
					for _, env := range art.BuildpackArtifact.Env {
						parts := strings.SplitN(env, "=", 2)
						if len(parts) == 2 {
							packEnv[parts[0]] = parts[1]
						}
					}

					runImage := art.BuildpackArtifact.RunImage
					if resolved, ok := builtImages[runImage]; ok {
						fmt.Printf("Resolving runImage %s to built artifact %s\n", runImage, resolved)
						runImage = resolved
					}

					po := pack.BuildOptions{
						ImageName: fullTag,
						Builder:   art.BuildpackArtifact.Builder,
						Path:      filepath.Join(cwd, art.Workspace),
						Publish:   false,
						RunImage:  runImage,
						Target:    "",
						Env:       packEnv,
						SBOMDir: func() string {
							s, _ := cmd.Flags().GetString("sbom-output")
							return s
						}(),
						InsecureRegistries: opts.InsecureRegistries,
						Volumes:            []string{helmOutDir + ":/out"},
					}
					if err := packBuild(ctx, po, os.Stdout); err != nil {
						return fmt.Errorf("direct pack build (chart) failed for %s: %w", imageName, err)
					}

					refBytes, err := os.ReadFile(filepath.Join(helmOutDir, "ref"))
					if err != nil {
						return fmt.Errorf("reading helm push ref for %s: %w", imageName, err)
					}
					chartRef := strings.TrimSpace(string(refBytes))
					built = append(built, util.Build{ImageName: imageName, Tag: chartRef})
					builtImages[imageName] = chartRef
					fmt.Printf("Chart artifact %s -> %s\n", imageName, chartRef)
					continue
				}

				// Resolve RunImage
				runImage := art.BuildpackArtifact.RunImage
				if resolved, ok := builtImages[runImage]; ok {
					fmt.Printf("Resolving runImage %s to built artifact %s\n", runImage, resolved)
					runImage = resolved
				}

					// Construct env
					packEnv := map[string]string{
						"BP_GO_PRIVATE": "github.com/octopilot/*",
					}
					for _, env := range art.BuildpackArtifact.Env {
						parts := strings.SplitN(env, "=", 2)
						if len(parts) == 2 {
							packEnv[parts[0]] = parts[1]
						}
					}

					// Prepare platform list
					targetPlatforms := opts.Platforms
					if len(targetPlatforms) == 0 {
						targetPlatforms = []string{""} // Default/Host
					}

					var platformManifests []string

					// Build for each platform
					for _, platform := range targetPlatforms {
						currentTag := fullTag
						// If explicit multi-platform build, use distinct tags for intermediate images
						if len(targetPlatforms) > 1 && platform != "" {
							sanitized := strings.ReplaceAll(platform, "/", "-")
							currentTag = fmt.Sprintf("%s-%s", fullTag, sanitized)
						}

						fmt.Printf("  -> Platform: %s, Tag: %s\n", platform, currentTag)

						// Handle localhost/127.0.0.1 special case
						packImageName := currentTag
						packRunImage := runImage
						packInsecureRegistries := opts.InsecureRegistries

						rewriteLocalhost := func(s string) (string, bool) {
							if strings.Contains(s, "localhost:5001") {
								return strings.Replace(s, "localhost:5001", "127.0.0.1:5001", -1), true
							}
							if strings.Contains(s, "127.0.0.1:5001") {
								return s, true
							}
							return s, false
						}

						var rewritten bool
						if newTag, ok := rewriteLocalhost(packImageName); ok {
							packImageName = newTag
							rewritten = true
						}
						if newRun, ok := rewriteLocalhost(packRunImage); ok {
							packRunImage = newRun
							rewritten = true
						}

						if rewritten {
							packInsecureRegistries = append(packInsecureRegistries, "127.0.0.1:5001")
						}

						packVolumes := []string{}
						if caPath := os.Getenv("OP_REGISTRY_CA_PATH"); caPath != "" {
							packVolumes = append(packVolumes, fmt.Sprintf("%s:/etc/ssl/certs/registry-ca.crt:ro", caPath))
							packEnv["SSL_CERT_FILE"] = "/etc/ssl/certs/registry-ca.crt"
						}

						po := pack.BuildOptions{
							ImageName: packImageName,
							Builder:   art.BuildpackArtifact.Builder,
							Path:      filepath.Join(cwd, art.Workspace),
							Publish:   true,
							RunImage:  packRunImage,
							Target:    platform,
							SBOMDir: func() string {
								s, _ := cmd.Flags().GetString("sbom-output")
								return s
							}(),
							InsecureRegistries: packInsecureRegistries,
							Volumes:            packVolumes,
						}
						if err := packBuild(ctx, po, os.Stdout); err != nil {
							return fmt.Errorf("direct pack build failed for %s (%s): %w", imageName, platform, err)
						}

						// Keep track of the pushed tag (original registry host, not 127.0.0.1)
						platformManifests = append(platformManifests, currentTag)
					}

					// Prepare remote options for index creation/push
					remoteOpts := []remote.Option{
						remote.WithAuthFromKeychain(authn.DefaultKeychain),
					}
					for _, reg := range opts.InsecureRegistries {
						if strings.HasPrefix(fullTag, reg) {
							t := &http.Transport{
								TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
							}
							remoteOpts = append(remoteOpts, remote.WithTransport(t))
							break
						}
					}

					finalDigest := ""

					// Create Manifest List (Index) if we built multiple platforms
					if len(targetPlatforms) > 1 {
						fmt.Printf("Creating manifest list %s from %v\n", fullTag, platformManifests)

						var idx mutate.IndexAddendum
						_ = idx

						// Start with empty index
						// We'll default to OCI, but can switch to Docker
						// GHCR usually works fine with OCI Index
						var index v1.ImageIndex = empty.Index
						index = mutate.IndexMediaType(index, types.DockerManifestList)

						for _, pTag := range platformManifests {
							pRef, err := parseReferenceForRemote(pTag, opts.InsecureRegistries)
							if err != nil {
								return fmt.Errorf("parsing platform tag %s: %w", pTag, err)
							}

							// Get the remote image descriptor and image
							desc, err := remote.Get(pRef, remoteOpts...)
							if err != nil {
								return fmt.Errorf("getting platform image %s: %w", pTag, err)
							}

							img, err := desc.Image()
							if err != nil {
								return fmt.Errorf("getting image content for %s: %w", pTag, err)
							}

							index = mutate.AppendManifests(index, mutate.IndexAddendum{
								Add:        img,
								Descriptor: desc.Descriptor,
							})
						}

						// Push the index
						ref, err := parseReferenceForRemote(fullTag, opts.InsecureRegistries)
						if err != nil {
							return fmt.Errorf("parsing full tag %s: %w", fullTag, err)
						}

						if err := remote.WriteIndex(ref, index, remoteOpts...); err != nil {
							return fmt.Errorf("writing manifest list %s: %w", fullTag, err)
						}

						// Get the digest of the index we just pushed
						// Note: WriteIndex doesn't return digest directly easily without computing it
						// We can compute it from index.Digest()
						d, err := index.Digest()
						if err != nil {
							return fmt.Errorf("computing index digest: %w", err)
						}
						finalDigest = d.String()
						fmt.Printf("Successfully pushed manifest list %s (digest: %s)\n", fullTag, finalDigest)

					} else {
						// Single platform, just get the digest
						ref, err := parseReferenceForRemote(fullTag, opts.InsecureRegistries)
						if err != nil {
							return fmt.Errorf("parsing reference %q: %w", fullTag, err)
						}
						img, err := remoteHead(ref, remoteOpts...)
						if err != nil {
							return fmt.Errorf("getting image digest for %q: %w", fullTag, err)
						}
						finalDigest = img.Digest.String()
					}

					// Append digest to tag so consumers (CI) can extract it
					fullTagWithDigest := fmt.Sprintf("%s@%s", fullTag, finalDigest)

					built = append(built, util.Build{
						ImageName: imageName,
						Tag:       fullTagWithDigest,
					})

					// Record for dependency resolution
					builtImages[imageName] = fullTagWithDigest

					// Tag with version if available
					if version := os.Getenv("DOCKER_METADATA_OUTPUT_VERSION"); version != "" {
						// Construct version tag (replace :latest with :version)
						// fullTag is ...:latest
						versionTagStr := strings.TrimSuffix(fullTag, "latest") + version
						fmt.Printf("Tagging %s as %s...\n", fullTag, versionTagStr)

						if len(targetPlatforms) > 1 {
							// Push the same index to the new tag
							// We can just rely on remote.WriteIndex again or `remote.Tag` if it existed
							// We can reconstruct the index or just fetch it.
							// Actually we already have `index` variable above inside the if block.
							// Logic is split. Better:

							verRef, err := parseReferenceForRemote(versionTagStr, opts.InsecureRegistries)
							if err != nil {
								return fmt.Errorf("parsing version reference %q: %w", versionTagStr, err)
							}

							// If we built an index, we should copy it (referencing same manifests)
							// Or just pull the index we just pushed and push it to new tag.
							srcRef, _ := parseReferenceForRemote(fullTag, opts.InsecureRegistries)
							desc, err := remote.Get(srcRef, remoteOpts...)
							if err != nil {
								return fmt.Errorf("getting source index %s: %w", fullTag, err)
							}

							if desc.MediaType.IsIndex() {
								idx, err := desc.ImageIndex()
								if err != nil {
									return fmt.Errorf("getting index content: %w", err)
								}
								if err := remote.WriteIndex(verRef, idx, remoteOpts...); err != nil {
									return fmt.Errorf("tagging version index %q: %w", versionTagStr, err)
								}
							} else {
								img, err := desc.Image()
								if err != nil {
									return fmt.Errorf("getting image content: %w", err)
								}
								if err := remote.Write(verRef, img, remoteOpts...); err != nil {
									return fmt.Errorf("tagging version image %q: %w", versionTagStr, err)
								}
							}

						} else {
							// Single image copy
							ref, err := parseReferenceForRemote(fullTag, opts.InsecureRegistries)
							if err != nil {
								return fmt.Errorf("parsing reference %q: %w", fullTag, err)
							}
							img, err := remoteImage(ref, remoteOpts...)
							if err != nil {
								return fmt.Errorf("reading image %q: %w", fullTag, err)
							}
							verRef, err := parseReferenceForRemote(versionTagStr, opts.InsecureRegistries)
							if err != nil {
								return fmt.Errorf("parsing version reference %q: %w", versionTagStr, err)
							}
							if err := remoteWrite(verRef, img, remoteOpts...); err != nil {
								return fmt.Errorf("tagging version %q: %w", versionTagStr, err)
							}
						}
						fmt.Printf("Successfully pushed %s\n", versionTagStr)
					}

					// WAIT FOR IMAGE PROPAGATION
					// In some registries (GHCR, etc.), a pushed image might not be immediately available
					// for pulling by a subsequent build step (even if push succeeded).
					// We poll for it to ensure the next step in the skaffold graph can succeed.
					timeout, _ := cmd.Flags().GetDuration("propagation-timeout")
					if err := waitForImage(fullTag, timeout, opts.InsecureRegistries, remoteOpts...); err != nil {
						fmt.Printf("Warning: failed to wait for image propagation: %v\n", err)
						// Don't fail the build, hope for the best, but warn.
					}

			} else if (len(opts.Platforms) > 1 || ttlUUID != "") && art.DockerArtifact != nil {
				// Multi-arch Docker artifact: build each platform separately and assemble the
				// manifest list ourselves. The Skaffold fork runner has a bug where BuildKit's
				// provenance/attestation manifest turns per-platform tags into OCI Indexes; the
				// fork then calls .Image() on that index and fails with
				// "no child with platform X in index <arm64-tag>@<digest>".
				// We mirror the buildpack path: per-platform docker build + go-containerregistry
				// manifest list assembly, with BUILDX_NO_DEFAULT_ATTESTATIONS=1 to suppress
				// attestation manifests so each per-platform tag is a clean single-arch image.

				var fullTag string
				if ttlUUID != "" {
					suffix := deriveTTLSuffix(art.ImageName)
					fullTag = fmt.Sprintf("ttl.sh/%s-%s:%s", ttlUUID, suffix, ttlTag)
				} else if strings.HasSuffix(repo, "/") {
					fullTag = fmt.Sprintf("%s%s:latest", repo, art.ImageName)
				} else {
					fullTag = fmt.Sprintf("%s/%s:latest", repo, art.ImageName)
				}

				contextDir := filepath.Join(cwd, art.Workspace)
				dockerfilePath := art.DockerArtifact.DockerfilePath
				if dockerfilePath == "" {
					dockerfilePath = "Dockerfile"
				}
				if !filepath.IsAbs(dockerfilePath) {
					dockerfilePath = filepath.Join(contextDir, dockerfilePath)
				}

				dockerRemoteOpts := []remote.Option{
					remote.WithAuthFromKeychain(authn.DefaultKeychain),
				}
				for _, reg := range opts.InsecureRegistries {
					if strings.HasPrefix(fullTag, reg) {
						dockerRemoteOpts = append(dockerRemoteOpts, remote.WithTransport(&http.Transport{
							TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
						}))
						break
					}
				}

				var platformManifests []string

				for _, platform := range opts.Platforms {
					sanitized := strings.ReplaceAll(platform, "/", "-")
					platformTag := fmt.Sprintf("%s-%s", fullTag, sanitized)
					if ttlUUID != "" && len(opts.Platforms) == 1 {
						platformTag = fullTag
					}

					fmt.Printf("Building Docker artifact %s for platform %s -> %s\n", art.ImageName, platform, platformTag)

					// BUILDX_NO_DEFAULT_ATTESTATIONS=1 prevents BuildKit from wrapping the
					// pushed image in an OCI Index that contains an attestation child manifest.
					// Without this, `docker build --push` via BuildKit produces an Index even
					// for a single platform, breaking our manifest-list assembly below.
					buildEnv := append(os.Environ(), "BUILDX_NO_DEFAULT_ATTESTATIONS=1")
					buildArgs := []string{
						"build",
						"--platform", platform,
						"--push",
						"--tag", platformTag,
						"--file", dockerfilePath,
						contextDir,
					}
					buildCmd := exec.CommandContext(ctx, "docker", buildArgs...)
					buildCmd.Stdout = os.Stdout
					buildCmd.Stderr = os.Stderr
					buildCmd.Env = buildEnv
					if err := buildCmd.Run(); err != nil {
						return fmt.Errorf("docker build failed for %s (%s): %w", art.ImageName, platform, err)
					}

					platformManifests = append(platformManifests, platformTag)
				}

				// Assemble manifest list from per-platform images (same logic as buildpack path)
				fmt.Printf("Creating manifest list %s from %v\n", fullTag, platformManifests)

				var index v1.ImageIndex = empty.Index
				index = mutate.IndexMediaType(index, types.DockerManifestList)

				for _, pTag := range platformManifests {
					pRef, err := parseReferenceForRemote(pTag, opts.InsecureRegistries)
					if err != nil {
						return fmt.Errorf("parsing platform tag %s: %w", pTag, err)
					}
					desc, err := remote.Get(pRef, dockerRemoteOpts...)
					if err != nil {
						return fmt.Errorf("getting platform image %s: %w", pTag, err)
					}
					img, err := desc.Image()
					if err != nil {
						return fmt.Errorf("getting image content for %s: %w", pTag, err)
					}
					index = mutate.AppendManifests(index, mutate.IndexAddendum{
						Add:        img,
						Descriptor: desc.Descriptor,
					})
				}

				ref, err := parseReferenceForRemote(fullTag, opts.InsecureRegistries)
				if err != nil {
					return fmt.Errorf("parsing full tag %s: %w", fullTag, err)
				}
				if err := remote.WriteIndex(ref, index, dockerRemoteOpts...); err != nil {
					return fmt.Errorf("writing manifest list %s: %w", fullTag, err)
				}

				d, err := index.Digest()
				if err != nil {
					return fmt.Errorf("computing index digest: %w", err)
				}
				finalDigest := d.String()
				fmt.Printf("Successfully pushed manifest list %s (digest: %s)\n", fullTag, finalDigest)

				fullTagWithDigest := fmt.Sprintf("%s@%s", fullTag, finalDigest)

				// Version tag (same logic as buildpack path)
				if version := os.Getenv("DOCKER_METADATA_OUTPUT_VERSION"); version != "" {
					versionTagStr := strings.TrimSuffix(fullTag, "latest") + version
					fmt.Printf("Tagging %s as %s...\n", fullTag, versionTagStr)
					verRef, err := parseReferenceForRemote(versionTagStr, opts.InsecureRegistries)
					if err != nil {
						return fmt.Errorf("parsing version reference %q: %w", versionTagStr, err)
					}
					srcRef, _ := parseReferenceForRemote(fullTag, opts.InsecureRegistries)
					desc, err := remote.Get(srcRef, dockerRemoteOpts...)
					if err != nil {
						return fmt.Errorf("getting source index %s: %w", fullTag, err)
					}
					if desc.MediaType.IsIndex() {
						idx, err := desc.ImageIndex()
						if err != nil {
							return fmt.Errorf("getting index content: %w", err)
						}
						if err := remote.WriteIndex(verRef, idx, dockerRemoteOpts...); err != nil {
							return fmt.Errorf("tagging version index %q: %w", versionTagStr, err)
						}
					} else {
						img, err := desc.Image()
						if err != nil {
							return fmt.Errorf("getting image for version tag: %w", err)
						}
						if err := remote.Write(verRef, img, dockerRemoteOpts...); err != nil {
							return fmt.Errorf("tagging version image %q: %w", versionTagStr, err)
						}
					}
					fmt.Printf("Successfully tagged version %s\n", versionTagStr)
				}

				// Wait for propagation
				timeout, _ := cmd.Flags().GetDuration("propagation-timeout")
				if err := waitForImage(fullTag, timeout, opts.InsecureRegistries, dockerRemoteOpts...); err != nil {
					fmt.Printf("Warning: failed to wait for image propagation: %v\n", err)
				}

				built = append(built, util.Build{ImageName: art.ImageName, Tag: fullTagWithDigest})
				builtImages[art.ImageName] = fullTagWithDigest

			} else {
				// Single-platform or non-Docker artifact: delegate to Skaffold runner.
				// The Skaffold runner works correctly for single-platform builds.
				fmt.Printf("Delegating non-buildpack artifact %s to Skaffold runner...\n", art.ImageName)
				artifactsToBuild := []*latest.Artifact{art}

				bRes, err := r.Build(ctx, os.Stdout, artifactsToBuild)
				if err != nil {
					return fmt.Errorf("skaffold build failed for %s: %w", art.ImageName, err)
				}

				for _, ba := range bRes {
					built = append(built, util.Build{
						ImageName: ba.ImageName,
						Tag:       ba.Tag,
					})
					builtImages[ba.ImageName] = ba.Tag

					singleRemoteOpts := []remote.Option{
						remote.WithAuthFromKeychain(authn.DefaultKeychain),
					}
					for _, reg := range opts.InsecureRegistries {
						if strings.HasPrefix(ba.Tag, reg) {
							singleRemoteOpts = append(singleRemoteOpts, remote.WithTransport(&http.Transport{
								TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
							}))
							break
						}
					}

					timeout, _ := cmd.Flags().GetDuration("propagation-timeout")
					if err := waitForImage(ba.Tag, timeout, opts.InsecureRegistries, singleRemoteOpts...); err != nil {
						fmt.Printf("Warning: failed to wait for image propagation for %s: %v\n", ba.Tag, err)
					}
				}
			}
			}

			// Write build_result.json
			if err := writeBuildResult(built); err != nil {
				return err
			}
			return nil
		}

		fmt.Printf("Building with Skaffold library (repo: %s)....\n", repo)
		buildArtifacts, err := r.Build(ctx, os.Stdout, artifactsToRun)
		if err != nil {
			return fmt.Errorf("build failed: %w", err)
		}

		// 5. Write build_result.json
		var built []util.Build
		for _, ba := range buildArtifacts {
			built = append(built, util.Build{ImageName: ba.ImageName, Tag: ba.Tag})
		}
		if err := writeBuildResult(built); err != nil {
			return err
		}
		return nil
	},
}

// parseReferenceForRemote parses an image reference for use with remote get/write.
// When the tag's registry is in insecureRegistries, uses name.Insecure so that HTTP
// (no TLS) is allowed; InsecureSkipVerify in remote options handles self-signed TLS.
func parseReferenceForRemote(tag string, insecureRegistries []string) (name.Reference, error) {
	for _, reg := range insecureRegistries {
		if strings.HasPrefix(tag, reg) {
			return name.ParseReference(tag, name.Insecure)
		}
	}
	return name.ParseReference(tag)
}

// waitForImage polls the registry until the image is available or timeout
func waitForImage(tag string, timeout time.Duration, insecureRegistries []string, opts ...remote.Option) error {
	fmt.Printf("Waiting for image propagation: %s (timeout: %s)\n", tag, timeout)

	ref, err := parseReferenceForRemote(tag, insecureRegistries)
	if err != nil {
		return err
	}

	start := time.Now()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	// Initial check
	if _, err := remoteHead(ref, opts...); err == nil {
		fmt.Printf("\nImage found: %s\n", tag)
		return nil
	}

	fmt.Print("Waiting")
	for range ticker.C {
		fmt.Print(".") // Progress indicator
		_, err := remoteHead(ref, opts...)
		if err == nil {
			fmt.Printf("\nImage found: %s\n", tag)
			return nil
		}
		if time.Since(start) > timeout {
			fmt.Println() // Newline after progress
			return fmt.Errorf("timeout waiting for image %s after %s", tag, timeout)
		}
	}
	return fmt.Errorf("timeout waiting for image %s", tag)
}

func writeBuildResult(builds []util.Build) error {
	if len(builds) > 0 {
		buildResult := util.BuildResult{
			Builds: make([]util.BuildEntry, 0, len(builds)),
		}
		for _, b := range builds {
			buildResult.Builds = append(buildResult.Builds, util.BuildEntry(b))
		}

		f, err := os.Create("build_result.json")
		if err != nil {
			return fmt.Errorf("error creating build_result.json: %w", err)
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "Error closing build_result.json: %v\n", closeErr)
			}
		}()
		if err := json.NewEncoder(f).Encode(buildResult); err != nil {
			return fmt.Errorf("error writing build_result.json: %w", err)
		}
	}
	return nil
}

func prepareSkaffoldOptions(cmd *cobra.Command, cwd string) config.SkaffoldOptions {
	// Resolve repo (ttl.sh when --ttl-uuid is set)
	ttlUUID, _ := cmd.Flags().GetString("ttl-uuid")
	if ttlUUID != "" {
		repo := "ttl.sh"
		// opts built below with this repo; actual tag per artifact is set in RunE
		return prepareSkaffoldOptionsWithRepo(cmd, cwd, repo)
	}
	repo, _ := cmd.Flags().GetString("repo")
	if repo == "" {
		repo = resolveDefaultRepo(cwd)
	}
	return prepareSkaffoldOptionsWithRepo(cmd, cwd, repo)
}

func prepareSkaffoldOptionsWithRepo(cmd *cobra.Command, cwd string, repo string) config.SkaffoldOptions {

	// Resolve filename
	filename, _ := cmd.Flags().GetString("filename")
	if filename == "" {
		filename = "skaffold.yaml"
	}
	// Make absolute
	if !filepath.IsAbs(filename) {
		filename = filepath.Join(cwd, filename)
	}

	// Prepare Skaffold Options
	opts := config.SkaffoldOptions{
		ConfigurationFile: filename,
		Command:           "build",
		CacheArtifacts:    false,
		DefaultRepo:       config.NewStringOrUndefined(&repo),
		AssumeYes:         true, // non-interactive
		Trigger:           "manual",
		Profiles:          []string{},
		CustomLabels:      []string{},
		Platforms:         []string{},
	}

	if val, _ := cmd.Flags().GetString("platform"); val != "" {
		// Split comma-separated platforms
		opts.Platforms = strings.Split(val, ",")
	}

	// Handle push flag explicitly
	if cmd.Flags().Changed("push") {
		val, _ := cmd.Flags().GetBool("push")
		opts.PushImages = config.NewBoolOrUndefined(&val)
	}
	// If not changed, leave as nil (undefined), which matches legacy behavior and passes tests.

	// Handle profile/label/namespace from env (backward compatibility)
	if val := viper.GetString("SKAFFOLD_PROFILE"); val != "" {
		opts.Profiles = append(opts.Profiles, val)
	}
	if val := viper.GetString("SKAFFOLD_LABEL"); val != "" {
		opts.CustomLabels = append(opts.CustomLabels, val)
	}
	if val := viper.GetString("SKAFFOLD_NAMESPACE"); val != "" {
		opts.Namespace = val
	}

	// Handle insecure registries from env and CLI (self-signed TLS or HTTP)
	if val := os.Getenv("SKAFFOLD_INSECURE_REGISTRY"); val != "" {
		opts.InsecureRegistries = append(opts.InsecureRegistries, strings.Split(val, ",")...)
	}
	if val := os.Getenv("SKAFFOLD_INSECURE_REGISTRIES"); val != "" {
		opts.InsecureRegistries = append(opts.InsecureRegistries, strings.Split(val, ",")...)
	}
	if cmd.Flags().Changed("insecure-registry") {
		if val, _ := cmd.Flags().GetString("insecure-registry"); val != "" {
			opts.InsecureRegistries = append(opts.InsecureRegistries, strings.Split(val, ",")...)
		}
	}
	return opts
}

// artifactImageNames returns image names from artifacts for error messages.
func artifactImageNames(artifacts []*latest.Artifact) []string {
	names := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		names = append(names, a.ImageName)
	}
	return names
}

// deriveTTLSuffix returns the last segment of the image name (e.g. cronjob-log-monitor-chart -> chart).
// Used for ttl.sh tagging: ttl.sh/<uuid>-<suffix>:<tag>.
func deriveTTLSuffix(imageName string) string {
	// Strip tag if present (host/path:tag -> host/path)
	base := imageName
	if idx := strings.LastIndex(base, ":"); idx > 0 {
		base = base[:idx]
	}
	// Last path component (ghcr.io/org/cronjob-log-monitor -> cronjob-log-monitor)
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	parts := strings.Split(base, "-")
	if len(parts) == 0 {
		return "image"
	}
	return parts[len(parts)-1]
}

func init() {
	rootCmd.AddCommand(buildCmd)
	buildCmd.Flags().String("repo", "", "Registry to push to (overrides defaults)")
	buildCmd.Flags().String("ttl-uuid", "", "When set, push to ttl.sh/<ttl-uuid>-<suffix>:<ttl-tag> for ephemeral integration builds (overrides repo)")
	buildCmd.Flags().String("ttl-tag", "1h", "Tag for ttl.sh pushes when --ttl-uuid is set (default 1h)")
	buildCmd.Flags().String("artifact", "", "Build only this artifact (exact image name from skaffold, e.g. ghcr.io/org/myimage)")
	buildCmd.Flags().String("insecure-registry", "", "Registry host(s) to treat as insecure (self-signed TLS or HTTP). Comma-separated (e.g. localhost:5001,myreg:5000). Also set via SKAFFOLD_INSECURE_REGISTRY or SKAFFOLD_INSECURE_REGISTRIES.")
	buildCmd.Flags().String("platform", "", "Target platforms (e.g. linux/amd64,linux/arm64)")
	buildCmd.Flags().Bool("push", false, "Push the built images to the registry")
	buildCmd.Flags().StringP("filename", "f", "skaffold.yaml", "Path to the Skaffold configuration file")
	buildCmd.Flags().String("sbom-output", "", "Directory to output SBOMs")
	buildCmd.Flags().Duration("propagation-timeout", 180*time.Second, "Timeout for waiting for image propagation (default 180s)")
}

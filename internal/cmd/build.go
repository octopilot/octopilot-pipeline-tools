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
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/graph"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/parser"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
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

		// The version tag for the local skaffold image name comes straight from the caller (DOCKER_METADATA_OUTPUT_VERSION
		// or VERSION); the pipeline is responsible for passing a clean value.
		var targetVersion string

		for _, key := range []string{"DOCKER_METADATA_OUTPUT_VERSION", "SKAFFOLD_TAG", "VERSION", "TAG", "IMAGE_TAG"} {
			if val := os.Getenv(key); val != "" {
				targetVersion = val
				break
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
		artifactKey, _ := cmd.Flags().GetString("artifact-key")
		pushTag, _ := cmd.Flags().GetString("tag")
		if ttlTag == "" {
			ttlTag = "1h"
		}
		// --ttl-uuid is sugar: push to ttl.sh under the run UUID. Nothing else changes; in particular platforms are
		// whatever --platform says (ttl.sh serves manifest lists; only re-tagging them is unsupported, which we no longer do).
		if ttlUUID != "" {
			repo = ttlRegistry
			if !cmd.Flags().Changed("tag") {
				pushTag = ttlTag
			}
		}
		fromResults, _ := cmd.Flags().GetStringSlice("from-build-result")
		runImageOverrides, _ := cmd.Flags().GetStringSlice("run-image-override")
		priorImages, err := loadBuiltImages(fromResults, runImageOverrides)
		if err != nil {
			return err
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

		// Where builders and run images are pulled from (--image-registry, else OP_IMAGE_REGISTRY; empty = as written).
		// Every artifact in the config counts as ours, including ones another job builds, so they are never rewritten.
		imageRegistry, _ := cmd.Flags().GetString("image-registry")
		if imageRegistry == "" {
			imageRegistry = os.Getenv(util.ImageRegistryEnv)
		}
		images := util.NewImageSource(imageRegistry, artifactImageNames(runCtx.Artifacts()))
		if images.Registry() != "" {
			fmt.Printf("Pulling builders and run images through %s\n", images.Registry())
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
		// Use direct Pack when: (1) push is enabled (multi-arch/export), or (2) any buildpack artifact
		// has an explicit buildpacks list. Skaffold's buildpack builder does not pass buildpacks/env to
		// pack, so without (2) the controller would be built as Java on local builds (e.g. Tilt).

		useDirectPack := false
		if push, _ := cmd.Flags().GetBool("push"); push {
			useDirectPack = true
		}
		if !useDirectPack {
			for _, a := range artifactsToRun {
				if a.BuildpackArtifact != nil && len(a.BuildpackArtifact.Buildpacks) > 0 {
					useDirectPack = true
					break
				}
			}
		}

		if useDirectPack {
			pushStr := "push: true"
			if push, _ := cmd.Flags().GetBool("push"); !push {
				pushStr = "push: false (using direct Pack for explicit buildpacks)"
			}
			fmt.Printf("Building with direct Pack integration (repo: %s, %s)....\n", repo, pushStr)

			// Build order from skaffold's own declarations (requires, runImage), not from names.
			artifactsToRun, err = orderArtifacts(artifactsToRun)
			if err != nil {
				return err
			}
			fmt.Printf("Build order (dependencies first): %s\n", strings.Join(artifactImageNames(artifactsToRun), " | "))

			var built []util.Build
			// Track built images for dependency resolution (imageName -> fullTag with digest)
			builtImages := priorImages

			// Pack runs the lifecycle in a Docker container. On Mac/Windows the container cannot
			// reach the host registry at localhost; use host.docker.internal. On Linux 127.0.0.1 works.
			// When OP_PACK_NETWORK=host the container shares the host network, so localhost works — do not rewrite.
			hostRegistryForPack := "127.0.0.1:5001"
			if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
				hostRegistryForPack = "host.docker.internal:5001"
			}
			if os.Getenv("OP_PACK_NETWORK") == "host" {
				hostRegistryForPack = "" // container sees host's localhost; keep refs as localhost:5001 / 127.0.0.1:5001
			}

			for _, art := range artifactsToRun {
				if art.BuildpackArtifact != nil {
					// It's a buildpack artifact
					imageName := art.ImageName

					fullTag, err := pushTarget(imageName, repo, pushTag, ttlUUID, artifactKey)
					if err != nil {
						return err
					}

					fmt.Printf("Building artifact %s -> %s\n", imageName, fullTag)

					builder := images.Resolve(art.BuildpackArtifact.Builder)
					if builder != art.BuildpackArtifact.Builder {
						fmt.Printf("Builder %s pulled as %s\n", art.BuildpackArtifact.Builder, builder)
					}

					// Chart artifacts (image name ends with "-chart"): run only the helm buildpack inside
					// the builder (no pack, no run image). The buildpack pushes the Helm OCI chart and
					// writes the ref to BP_HELM_OCI_OUTPUT; we consume that and never build a container image.
					if strings.HasSuffix(imageName, "-chart") {
						helmOutDir, err := os.MkdirTemp(cwd, ".op-helm-out-")
						if err != nil {
							return fmt.Errorf("creating helm output dir: %w", err)
						}
						defer os.RemoveAll(helmOutDir)
						layersDir, err := os.MkdirTemp(cwd, ".op-helm-layers-")
						if err != nil {
							return fmt.Errorf("creating helm layers dir: %w", err)
						}
						defer os.RemoveAll(layersDir)
						// Builder container runs as non-root; ensure it can write to bind-mounted dirs.
						_ = os.Chmod(helmOutDir, 0o777)
						_ = os.Chmod(layersDir, 0o777)

						hostWS := os.Getenv("GITHUB_WORKSPACE")
						helmOutDirHost := helmOutDir
						layersDirHost := layersDir
						if hostWS != "" {
							helmOutDirHost = filepath.Join(hostWS, filepath.Base(helmOutDir))
							layersDirHost = filepath.Join(hostWS, filepath.Base(layersDir))
						}
						workspacePath := filepath.Join(cwd, art.Workspace)
						if hostWS != "" {
							workspacePath = filepath.Join(hostWS, art.Workspace)
						}

						refBase := fullTag
						if idx := strings.LastIndex(fullTag, ":"); idx > 0 {
							refBase = fullTag[:idx]
						}
						rewrite := func(s string) string {
							if hostRegistryForPack == "" {
								return s
							}
							return strings.ReplaceAll(strings.ReplaceAll(s, "localhost:5001", hostRegistryForPack), "127.0.0.1:5001", hostRegistryForPack)
						}
						chartPackRefBase := rewrite(refBase)
						// Chart build runs in a container (no host network). So when the registry is localhost/127.0.0.1,
						// the container cannot reach it. On darwin/windows use host.docker.internal so the container can push.
						chartRefForContainer := chartPackRefBase
						if strings.Contains(chartPackRefBase, "localhost:5001") || strings.Contains(chartPackRefBase, "127.0.0.1:5001") {
							if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
								chartRefForContainer = strings.ReplaceAll(strings.ReplaceAll(chartPackRefBase, "localhost:5001", "host.docker.internal:5001"), "127.0.0.1:5001", "host.docker.internal:5001")
							}
						}
						chartInsecureRegistries := opts.InsecureRegistries
						if hostRegistryForPack != "" && (strings.Contains(fullTag, "localhost:5001") || strings.Contains(fullTag, "127.0.0.1:5001")) {
							chartInsecureRegistries = append(chartInsecureRegistries, hostRegistryForPack)
						}
						if chartRefForContainer != chartPackRefBase {
							chartInsecureRegistries = append(chartInsecureRegistries, "host.docker.internal:5001")
						}
						chartEnv := map[string]string{
							"BP_HELM_OCI_REF":    chartRefForContainer,
							"BP_HELM_OCI_OUTPUT": "/out",
						}
						if idx := strings.Index(chartRefForContainer, "/"); idx > 0 {
							chartRegistryHost := chartRefForContainer[:idx]
							for _, reg := range chartInsecureRegistries {
								if reg == chartRegistryHost {
									chartEnv["BP_HELM_OCI_PLAIN_HTTP"] = "true"
									break
								}
							}
						}
						for _, env := range art.BuildpackArtifact.Env {
							parts := strings.SplitN(env, "=", 2)
							if len(parts) == 2 {
								chartEnv[parts[0]] = parts[1]
							}
						}

						if err := runChartBuildInBuilder(ctx, builder, workspacePath, layersDirHost, helmOutDirHost, chartEnv); err != nil {
							return fmt.Errorf("chart build (helm buildpack in builder) failed for %s: %w", imageName, err)
						}

						chartRef, err := readChartRef(helmOutDir, fullTag, filepath.Join(cwd, art.Workspace), imageName)
						if err != nil {
							return err
						}
						built = append(built, util.Build{ImageName: imageName, Tag: chartRef})
						builtImages[imageName] = chartRef
						fmt.Printf("Chart artifact %s -> %s\n", imageName, chartRef)
						fmt.Printf("  build_result entry: %s -> %s\n", imageName, chartRef)
						continue
					}

					// Non-chart buildpack path: use pack with run image (unchanged for other Skaffold
					// configurations). Run image is only skipped for "-chart" artifacts above.
					// Multicontext: runImage may reference a previously built artifact (e.g. base-image).
					runImage := art.BuildpackArtifact.RunImage
					if resolved, ok := builtImages[runImage]; ok {
						fmt.Printf("Resolving runImage %s to built artifact %s\n", runImage, resolved)
						runImage = resolved
					} else {
						if runImage == "" && images.Registry() != "" {
							// pack would pull the builder's default run image from its own registry; name it so it can be
							// pulled through the configured registry too.
							if def, err := builderRunImageLookup(builder, opts.InsecureRegistries); err != nil {
								fmt.Printf("Warning: default run image of %s not read (%v); pack uses the builder's own\n", builder, err)
							} else {
								runImage = def
							}
						}
						if pulled := images.Resolve(runImage); pulled != runImage {
							fmt.Printf("Run image %s pulled as %s\n", runImage, pulled)
							runImage = pulled
						}
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
					// Private git deps during cargo fetch. The buildpack writes
					// a git insteadOf and deletes it before the layer is exported.
					if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
						packEnv["GITHUB_TOKEN"] = tok
					}

					// Prepare platform list
					targetPlatforms := opts.Platforms
					if len(targetPlatforms) == 0 {
						targetPlatforms = []string{""} // Default/Host
					}

					var platformManifests []string

					// Build for each platform
					for _, platform := range targetPlatforms {
						currentTag := platformTag(fullTag, platform, len(targetPlatforms))

						fmt.Printf("  -> Platform: %s, Tag: %s\n", platform, currentTag)

						// Pack runs the lifecycle in a Docker container; hostRegistryForPack is set above (host-aware).
						packImageName := currentTag
						packRunImage := runImage
						packInsecureRegistries := opts.InsecureRegistries

						rewriteForPackContainer := func(s string) (string, bool) {
							if hostRegistryForPack == "" {
								return s, false
							}
							if strings.Contains(s, "localhost:5001") || strings.Contains(s, "127.0.0.1:5001") {
								out := strings.ReplaceAll(strings.ReplaceAll(s, "localhost:5001", hostRegistryForPack), "127.0.0.1:5001", hostRegistryForPack)
								return out, true
							}
							return s, false
						}

						var rewritten bool
						if newTag, ok := rewriteForPackContainer(packImageName); ok {
							packImageName = newTag
							rewritten = true
						}
						if newRun, ok := rewriteForPackContainer(packRunImage); ok {
							packRunImage = newRun
							rewritten = true
						}

						if rewritten {
							packInsecureRegistries = append(packInsecureRegistries, hostRegistryForPack)
						}

						packVolumes := []string{}
						if caPath := os.Getenv("OP_REGISTRY_CA_PATH"); caPath != "" {
							packVolumes = append(packVolumes, fmt.Sprintf("%s:/etc/ssl/certs/registry-ca.crt:ro", caPath))
							packEnv["SSL_CERT_FILE"] = "/etc/ssl/certs/registry-ca.crt"
						}

						po := pack.BuildOptions{
							ImageName:  packImageName,
							Builder:    builder,
							Path:       filepath.Join(cwd, art.Workspace),
							Publish:    true,
							RunImage:   packRunImage,
							Env:        packEnv,
							Buildpacks: art.BuildpackArtifact.Buildpacks,
							Target:     platform,
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

					propagation, _ := cmd.Flags().GetDuration("propagation-timeout")
					fullTagWithDigest, err := publishImage(fullTag, platformManifests, opts.InsecureRegistries, propagation)
					if err != nil {
						return err
					}
					// The caller's version (branch on main, v1.2.3 on tags) as an additional tag on the same manifest, so
					// consumers can pin e.g. op:main. build_result.json keeps the digest-pinned primary reference.
					if targetVersion != "" && targetVersion != pushTag && repo != ttlRegistry {
						if err := tagAlias(fullTag, targetVersion, opts.InsecureRegistries); err != nil {
							return err
						}
					}
					built = append(built, util.Build{
						ImageName: imageName,
						Tag:       fullTagWithDigest,
					})
					fmt.Printf("  build_result entry: %s -> %s\n", imageName, fullTagWithDigest)

					// Record for dependency resolution
					builtImages[imageName] = fullTagWithDigest

					if packEnv["BP_RUST_BRRTROUTER"] == "1" {
						slices, err := fanoutSuite(filepath.Join(cwd, art.Workspace), fullTagWithDigest, repo, pushTag, ttlUUID, opts.InsecureRegistries, propagation)
						if err != nil {
							return fmt.Errorf("suite fan-out for %s: %w", imageName, err)
						}
						for _, s := range slices {
							built = append(built, s)
							builtImages[s.ImageName] = s.Tag
							fmt.Printf("  build_result entry: %s -> %s\n", s.ImageName, s.Tag)
						}
					}

				} else if art.DockerArtifact != nil && len(opts.Platforms) > 0 {
					// Multi-arch Docker artifact: build each platform separately and assemble the
					// manifest list ourselves. The Skaffold fork runner has a bug where BuildKit's
					// provenance/attestation manifest turns per-platform tags into OCI Indexes; the
					// fork then calls .Image() on that index and fails with
					// "no child with platform X in index <arm64-tag>@<digest>".
					// We mirror the buildpack path: per-platform docker build + go-containerregistry
					// manifest list assembly, with BUILDX_NO_DEFAULT_ATTESTATIONS=1 to suppress
					// attestation manifests so each per-platform tag is a clean single-arch image.

					fullTag, err := pushTarget(art.ImageName, repo, pushTag, ttlUUID, artifactKey)
					if err != nil {
						return err
					}

					contextDir := filepath.Join(cwd, art.Workspace)
					dockerfilePath := art.DockerArtifact.DockerfilePath
					if dockerfilePath == "" {
						dockerfilePath = "Dockerfile"
					}
					if !filepath.IsAbs(dockerfilePath) {
						dockerfilePath = filepath.Join(contextDir, dockerfilePath)
					}

					var platformManifests []string

					for _, platform := range opts.Platforms {
						platformTag := platformTag(fullTag, platform, len(opts.Platforms))

						fmt.Printf("Building Docker artifact %s for platform %s -> %s\n", art.ImageName, platform, platformTag)

						// BUILDX_NO_DEFAULT_ATTESTATIONS=1 prevents BuildKit from wrapping the
						// pushed image in an OCI Index that contains an attestation child manifest.
						// Without this, `docker build --push` via BuildKit produces an Index even
						// for a single platform, breaking our manifest-list assembly below.
						buildEnv := append(os.Environ(), "BUILDX_NO_DEFAULT_ATTESTATIONS=1")
						buildArgs := dockerCLIArgs(art, platform, platformTag, dockerfilePath, contextDir, images.Registry())
						buildCmd := exec.CommandContext(ctx, "docker", buildArgs...)
						buildCmd.Stdout = os.Stdout
						buildCmd.Stderr = os.Stderr
						buildCmd.Env = buildEnv
						if err := buildCmd.Run(); err != nil {
							return fmt.Errorf("docker build failed for %s (%s): %w", art.ImageName, platform, err)
						}

						platformManifests = append(platformManifests, platformTag)
					}

					propagation, _ := cmd.Flags().GetDuration("propagation-timeout")
					fullTagWithDigest, err := publishImage(fullTag, platformManifests, opts.InsecureRegistries, propagation)
					if err != nil {
						return err
					}
					// The caller's version (branch on main, v1.2.3 on tags) as an additional tag on the same manifest, so
					// consumers can pin e.g. op:main. build_result.json keeps the digest-pinned primary reference.
					if targetVersion != "" && targetVersion != pushTag && repo != ttlRegistry {
						if err := tagAlias(fullTag, targetVersion, opts.InsecureRegistries); err != nil {
							return err
						}
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
							remote.WithAuthFromKeychain(util.Keychain),
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
							return fmt.Errorf("image %s was pushed but is not resolvable from the registry: %w", ba.Tag, err)
						}
					}
				}
			}

			// build_result.json lists artifacts in build order (dependencies first)
			fmt.Printf("Writing build_result.json (%d entries):\n", len(built))
			for _, b := range built {
				fmt.Printf("  %s -> %s\n", b.ImageName, b.Tag)
			}
			if err := writeBuildResult(built); err != nil {
				return err
			}
			return nil
		}

		artifactsToRun, err = orderArtifacts(artifactsToRun)
		if err != nil {
			return err
		}
		fmt.Printf("Build order (dependencies first): %s\n", strings.Join(artifactImageNames(artifactsToRun), " | "))

		fmt.Printf("Building with Skaffold library (repo: %s)....\n", repo)
		buildArtifacts, err := r.Build(ctx, os.Stdout, artifactsToRun)
		if err != nil {
			return fmt.Errorf("build failed: %w", err)
		}

		// 5. Write build_result.json (deterministic order: primary first)
		var built []util.Build
		for _, ba := range buildArtifacts {
			built = append(built, util.Build{ImageName: ba.ImageName, Tag: ba.Tag})
		}
		fmt.Printf("Writing build_result.json (%d entries):\n", len(built))
		for _, b := range built {
			fmt.Printf("  %s -> %s\n", b.ImageName, b.Tag)
		}
		if err := writeBuildResult(built); err != nil {
			return err
		}
		return nil
	},
}

// helmBuildpackPath is the path inside the builder image to the octopilot/helm buildpack (id slug + version).
// Overridable via env OP_HELM_BUILDPACK_PATH for different builder layouts.
const defaultHelmBuildpackPath = "/cnb/buildpacks/octopilot_helm/0.1.3"

func getHelmBuildpackPath() string {
	if p := os.Getenv("OP_HELM_BUILDPACK_PATH"); p != "" {
		return p
	}
	return defaultHelmBuildpackPath
}

// runChartBuildInBuilder runs only the helm buildpack inside the builder container (detect + build).
// No run image or pack lifecycle: the buildpack pushes the Helm OCI chart and writes the ref to helmOutDir.
// Uses the Docker API so it works when op runs inside a container that has the socket mounted but no docker CLI.
// workspacePath is the host path to the chart context; helmOutDirHost is the host path for the output volume (ref, etc.).
func runChartBuildInBuilder(ctx context.Context, builderImage, workspacePath, layersDirHost, helmOutDirHost string, env map[string]string) error {
	buildpackPath := getHelmBuildpackPath()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("helm buildpack in builder: docker client: %w", err)
	}
	defer cli.Close()

	// Pull builder image if not present (e.g. in CI the daemon may not have it).
	rd, pullErr := cli.ImagePull(ctx, builderImage, image.PullOptions{})
	if pullErr == nil {
		_, _ = io.Copy(io.Discard, rd)
		rd.Close()
	}
	// Ignore pull errors (e.g. image already present or no network); ContainerCreate will fail with a clear error if missing.

	envSlice := []string{"CNB_BUILD_DIR=/workspace", "CNB_LAYERS_DIR=/layers"}
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}
	cfg := &container.Config{
		Image:        builderImage,
		Cmd:          []string{"/bin/sh", "-c", buildpackPath + "/bin/detect && " + buildpackPath + "/bin/build"},
		Env:          envSlice,
		AttachStdout: true,
		AttachStderr: true,
	}
	hostCfg := &container.HostConfig{
		Binds: []string{
			workspacePath + ":/workspace:ro",
			layersDirHost + ":/layers",
			helmOutDirHost + ":/out",
		},
		AutoRemove: true,
	}
	// On Linux, when pushing to localhost registry the container needs host network to reach it (darwin/windows use host.docker.internal in env).
	if runtime.GOOS == "linux" {
		if ociRef := env["BP_HELM_OCI_REF"]; ociRef != "" && (strings.Contains(ociRef, "localhost:5001") || strings.Contains(ociRef, "127.0.0.1:5001")) {
			hostCfg.NetworkMode = "host"
		}
	}
	createResp, err := cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		return fmt.Errorf("helm buildpack in builder: create container: %w", err)
	}
	if err := cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("helm buildpack in builder: start container: %w", err)
	}
	attachResp, err := cli.ContainerAttach(ctx, createResp.ID, container.AttachOptions{Stream: true, Stdout: true, Stderr: true})
	if err != nil {
		_ = cli.ContainerRemove(ctx, createResp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("helm buildpack in builder: attach: %w", err)
	}
	defer attachResp.Close()
	go func() { _, _ = stdcopy.StdCopy(os.Stdout, os.Stderr, attachResp.Reader) }()
	statusCh, errCh := cli.ContainerWait(ctx, createResp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("helm buildpack in builder: wait: %w", err)
		}
	case body := <-statusCh:
		if body.StatusCode != 0 {
			return fmt.Errorf("helm buildpack in builder: exit code %d", body.StatusCode)
		}
	}
	return nil
}

// readChartRef reads the helm push ref from helmOutDir/ref. It validates that the ref has
// Helm OCI shape (registry/repo/chartname:version or ...@digest). If the buildpack wrote
// an image-style ref (e.g. ttl.sh/uuid-chart:1h@sha256:...) without the chart name segment,
// it normalizes using Chart.yaml name and version so Flux and helm pull get a pullable ref.
func readChartRef(helmOutDir, fullTag, workspaceDir, imageName string) (string, error) {
	refBase := fullTag
	if idx := strings.LastIndex(fullTag, ":"); idx > 0 {
		refBase = fullTag[:idx]
	}
	chartYaml := filepath.Join(workspaceDir, "Chart.yaml")
	yb, yerr := os.ReadFile(chartYaml)
	if yerr != nil {
		return "", fmt.Errorf("reading helm push ref for %s: need Chart.yaml: %w", imageName, yerr)
	}
	content := string(yb)
	version := parseChartVersionFromYAML(content)
	if version == "" {
		version = "latest"
	}
	chartName := parseChartNameFromYAML(content)
	if chartName == "" {
		chartName = "chart"
	}

	refPath := filepath.Join(helmOutDir, "ref")
	refBytes, err := os.ReadFile(refPath)
	if err == nil {
		ref := strings.TrimSpace(string(refBytes))
		if isValidHelmChartRef(ref, chartName) {
			return ref, nil
		}
		// Buildpack wrote an invalid (image-style) ref; normalize to repo/chartname:version@digest.
		digest := extractDigestFromRef(ref)
		normalized := refBase + "/" + chartName + ":" + version
		if digest != "" {
			normalized += "@" + digest
		}
		fmt.Printf("Chart ref from buildpack missing chart name segment; normalized to %s\n", normalized)
		return normalized, nil
	}

	// Ref file not present; derive full ref from refBase and Chart.yaml.
	chartRef := refBase + "/" + chartName + ":" + version
	fmt.Printf("Chart ref file not found; using derived ref %s (from Chart.yaml)\n", chartRef)
	return chartRef, nil
}

// isValidHelmChartRef reports whether ref is registry/repo/<chartName>:version[@digest].
// helm push oci://registry/repo stores the chart at registry/repo/<chart name>:<version>.
// A ref whose last path segment is not the chart name (ttl.sh/<uuid>-chart:0.1.0, or
// ghcr.io/octopilot/igniteflux-chart:0.1.0) is only the prefix op handed helm.
func isValidHelmChartRef(ref, chartName string) bool {
	beforeDigest := ref
	if at := strings.Index(ref, "@"); at > 0 {
		beforeDigest = ref[:at]
	}
	lastSlash := strings.LastIndex(beforeDigest, "/")
	colon := strings.LastIndex(beforeDigest, ":")
	if lastSlash <= 0 || colon <= lastSlash {
		return false
	}
	return beforeDigest[lastSlash+1:colon] == chartName
}

// extractDigestFromRef returns the digest part of ref (e.g. "sha256:...") or empty if none.
func extractDigestFromRef(ref string) string {
	if at := strings.Index(ref, "@"); at > 0 && at+1 < len(ref) {
		return ref[at+1:]
	}
	return ""
}

// parseChartVersionFromYAML extracts the first "version:" value from Chart.yaml-style content.
func parseChartVersionFromYAML(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "version:"))
			v = strings.Trim(v, "\"'")
			return v
		}
	}
	return ""
}

// parseChartNameFromYAML extracts the first "name:" value from Chart.yaml-style content.
func parseChartNameFromYAML(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			n := strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			n = strings.Trim(n, "\"'")
			return n
		}
	}
	return ""
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

// builderRunImageLookup reads a builder's default run image from its metadata label in the registry. A variable so
// tests can stub the registry.
var builderRunImageLookup = func(builder string, insecureRegistries []string) (string, error) {
	ref, err := name.ParseReference(builder, name.WeakValidation)
	if err != nil {
		return "", err
	}
	img, err := remote.Image(ref, remoteOptionsFor(builder, insecureRegistries)...)
	if err != nil {
		return "", err
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return "", err
	}
	return util.BuilderRunImage(cfg.Config.Labels[util.BuilderMetadataLabel])
}

// dockerCLIArgs is the `docker build` argv for one platform. Skaffold
// buildArgs become --build-arg. A GITHUB_TOKEN in the environment is passed
// as a BuildKit secret named github_token so a Dockerfile can fetch private
// git dependencies without writing the token into an image layer.
//
// imageRegistry (--image-registry) is offered as the build arg OP_IMAGE_REGISTRY
// unless skaffold already sets it. op cannot rewrite FROM lines; a Dockerfile
// opts in with `ARG OP_IMAGE_REGISTRY=docker.io` and `FROM ${OP_IMAGE_REGISTRY}/library/ubuntu:jammy`.
func dockerCLIArgs(art *latest.Artifact, platform, tag, dockerfilePath, contextDir, imageRegistry string) []string {
	args := []string{
		"build",
		"--platform", platform,
		"--push",
		"--tag", tag,
		"--file", dockerfilePath,
	}
	if art != nil && art.DockerArtifact != nil {
		keys := make([]string, 0, len(art.DockerArtifact.BuildArgs))
		for key := range art.DockerArtifact.BuildArgs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := art.DockerArtifact.BuildArgs[key]
			if value == nil {
				continue
			}
			args = append(args, "--build-arg", key+"="+*value)
		}
	}
	if imageRegistry != "" {
		if art == nil || art.DockerArtifact == nil || art.DockerArtifact.BuildArgs[util.ImageRegistryEnv] == nil {
			args = append(args, "--build-arg", util.ImageRegistryEnv+"="+imageRegistry)
		}
	}
	if os.Getenv("GITHUB_TOKEN") != "" {
		args = append(args, "--secret", "id=github_token,env=GITHUB_TOKEN")
	}
	args = append(args, contextDir)
	return args
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

func artifactImageNames(artifacts []*latest.Artifact) []string {
	names := make([]string, 0, len(artifacts))
	for _, a := range artifacts {
		names = append(names, a.ImageName)
	}
	return names
}

func init() {
	rootCmd.AddCommand(buildCmd)
	buildCmd.Flags().String("repo", "", "Registry to push to (overrides defaults)")
	buildCmd.Flags().String("ttl-uuid", "", "When set, push to ttl.sh/<ttl-uuid>-<suffix>:<ttl-tag> for ephemeral integration builds (overrides repo)")
	buildCmd.Flags().String("ttl-tag", "1h", "Tag for ttl.sh pushes when --ttl-uuid is set and --tag is not (default 1h)")
	buildCmd.Flags().String("artifact-key", "", "Short, unique key for the artifact being built (from the pipeline's detect step); with ttl.sh the image is ttl.sh/<ttl-uuid>-<key>")
	buildCmd.Flags().StringSlice("from-build-result", nil, "build_result.json file(s) from earlier builds; their images satisfy runImage references from this build (repeatable)")
	buildCmd.Flags().StringSlice("run-image-override", nil, "name=ref: use ref for a buildpack runImage that names artifact <name> (repeatable)")
	buildCmd.Flags().String("tag", "latest", "Tag for --repo pushes (release jobs re-tag by version themselves)")
	buildCmd.Flags().String("image-registry", "", "Pull buildpack builders and run images through this registry, keeping their repository path (e.g. us-docker.pkg.dev/<project>/<mirror>: ghcr.io/x/y:t -> <registry>/x/y:t, ubuntu:jammy -> <registry>/library/ubuntu:jammy). Images this config builds are never rewritten. Default: $OP_IMAGE_REGISTRY, else images are pulled as written. Dockerfile builds receive it as --build-arg OP_IMAGE_REGISTRY")
	buildCmd.Flags().String("artifact", "", "Build only this artifact (exact image name from skaffold, e.g. ghcr.io/org/myimage)")
	buildCmd.Flags().String("insecure-registry", "", "Registry host(s) to treat as insecure (self-signed TLS or HTTP). Comma-separated (e.g. localhost:5001,myreg:5000). Also set via SKAFFOLD_INSECURE_REGISTRY or SKAFFOLD_INSECURE_REGISTRIES.")
	buildCmd.Flags().String("platform", "", "Target platforms (e.g. linux/amd64,linux/arm64)")
	buildCmd.Flags().Bool("push", false, "Push the built images to the registry")
	buildCmd.Flags().StringP("filename", "f", "skaffold.yaml", "Path to the Skaffold configuration file")
	buildCmd.Flags().String("sbom-output", "", "Directory to output SBOMs")
	buildCmd.Flags().Duration("propagation-timeout", 180*time.Second, "Timeout for waiting for image propagation (default 180s)")
}

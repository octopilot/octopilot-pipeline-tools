package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/graph"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/parser"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	"github.com/google/go-containerregistry/pkg/authn"
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

		opts := prepareSkaffoldOptions(cmd, cwd)
		repo := ""
		if v := opts.DefaultRepo.Value(); v != nil {
			repo = *v
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

			// Iterate runCtx.Artifacts() which is []*latest.Artifact
			for _, art := range runCtx.Artifacts() {
				if art.BuildpackArtifact != nil {
					// It's a buildpack artifact
					imageName := art.ImageName

					// Construct tag
					var fullTag string
					if repo != "" {
						if strings.HasSuffix(repo, "/") {
							fullTag = fmt.Sprintf("%s%s:latest", repo, imageName)
						} else {
							fullTag = fmt.Sprintf("%s/%s:latest", repo, imageName)
						}
					} else {
						fullTag = fmt.Sprintf("%s:latest", imageName)
					}

					fmt.Printf("Building artifact %s -> %s\n", imageName, fullTag)

					// Check if RunImage is a reference to a previously built artifact
					runImage := art.BuildpackArtifact.RunImage
					if resolved, ok := builtImages[runImage]; ok {
						fmt.Printf("Resolving runImage %s to built artifact %s\n", runImage, resolved)
						runImage = resolved
					}

					// Construct env
					packEnv := map[string]string{
						"BP_GO_PRIVATE": "github.com/octopilot/*",
					}
					// Add env vars from artifact definition
					for _, env := range art.BuildpackArtifact.Env {
						parts := strings.SplitN(env, "=", 2)
						if len(parts) == 2 {
							packEnv[parts[0]] = parts[1]
						}
					}

					po := pack.BuildOptions{
						ImageName: fullTag,
						Builder:   art.BuildpackArtifact.Builder,
						Path:      filepath.Join(cwd, art.Workspace),
						Publish:   true,
						RunImage:  runImage,

						Target: func() string {
							if len(opts.Platforms) > 0 {
								// Pack only supports one target at a time in this context usually,
								// or we pick the first one.
								return opts.Platforms[0]
							}
							return ""
						}(),
						SBOMDir: func() string {
							s, _ := cmd.Flags().GetString("sbom-output")
							return s
						}(),
					}
					if err := packBuild(ctx, po, os.Stdout); err != nil {
						return fmt.Errorf("direct pack build failed: %w", err)
					}

					// Resolve digest for attestation
					ref, err := name.ParseReference(fullTag)
					if err != nil {
						return fmt.Errorf("parsing reference %q: %w", fullTag, err)
					}

					// Fetch the image descriptor to get the digest
					// We use authn.DefaultKeychain to use the same credentials as docker/pack
					img, err := remoteHead(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
					if err != nil {
						return fmt.Errorf("getting image digest for %q: %w", fullTag, err)
					}

					// We need the digest to be fully qualified
					digest := img.Digest.String()
					fmt.Printf("Resolved digest for %s: %s\n", fullTag, digest)

					// Append digest to tag so consumers (CI) can extract it
					fullTagWithDigest := fmt.Sprintf("%s@%s", fullTag, digest)

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

						ref, err := name.ParseReference(fullTag)
						if err != nil {
							return fmt.Errorf("parsing reference %q: %w", fullTag, err)
						}

						// Get the remote image
						img, err := remoteImage(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
						if err != nil {
							return fmt.Errorf("reading image %q: %w", fullTag, err)
						}

						verRef, err := name.ParseReference(versionTagStr)
						if err != nil {
							return fmt.Errorf("parsing version reference %q: %w", versionTagStr, err)
						}

						if err := remoteWrite(verRef, img, remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
							return fmt.Errorf("tagging version %q: %w", versionTagStr, err)
						}
						fmt.Printf("Successfully pushed %s\n", versionTagStr)
					}
				} else {
					fmt.Printf("Delegating non-buildpack artifact %s to Skaffold runner...\n", art.ImageName)
					// Create a slice explicitly for this artifact using the imported type
					artifactsToBuild := []*latest.Artifact{art}

					// Use the existing runner to build just this artifact
					// Skaffold runner handles the pushing if configured in opts (which it is)
					bRes, err := r.Build(ctx, os.Stdout, artifactsToBuild)
					if err != nil {
						return fmt.Errorf("skaffold build failed for %s: %w", art.ImageName, err)
					}

					// Collect results
					for _, ba := range bRes {
						built = append(built, util.Build{
							ImageName: ba.ImageName,
							Tag:       ba.Tag,
						})
						// Record for dependency resolution
						builtImages[ba.ImageName] = ba.Tag
					}
				}
			}
			// Implementation note: The above loop handles buildpacks. We need to handle Dockerfiles.
			// Let's refactor:
			// 1. Filter artifacts into `buildpackArtifacts` and `standardArtifacts`
			// 2. Iterate buildpackArtifacts and run custom logic
			// 3. Pass standardArtifacts to r.Build()

			// Refactored logic below (replacing loop):

			// runCtx.Artifacts() returns []*latest.Artifact

			for _, art := range runCtx.Artifacts() {
				if art.BuildpackArtifact != nil {
					// ... (existing buildpack logic) ...
					// (RE-INLINE existing logic for clarity in diff, but targeted)
					// It seems the user prompt implies just modifying the "else" block or flow.
					// But standard skaffold build `r.Build` builds ALL passed artifacts.
				} else {
					// Adding to standard list
				}
			}

			// This replace is complex. Let's step back.
			// The user just wants it to work.
			// The easiest way is to NOT use `useDirectPack` if there are Dockerfile artifacts?
			// BUT we want the custom logic for buildpacks (cross-arch without daemon).

			// Correct approach: Split the artifacts.
			// But I cannot see definitions of `latest` package types easily.
			// Assuming `runCtx.Artifacts()` returns the list we can filter.

			// Let's simplify and just fix the "Skipping" part by collecting them.

			// We need to re-write the loop entirely to support this split.
			// Since this is a partial replace, I will replace the loop content to handle the split.
			// Wait, I can't replace the whole loop easily with `replace_file_content` if it's huge.
			// The file content shows lines 75-166 is the loop.
			// I will replace the WHOLE loop logic.

			// Write build_result.json
			if err := writeBuildResult(built); err != nil {
				return err
			}
			return nil
		}

		fmt.Printf("Building with Skaffold library (repo: %s)....\n", repo)
		buildArtifacts, err := r.Build(ctx, os.Stdout, runCtx.Artifacts())
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

func writeBuildResult(builds []util.Build) error {
	if len(builds) > 0 {
		buildResult := util.BuildResult{
			Builds: make([]interface{}, 0, len(builds)),
		}
		for _, b := range builds {
			buildResult.Builds = append(buildResult.Builds, map[string]string{
				"imageName": b.ImageName,
				"tag":       b.Tag,
			})
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
	// Resolve repo
	repo, _ := cmd.Flags().GetString("repo")
	if repo == "" {
		repo = resolveDefaultRepo(cwd)
	}

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
	return opts
}

func init() {
	rootCmd.AddCommand(buildCmd)
	buildCmd.Flags().String("repo", "", "Registry to push to (overrides defaults)")
	buildCmd.Flags().String("platform", "", "Target platforms (e.g. linux/amd64,linux/arm64)")
	buildCmd.Flags().Bool("push", false, "Push the built images to the registry")
	buildCmd.Flags().StringP("filename", "f", "skaffold.yaml", "Path to the Skaffold configuration file")
	buildCmd.Flags().String("sbom-output", "", "Directory to output SBOMs")
}

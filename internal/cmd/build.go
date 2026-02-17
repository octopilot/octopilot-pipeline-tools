package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/parser"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/octopilot/octopilot-pipeline-tools/internal/pack"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

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
		configs, err := parser.GetAllConfigs(ctx, opts)
		if err != nil {
			return fmt.Errorf("error parsing skaffold config: %w", err)
		}

		// 2. Create RunContext
		runCtx, err := runcontext.GetRunContext(ctx, opts, configs)
		if err != nil {
			return fmt.Errorf("error creating run context: %w", err)
		}

		// 3. Create Runner
		r, err := runner.NewForConfig(ctx, runCtx)
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

					po := pack.BuildOptions{
						ImageName: fullTag,
						Builder:   art.BuildpackArtifact.Builder,
						Path:      filepath.Join(cwd, art.Workspace),
						Publish:   true,
						Env: map[string]string{
							"BP_GO_PRIVATE": "github.com/octopilot/*",
						},
					}
					if err := pack.Build(ctx, po, os.Stdout); err != nil {
						return fmt.Errorf("direct pack build failed: %w", err)
					}

					// Resolve digest for attestation
					ref, err := name.ParseReference(fullTag)
					if err != nil {
						return fmt.Errorf("parsing reference %q: %w", fullTag, err)
					}

					// Fetch the image descriptor to get the digest
					// We use authn.DefaultKeychain to use the same credentials as docker/pack
					img, err := remote.Head(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
					if err != nil {
						return fmt.Errorf("fetching image properties for %q: %w", fullTag, err)
					}

					digest := img.Digest.String()
					fmt.Printf("Resolved digest for %s: %s\n", fullTag, digest)

					// Append digest to tag so consumers (CI) can extract it
					fullTagWithDigest := fmt.Sprintf("%s@%s", fullTag, digest)

					built = append(built, util.Build{
						ImageName: imageName,
						Tag:       fullTagWithDigest,
					})
				} else {
					fmt.Printf("Skipping non-buildpacks artifact %s (not supported in direct push mode yet)\n", art.ImageName)
				}
			}
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
		repo = util.ResolveDefaultRepo(cwd)
	}

	// Prepare Skaffold Options
	opts := config.SkaffoldOptions{
		ConfigurationFile: "skaffold.yaml",
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

	if val, _ := cmd.Flags().GetBool("push"); val {
		opts.PushImages = config.NewBoolOrUndefined(&val)
	}

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
}

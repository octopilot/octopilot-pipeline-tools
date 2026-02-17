package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/config"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/parser"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/runner/runcontext"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build with Skaffold. Use 'op build' for full build.",
	Long:  `Build with Skaffold. Wraps 'skaffold build' using the Go library.`,
	Run: func(cmd *cobra.Command, args []string) {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting cwd: %v\n", err)
			os.Exit(1)
		}

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

		ctx := context.Background()

		// 1. Parse Config
		configs, err := parser.GetAllConfigs(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing skaffold config: %v\n", err)
			os.Exit(1)
		}

		// 2. Create RunContext
		runCtx, err := runcontext.GetRunContext(ctx, opts, configs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating run context: %v\n", err)
			os.Exit(1)
		}

		// 3. Create Runner
		r, err := runner.NewForConfig(ctx, runCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating runner: %v\n", err)
			os.Exit(1)
		}

		// 4. Build
		fmt.Printf("Building with Skaffold library (repo: %s)...\n", repo)
		buildArtifacts, err := r.Build(ctx, os.Stdout, runCtx.Artifacts())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Build failed: %v\n", err)
			os.Exit(1)
		}

		// 5. Write build_result.json
		if len(buildArtifacts) > 0 {
			buildResult := util.BuildResult{
				Builds: make([]interface{}, 0, len(buildArtifacts)),
			}
			for _, ba := range buildArtifacts {
				buildResult.Builds = append(buildResult.Builds, map[string]string{
					"imageName": ba.ImageName,
					"tag":       ba.Tag,
				})
			}

			f, err := os.Create("build_result.json")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating build_result.json: %v\n", err)
			} else {
				defer func() {
					if closeErr := f.Close(); closeErr != nil {
						fmt.Fprintf(os.Stderr, "Error closing build_result.json: %v\n", closeErr)
					}
				}()
				if err := json.NewEncoder(f).Encode(buildResult); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing build_result.json: %v\n", err)
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(buildCmd)
	buildCmd.Flags().String("repo", "", "Registry to push to (overrides defaults)")
	buildCmd.Flags().String("platform", "", "Target platforms (e.g. linux/amd64,linux/arm64)")
}

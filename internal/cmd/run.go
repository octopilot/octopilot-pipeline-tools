package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [context]",
	Short: "Run a built image for a Skaffold context (local dev).",
	Long: `Run a built image locally using docker run.

Use "op run context list" to list contexts defined in skaffold.yaml.
Use "op run <context>" to run that context.

The image reference is resolved in order:
  1. build_result.json (if present) — uses the exact pushed digest.
  2. Default repo from .github/octopilot.yaml or SKAFFOLD_DEFAULT_REPO,
     with tag "latest" as fallback.

Ports, environment variables, and volume mounts are read from
.github/octopilot.yaml; if absent, defaults apply (8080:8080, PORT=8080).`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		skaffoldFile, _ := cmd.Flags().GetString("skaffold-file")

		artifacts, err := util.ParseSkaffoldArtifacts(filepath.Join(cwd, skaffoldFile))
		if err != nil {
			return fmt.Errorf("reading skaffold.yaml: %w", err)
		}

		// "op run context list"
		if len(args) >= 2 && args[0] == "context" && args[1] == "list" {
			fmt.Println("Contexts (use: op run <context>):")
			for _, art := range artifacts {
				fmt.Printf("  %s\n", art.Context)
			}
			return nil
		}

		contextName := args[0]
		var matched *util.Artifact
		for i, art := range artifacts {
			if art.Context == contextName {
				matched = &artifacts[i]
				break
			}
		}
		if matched == nil {
			return fmt.Errorf("unknown context %q — use 'op run context list' to see available contexts", contextName)
		}

		// Resolve image: prefer build_result.json, fall back to default repo + latest.
		fullImage := resolveRunImage(cwd, matched.Image)

		cfg, _ := util.LoadRunConfig(cwd)
		contextDir := filepath.Join(cwd, matched.Context)
		hostPorts, env, volumes, containerPort := util.GetRunOptionsForContext(contextName, cwd, cfg, contextDir)

		if len(hostPorts) == 0 {
			freePort, err := util.FindFreePort(8080, 100)
			if err != nil {
				return fmt.Errorf("finding free port: %w", err)
			}
			hostPorts = []string{fmt.Sprintf("%d:%d", freePort, containerPort)}
			fmt.Fprintf(os.Stderr, "Mapped to http://localhost:%d\n", freePort)
		}

		dockerArgs := []string{"run", "--rm", "-it"}
		for _, p := range hostPorts {
			dockerArgs = append(dockerArgs, "-p", p)
		}
		for k, v := range env {
			dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, v))
		}
		for _, v := range volumes {
			dockerArgs = append(dockerArgs, "-v", v)
		}
		dockerArgs = append(dockerArgs, fullImage)

		fmt.Fprintf(os.Stderr, "Running: docker %v\n", dockerArgs)
		c := exec.Command("docker", dockerArgs...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
		if err := c.Run(); err != nil {
			return fmt.Errorf("docker run failed: %w", err)
		}
		return nil
	},
}

// resolveRunImage finds the fully-qualified image reference for imageName.
// It checks build_result.json first; if absent or the image isn't listed,
// it falls back to <defaultRepo>/<imageName>:latest.
func resolveRunImage(cwd, imageName string) string {
	if res, err := util.ReadBuildResult(cwd); err == nil {
		if tag, err := util.GetTagForImage(res, imageName); err == nil && tag != "" {
			return tag
		}
	}
	repo := util.ResolveDefaultRepo(cwd)
	return fmt.Sprintf("%s/%s:latest", repo, imageName)
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().String("skaffold-file", "skaffold.yaml", "Path to skaffold.yaml")
}

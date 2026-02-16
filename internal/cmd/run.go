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
	Short: "Run a built image for a Skaffold context.",
	Long: `Run a built image for a Skaffold context (local dev only).
Use "op run context list" to list runnable contexts.
Use "op run <context>" to run that context.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		skaffoldFile, _ := cmd.Flags().GetString("skaffold-file")

		artifacts, err := util.ParseSkaffoldArtifacts(filepath.Join(cwd, skaffoldFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read skaffold.yaml: %v\n", err)
			os.Exit(1)
		}

		if len(args) >= 2 && args[0] == "context" && args[1] == "list" {
			fmt.Println("Contexts (use: op run <context>):")
			for _, art := range artifacts {
				fmt.Printf("  %s\n", art.Context)
			}
			return
		}

		contextName := args[0]
		var matched *util.Artifact
		for _, art := range artifacts {
			if art.Context == contextName {
				matched = &art
				break
			}
		}

		if matched == nil {
			fmt.Fprintf(os.Stderr, "Unknown context: %s. Use 'op run context list'.\n", contextName)
			os.Exit(1)
		}

		// Resolve image logic can be complex (build_result.json vs default).
		// For MVP Phase 2, let's use default repo/tag as fallback or "latest".
		// Python logic checks build_result.json first.
		// We'll skip build_result.json for now and assume default repo layout:
		// repo/image:tag

		repo := util.ResolveDefaultRepo(cwd)
		tag := "latest" // Load from config if available

		fullImage := fmt.Sprintf("%s/%s:%s", repo, matched.Image, tag)

		// Get run options
		cfg, _ := util.LoadRunConfig(cwd)
		contextDir := filepath.Join(cwd, matched.Context)

		hostPorts, env, volumes, containerPort := util.GetRunOptionsForContext(contextName, cwd, cfg, contextDir)

		if len(hostPorts) == 0 {
			freePort, err := util.FindFreePort(8080, 100)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to find free port: %v\n", err)
				os.Exit(1)
			}
			hostPorts = []string{fmt.Sprintf("%d:%d", freePort, containerPort)}
			fmt.Fprintf(os.Stderr, "Mapped to http://localhost:%d\n", freePort)
		}

		// Prepare docker run
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

		fmt.Printf("Running: docker %v\n", dockerArgs)
		c := exec.Command("docker", dockerArgs...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
		if err := c.Run(); err != nil {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().String("skaffold-file", "skaffold.yaml", "Path to skaffold.yaml")
}

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch-deployment",
	Short: "Wait for Flux to update deployment, then kubectl rollout status.",
	Run: func(cmd *cobra.Command, args []string) {
		component, _ := cmd.Flags().GetString("component")
		env, _ := cmd.Flags().GetString("environment")
		namespace, _ := cmd.Flags().GetString("namespace")
		timeout, _ := cmd.Flags().GetString("timeout")
		buildResultDir, _ := cmd.Flags().GetString("build-result-dir")

		destRepo := util.GetWatchDestinationRepository(env)
		if destRepo == "" {
			fmt.Fprintln(os.Stderr, "Error: Could not resolve destination repository.")
			os.Exit(1)
		}

		res, err := util.ReadBuildResult(buildResultDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading build_result.json: %v\n", err)
			os.Exit(1)
		}
		tag, err := util.GetFirstTag(res)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting tag: %v\n", err)
			os.Exit(1)
		}

		// Extract just the tag part if it's a full ref
		// If tag is "repo/image:v1", we want "v1" or "image:v1" to match against k8s?
		// Python logic:
		// image_tag_only = tag.split(":")[-1]
		// status check compares if image_tag_only in current_image or tag in current_image.

		imageTagOnly := tag
		if idx := strings.LastIndex(tag, ":"); idx != -1 {
			imageTagOnly = tag[idx+1:]
		}

		fmt.Printf("Waiting for deployment %s to use image tag %s ...\n", component, imageTagOnly)

		// Poll loop
		for {
			// Trigger flux reconcile (optional but speeds up sync)
			// flux reconcile helmrelease <component> -n <namespace>
			_ = exec.Command("flux", "reconcile", "helmrelease", component, "-n", namespace).Run()

			// Check current image
			// kubectl -n <ns> get deployment <comp> -o jsonpath='{.spec.template.spec.containers[0].image}'
			out, err := exec.Command("kubectl", "-n", namespace, "get", "deployment", component, "-o", "jsonpath={.spec.template.spec.containers[0].image}").Output()
			if err == nil {
				currentImage := string(out)
				if strings.Contains(currentImage, imageTagOnly) || strings.Contains(currentImage, tag) {
					break
				}
			}

			time.Sleep(10 * time.Second)
		}

		fmt.Printf("Image matched. Waiting for rollout (timeout %s) ...\n", timeout)
		if err := util.RunCommand("kubectl", "-n", namespace, "rollout", "status", "deployment/"+component, "--timeout", timeout); err != nil {
			fmt.Fprintf(os.Stderr, "Rollout failed: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(watchCmd)
	watchCmd.Flags().String("component", "", "Deployment/HelmRelease name")
	watchCmd.Flags().String("environment", "", "Environment (dev, pp, prod)")
	watchCmd.Flags().String("namespace", "default", "Kubernetes namespace")
	watchCmd.Flags().String("timeout", "30m", "kubectl rollout status timeout")
	watchCmd.Flags().String("build-result-dir", "", "Directory containing build_result.json")
	_ = watchCmd.MarkFlagRequired("component")
	_ = watchCmd.MarkFlagRequired("environment")
}

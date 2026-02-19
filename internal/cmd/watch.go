package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
)

// watchPollInterval is the time between polling attempts.
// Reduced in tests to avoid sleeping.
var watchPollInterval = 10 * time.Second

// watchFluxReconcile and watchGetDeploymentImage are vars so tests can replace them.
var watchFluxReconcile = func(component, namespace string) {
	_ = exec.Command("flux", "reconcile", "helmrelease", component, "-n", namespace).Run()
}

var watchGetDeploymentImage = func(namespace, component string) (string, error) {
	out, err := exec.Command(
		"kubectl", "-n", namespace,
		"get", "deployment", component,
		"-o", "jsonpath={.spec.template.spec.containers[0].image}",
	).Output()
	return string(out), err
}

var watchCmd = &cobra.Command{
	Use:   "watch-deployment",
	Short: "Wait for Flux to update a deployment, then kubectl rollout status.",
	Long: `Polls until the named Deployment's image tag matches the one from
build_result.json, then runs kubectl rollout status to confirm the rollout
completes within the given timeout.

When skaffold.yaml defines multiple artifacts, use --image-name to select
which artifact's tag to watch for. Defaults to the last entry (application
image; base images come first by convention).`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		component, _ := cmd.Flags().GetString("component")
		env, _ := cmd.Flags().GetString("environment")
		namespace, _ := cmd.Flags().GetString("namespace")
		timeout, _ := cmd.Flags().GetString("timeout")
		buildResultDir, _ := cmd.Flags().GetString("build-result-dir")
		imageName, _ := cmd.Flags().GetString("image-name")
		pollTimeout, _ := cmd.Flags().GetDuration("poll-timeout")

		destRepo := util.GetWatchDestinationRepository(env)
		if destRepo == "" {
			return fmt.Errorf("could not resolve destination repository — set GOOGLE_GKE_IMAGE_* env vars")
		}

		res, err := util.ReadBuildResult(buildResultDir)
		if err != nil {
			return fmt.Errorf("reading build_result.json: %w", err)
		}

		fullRef, err := util.SelectTag(res, imageName)
		if err != nil {
			return fmt.Errorf("selecting image: %w", err)
		}

		// Extract the version tag from a fully-qualified ref:
		//   ghcr.io/octopilot/op:v1.0.0@sha256:abc123... → "v1.0.0"
		versionTag := extractVersionTag(fullRef)

		fmt.Fprintf(os.Stderr, "Watching deployment %s in namespace %s\n", component, namespace)
		fmt.Fprintf(os.Stderr, "Waiting for image tag: %s\n", versionTag)

		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()

		ticker := time.NewTicker(watchPollInterval)
		defer ticker.Stop()

		for {
			watchFluxReconcile(component, namespace)

			currentImage, err := watchGetDeploymentImage(namespace, component)
			if err == nil && currentImage != "" {
				if strings.Contains(currentImage, versionTag) || strings.Contains(currentImage, fullRef) {
					fmt.Fprintf(os.Stderr, "Image matched (%s). Running rollout status (timeout %s)...\n",
						currentImage, timeout)
					if err := util.RunCommand("kubectl", "-n", namespace, "rollout", "status",
						"deployment/"+component, "--timeout", timeout); err != nil {
						return fmt.Errorf("rollout failed: %w", err)
					}
					fmt.Println("Rollout complete.")
					return nil
				}
			}

			select {
			case <-ctx.Done():
				return fmt.Errorf("timed out (%s) waiting for deployment %s to use tag %s",
					pollTimeout, component, versionTag)
			case <-ticker.C:
			}
		}
	},
}

// extractVersionTag returns the version portion of a fully-qualified image ref.
//
//	"ghcr.io/org/image:v1.0.0@sha256:abc" → "v1.0.0"
//	"ghcr.io/org/image:v1.0.0"            → "v1.0.0"
//	"image:v1.0.0"                         → "v1.0.0"
//
// Falls back to returning the input unchanged if no tag separator is found.
func extractVersionTag(fullRef string) string {
	ref := fullRef
	if at := strings.Index(ref, "@"); at != -1 {
		ref = ref[:at]
	}
	if colon := strings.LastIndex(ref, ":"); colon != -1 {
		return ref[colon+1:]
	}
	return fullRef
}

func init() {
	rootCmd.AddCommand(watchCmd)
	watchCmd.Flags().String("component", "", "Deployment or HelmRelease name")
	watchCmd.Flags().String("environment", "", "Target environment (dev, pp, prod)")
	watchCmd.Flags().String("namespace", "default", "Kubernetes namespace")
	watchCmd.Flags().String("timeout", "30m", "kubectl rollout status timeout")
	watchCmd.Flags().String("build-result-dir", "", "Directory containing build_result.json (default: cwd)")
	watchCmd.Flags().String("image-name", "", "Artifact name to watch for (default: last entry in build_result.json)")
	watchCmd.Flags().Duration("poll-timeout", 10*time.Minute, "Maximum time to poll before failing")
	_ = watchCmd.MarkFlagRequired("component")
	_ = watchCmd.MarkFlagRequired("environment")
}

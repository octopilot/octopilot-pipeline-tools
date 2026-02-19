package cmd

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
)

// craneCopy is a var so it can be replaced in tests.
var craneCopy = func(src, dst string, opts ...crane.Option) error {
	return crane.Copy(src, dst, opts...)
}

var promoteCmd = &cobra.Command{
	Use:   "promote-image",
	Short: "Copy image from source to destination registry (using crane library).",
	Long: `Promote (copy) a container image from a source environment registry to a
destination registry without rebuilding. Reads the image reference from
build_result.json.

When skaffold.yaml defines multiple artifacts (e.g. a base image and an
application image), use --image-name to select which artifact to promote.
By default the last entry in build_result.json is used (the application
image; base images appear first by convention).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceEnv, _ := cmd.Flags().GetString("source")
		destEnv, _ := cmd.Flags().GetString("destination")
		buildResultDir, _ := cmd.Flags().GetString("build-result-dir")
		imageName, _ := cmd.Flags().GetString("image-name")

		srcRepo, destRepo := util.GetPromoteRepositories(sourceEnv, destEnv)
		if srcRepo == "" || destRepo == "" {
			return fmt.Errorf("could not resolve repositories — set GOOGLE_GKE_IMAGE_* env vars or config")
		}

		res, err := util.ReadBuildResult(buildResultDir)
		if err != nil {
			return fmt.Errorf("reading build_result.json: %w", err)
		}

		// Select the correct artifact (by name or last entry).
		fullRef, err := util.SelectTag(res, imageName)
		if err != nil {
			return fmt.Errorf("selecting image: %w", err)
		}

		// fullRef is the fully-qualified stored ref:
		//   ghcr.io/octopilot/op:v1.0.0@sha256:abc123...
		//
		// srcRef: use the stored ref directly (it already includes the source registry).
		// destRef: replace the source registry prefix with the destination prefix.
		srcRef := fullRef
		imageRelPath := fullRef
		if strings.HasPrefix(fullRef, srcRepo+"/") {
			imageRelPath = strings.TrimPrefix(fullRef, srcRepo+"/")
		}
		destRef := fmt.Sprintf("%s/%s", strings.TrimSuffix(destRepo, "/"), imageRelPath)

		fmt.Printf("Promoting %s\n     -> %s\n", srcRef, destRef)

		if err := craneCopy(srcRef, destRef); err != nil {
			return fmt.Errorf("promotion failed: %w", err)
		}

		fmt.Println("Promotion successful.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(promoteCmd)
	promoteCmd.Flags().String("source", "", "Source environment (dev, pp, prod)")
	promoteCmd.Flags().String("destination", "", "Destination environment (pp, prod)")
	promoteCmd.Flags().String("build-result-dir", "", "Directory containing build_result.json (default: cwd)")
	promoteCmd.Flags().String("image-name", "", "Artifact name to promote (default: last entry in build_result.json)")
	_ = promoteCmd.MarkFlagRequired("source")
	_ = promoteCmd.MarkFlagRequired("destination")
}

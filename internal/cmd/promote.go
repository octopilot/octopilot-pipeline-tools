package cmd

import (
	"fmt"
	"os"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
)

var promoteCmd = &cobra.Command{
	Use:   "promote-image",
	Short: "Copy image from source to destination registry (using crane library).",
	Run: func(cmd *cobra.Command, args []string) {
		sourceEnv, _ := cmd.Flags().GetString("source")
		destEnv, _ := cmd.Flags().GetString("destination")
		buildResultDir, _ := cmd.Flags().GetString("build-result-dir")

		srcRepo, destRepo := util.GetPromoteRepositories(sourceEnv, destEnv)
		if srcRepo == "" || destRepo == "" {
			fmt.Fprintln(os.Stderr, "Error: Could not resolve repositories. Set GOOGLE_GKE_IMAGE_* env vars.")
			os.Exit(1)
		}

		// Read tag from build_result.json
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

		srcRef := fmt.Sprintf("%s/%s", srcRepo, tag)
		destRef := fmt.Sprintf("%s/%s", destRepo, tag)

		fmt.Printf("Promoting %s -> %s\n", srcRef, destRef)

		// Use crane library
		// crane.Copy(src, dest, options...)
		// We might need authentication options if not using standard docker config.
		// crane uses ~/.docker/config.json by default which is what we want.

		if err := crane.Copy(srcRef, destRef); err != nil {
			fmt.Fprintf(os.Stderr, "Promotion failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Promotion successful.")
	},
}

func init() {
	rootCmd.AddCommand(promoteCmd)
	promoteCmd.Flags().String("source", "", "Source environment (dev, pp, prod)")
	promoteCmd.Flags().String("destination", "", "Destination environment (pp, prod)")
	promoteCmd.Flags().String("build-result-dir", "", "Directory containing build_result.json")
	promoteCmd.MarkFlagRequired("source")
	promoteCmd.MarkFlagRequired("destination")
}

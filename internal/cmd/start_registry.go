package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/octopilot/octopilot-pipeline-tools/internal/util"
	"github.com/spf13/cobra"
)

var startRegistryCmd = &cobra.Command{
	Use:   "start-registry",
	Short: "Start local TLS registry.",
	Long:  `Starts a local Docker registry with TLS on 5001. Generates certs and configures trust.`,
	Run: func(cmd *cobra.Command, args []string) {
		trust, _ := cmd.Flags().GetBool("trust")
		image, _ := cmd.Flags().GetString("image")

		// 1. Setup Data Directory
		home, _ := os.UserHomeDir()
		baseDir := filepath.Join(home, ".octopilot", "registry")
		certDir := filepath.Join(baseDir, "certs")

		fmt.Printf("Registry setup at %s\n", baseDir)

		// 2. Generate Certs if missing
		tlsCrt := filepath.Join(certDir, "tls.crt")
		tlsKey := filepath.Join(certDir, "tls.key")
		_, errCrt := os.Stat(tlsCrt)
		_, errKey := os.Stat(tlsKey)
		if os.IsNotExist(errCrt) || os.IsNotExist(errKey) {
			fmt.Println("Generating new self-signed certificates...")
			if err := util.GenerateCerts(certDir); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to generate certs: %v\n", err)
				os.Exit(1)
			}
		}

		// 3. Trust Certs
		if trust {
			if err := util.TrustCert(tlsCrt); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to trust cert: %v\n", err)
				// Don't exit, might still work for untrusted usage
			}

			if runtime.GOOS == "darwin" {
				ports := []string{"localhost:5001", "host.docker.internal:5001", "registry.local:5001"}
				if err := util.InstallCertTrustColima(tlsCrt, ports); err == nil {
					fmt.Println("Cert installed in Colima. You may need to restart Colima ('colima restart') if not automated.")
				} else {
					fmt.Printf("Colima trust skipped or failed (is colima running?): %v\n", err)
				}
			}
		}

		// 4. Start Docker Container
		exec.Command("docker", "rm", "-f", "octopilot-registry").Run()

		fmt.Printf("Starting registry container %s...\n", image)
		runArgs := []string{
			"run", "-d",
			"--name", "octopilot-registry",
			"--restart", "unless-stopped",
			"-p", "5001:5000",
			"-v", fmt.Sprintf("%s:/certs", certDir),
			"-v", "octopilot-registry-data:/var/lib/registry",
			"-e", "REGISTRY_HTTP_TLS_CERTIFICATE=/certs/tls.crt",
			"-e", "REGISTRY_HTTP_TLS_KEY=/certs/tls.key",
			image,
		}

		runCmd := exec.Command("docker", runArgs...)
		runCmd.Stdout = os.Stdout
		runCmd.Stderr = os.Stderr
		if err := runCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start registry: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Registry started at https://localhost:5001")
	},
}

func init() {
	rootCmd.AddCommand(startRegistryCmd)
	startRegistryCmd.Flags().Bool("trust", false, "Trust the generated certificate on the host")
	startRegistryCmd.Flags().String("image", "registry:2", "Registry image to use")
}

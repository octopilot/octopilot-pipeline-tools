package util

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// InstallCertTrustColima installs the cert into the Colima VM's Docker trust store.
// ports: e.g. "localhost:5001", "host.docker.internal:5001", "registry.local:5001"
func InstallCertTrustColima(certPath string, ports []string) error {
	// 1. Check Colima status
	if err := exec.Command("colima", "status").Run(); err != nil {
		return fmt.Errorf("colima is not running or not found")
	}

	// 2. Read Cert
	certContent, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}

	// 3. Construct Script
	// mkdir -p /etc/docker/certs.d/host:port...
	// cat > /tmp/registry-ca.crt
	// cp ...

	if len(ports) == 0 {
		return nil
	}

	var dirs []string
	for _, p := range ports {
		dirs = append(dirs, fmt.Sprintf("/etc/docker/certs.d/%s", p))
	}
	mkdirCmd := fmt.Sprintf("mkdir -p %s", strings.Join(dirs, " "))

	cpCmds := []string{}
	for _, p := range ports {
		cpCmds = append(cpCmds, fmt.Sprintf("cp /tmp/registry-ca.crt /etc/docker/certs.d/%s/ca.crt", p))
	}

	fullScript := fmt.Sprintf("%s && cat > /tmp/registry-ca.crt && %s", mkdirCmd, strings.Join(cpCmds, " && "))

	fmt.Println("Installing certificate into Colima VM...")
	cmd := exec.Command("colima", "ssh", "--", "sudo", "sh", "-c", fullScript)
	cmd.Stdin = strings.NewReader(string(certContent))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install cert in Colima: %w", err)
	}

	return nil
}

func RestartColima() error {
	fmt.Println("Restarting Colima to apply trust settings...")
	cmd := exec.Command("colima", "restart")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

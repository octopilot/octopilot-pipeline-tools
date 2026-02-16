//go:build darwin

package util

import (
	"fmt"
	"os"
	"os/exec"
)

func trustCertImpl(certPath string) error {
	// sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain <cert>
	// or user keychain if we want to avoid sudo? Python script preferred System keychain.

	cmd := exec.Command("sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", certPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	fmt.Println("Adding certificate to macOS System Keychain (may prompt for sudo)...")
	return cmd.Run()
}

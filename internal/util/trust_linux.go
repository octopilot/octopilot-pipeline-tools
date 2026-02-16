//go:build linux

package util

import (
	"fmt"
	"os"
	"os/exec"
)

func trustCertImpl(certPath string) error {
	// cp to /usr/local/share/ca-certificates/registry-tls-localhost.crt
	dest := "/usr/local/share/ca-certificates/registry-tls-localhost.crt"

	fmt.Printf("Copying cert to %s (sudo)...\n", dest)
	cmdCp := exec.Command("sudo", "cp", certPath, dest)
	cmdCp.Stdout = os.Stdout
	cmdCp.Stderr = os.Stderr
	cmdCp.Stdin = os.Stdin
	if err := cmdCp.Run(); err != nil {
		return err
	}

	fmt.Println("Updating CA certificates...")
	cmdUpdate := exec.Command("sudo", "update-ca-certificates")
	cmdUpdate.Stdout = os.Stdout
	cmdUpdate.Stderr = os.Stderr
	cmdUpdate.Stdin = os.Stdin
	return cmdUpdate.Run()
}

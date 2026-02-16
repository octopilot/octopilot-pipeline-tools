//go:build !darwin && !linux

package util

func trustCertImpl(certPath string) error {
	return trustCertUnsupported(certPath)
}

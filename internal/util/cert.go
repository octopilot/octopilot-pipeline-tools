package util

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// GenerateCerts generates a self-signed CA and a server certificate.
// It writes ca.crt, tls.crt, and tls.key to the specified directory.
func GenerateCerts(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 1. Generate CA
	caPriv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}

	caTpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "Octopilot Registry CA",
			Organization: []string{"Octopilot"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, &caTpl, &caTpl, &caPriv.PublicKey, caPriv)
	if err != nil {
		return err
	}

	// Write CA struct to file (optional, but good for debugging/trusting explicitly)
	// We'll trust the CA or the Leaf? Usually trusting CA is better.
	// But for simplicity, existing python tool trusted the leaf?
	// Python tool: "Install cert for system trust". It used `tls.crt`.
	// We will write ca.crt anyway.

	// 2. Generate Server Cert
	servPriv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}

	servTpl := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"Octopilot"},
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		DNSNames:     []string{"localhost", "host.docker.internal", "registry.local"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	servBytes, err := x509.CreateCertificate(rand.Reader, &servTpl, &caTpl, &servPriv.PublicKey, caPriv)
	if err != nil {
		return err
	}

	// Write files
	if err := writePem(filepath.Join(dir, "ca.crt"), "CERTIFICATE", caBytes); err != nil {
		return err
	}
	if err := writePem(filepath.Join(dir, "tls.crt"), "CERTIFICATE", servBytes); err != nil {
		return err
	}
	if err := writePem(filepath.Join(dir, "tls.key"), "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(servPriv)); err != nil {
		return err
	}

	return nil
}

func writePem(path, type_ string, bytes []byte) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return pem.Encode(out, &pem.Block{Type: type_, Bytes: bytes})
}

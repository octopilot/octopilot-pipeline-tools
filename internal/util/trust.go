package util

// TrustCert installs the certificate at certPath into the system trust store.
// Implementation is platform specific.
func TrustCert(certPath string) error {
	return trustCertImpl(certPath)
}

// IsTrusted checks if the cert is already trusted (simplified check).
// Implementation detail: checking sentinel file or keychain integration.
// For now, we'll just check if the trust was attempted recently via a sentinel?
// The Python tool used a sentinel file .system-trust-installed with fingerprint.
// We can implement similar logic later if strictly needed to avoid sudo prompts.
// For MVP, we'll just attempt trust (sudo might prompt).
func IsTrusted(certPath string) bool {
	// TODO: Implement idempotency check
	return false
}

// Fallback for unsupported platforms

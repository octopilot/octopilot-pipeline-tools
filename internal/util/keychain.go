package util

import (
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/google"
)

// Keychain is how op and pack authenticate to registries: the docker config first (DOCKER_CONFIG, including
// credential helpers on PATH), then Google credentials for gcr.io and *.pkg.dev (Workload Identity / metadata
// server, GOOGLE_APPLICATION_CREDENTIALS, gcloud). The second makes Artifact Registry usable from the op container,
// where the runner's docker-credential-gcr helper does not exist; other registries are unaffected (anonymous there).
var Keychain authn.Keychain = fallbackKeychain{authn.DefaultKeychain, google.Keychain}

// fallbackKeychain returns the first non-anonymous credential. Unlike authn.NewMultiKeychain, an error from one
// keychain does not stop the search: a docker config naming a credential helper that is not installed (the runner's
// config copied into the op container) must not hide Google credentials that work. The first error is returned only
// when no keychain produced a credential.
type fallbackKeychain []authn.Keychain

func (k fallbackKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	var firstErr error
	for _, kc := range k {
		auth, err := kc.Resolve(target)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if auth != authn.Anonymous {
			return auth, nil
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return authn.Anonymous, nil
}

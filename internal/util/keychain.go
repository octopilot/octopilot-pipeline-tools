package util

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/google"
)

// Keychain is how op and pack authenticate to registries: the docker config first (DOCKER_CONFIG, including
// credential helpers on PATH), then Google credentials for gcr.io and *.pkg.dev (Workload Identity / metadata
// server, GOOGLE_APPLICATION_CREDENTIALS, gcloud). The second makes Artifact Registry usable from the op container,
// where the runner's docker-credential-gcr helper does not exist; other registries are unaffected (anonymous there).
//
// With GOOGLE_APPLICATION_CREDENTIALS set, Google credentials come FIRST for Google registries. A pipeline sets it to a
// Workload Identity Federation credential configuration (external_account) that mints a fresh access token whenever
// the last one expires, while the docker config holds the static `docker login -u oauth2accesstoken` token from the
// start of the job, which Google stops accepting after its lifetime (1 hour): a suite fan-out still pushing an hour
// in got 401 on every request. Non-Google registries resolve to anonymous in the Google keychain, so they still use
// the docker config.
var Keychain authn.Keychain = keychainFor(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))

func keychainFor(googleCredentials string) authn.Keychain {
	if strings.TrimSpace(googleCredentials) != "" {
		return fallbackKeychain{google.Keychain, authn.DefaultKeychain}
	}
	return fallbackKeychain{authn.DefaultKeychain, google.Keychain}
}

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

// newGoogleEnvAuthenticator is google.NewEnvAuthenticator; a variable for tests.
var newGoogleEnvAuthenticator = google.NewEnvAuthenticator

// ReportGoogleCredentials says, when GOOGLE_APPLICATION_CREDENTIALS is set, whether a token can be minted from it.
// Keychain falls back to the docker config silently when it cannot, which would hide a broken credential
// configuration until the docker login's token expired an hour later. Never prints the token.
func ReportGoogleCredentials(ctx context.Context) {
	path := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
	if path == "" {
		return
	}
	kind := "unreadable"
	if raw, err := os.ReadFile(path); err == nil {
		var f struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &f) == nil && f.Type != "" {
			kind = f.Type
		}
	}
	auth, err := newGoogleEnvAuthenticator(ctx)
	if err == nil {
		_, err = auth.Authorization()
	}
	if err != nil {
		fmt.Printf("warning: registry auth: GOOGLE_APPLICATION_CREDENTIALS (%s) gives no token: %v; Google registries fall back to the docker config\n", kind, err)
		return
	}
	fmt.Printf("Registry auth: Google registries use GOOGLE_APPLICATION_CREDENTIALS (%s): token minted, refreshed when it expires\n", kind)
}

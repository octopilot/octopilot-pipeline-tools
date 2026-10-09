package util

import (
	"errors"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

type fakeKeychain struct {
	auth authn.Authenticator
	err  error
}

func (f fakeKeychain) Resolve(authn.Resource) (authn.Authenticator, error) { return f.auth, f.err }

func TestFallbackKeychain(t *testing.T) {
	target, _ := name.NewRegistry("us-docker.pkg.dev")
	token := &authn.Bearer{Token: "t"}
	broken := fakeKeychain{err: errors.New("docker-credential-gcr: executable file not found")}
	anon := fakeKeychain{auth: authn.Anonymous}
	good := fakeKeychain{auth: token}

	if a, err := (fallbackKeychain{broken, good}).Resolve(target); err != nil || a != token {
		t.Errorf("missing helper then Google creds: got %v, %v; want the Google credential", a, err)
	}
	if a, err := (fallbackKeychain{good, broken}).Resolve(target); err != nil || a != token {
		t.Errorf("first credential wins: got %v, %v", a, err)
	}
	if a, err := (fallbackKeychain{anon, anon}).Resolve(target); err != nil || a != authn.Anonymous {
		t.Errorf("nothing configured: got %v, %v; want anonymous", a, err)
	}
	if _, err := (fallbackKeychain{broken, anon}).Resolve(target); err == nil {
		t.Errorf("no credential anywhere and a broken helper: want the helper's error")
	}
}

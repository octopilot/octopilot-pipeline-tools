package util

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestKeychainOrderFollowsGoogleApplicationCredentials(t *testing.T) {
	kc, ok := keychainFor("").(fallbackKeychain)
	if !ok || len(kc) != 2 || kc[0] != authn.DefaultKeychain {
		t.Errorf("no GOOGLE_APPLICATION_CREDENTIALS: want the docker config first, got %#v", kc)
	}
	kc, ok = keychainFor("/tmp/gcp-credentials.json").(fallbackKeychain)
	if !ok || len(kc) != 2 || kc[1] != authn.DefaultKeychain {
		t.Errorf("GOOGLE_APPLICATION_CREDENTIALS set: want Google credentials first, got %#v", kc)
	}
}

func TestReportGoogleCredentials(t *testing.T) {
	old := newGoogleEnvAuthenticator
	t.Cleanup(func() { newGoogleEnvAuthenticator = old })
	calls := 0
	newGoogleEnvAuthenticator = func(context.Context) (authn.Authenticator, error) {
		calls++
		return nil, errors.New("sts: invalid_grant")
	}

	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	ReportGoogleCredentials(context.Background())
	if calls != 0 {
		t.Fatalf("no GOOGLE_APPLICATION_CREDENTIALS: no token should be minted")
	}

	path := filepath.Join(t.TempDir(), "creds.json")
	if err := os.WriteFile(path, []byte(`{"type":"external_account"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	ReportGoogleCredentials(context.Background()) // a failure is reported, not fatal
	if calls != 1 {
		t.Fatalf("want one token attempt, got %d", calls)
	}
}

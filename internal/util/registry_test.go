package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeRegistryFile(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, RegistryFilename), []byte(content), 0o644))
}

func TestInterpolate(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		input string
		want  string
	}{
		{
			name:  "plain ${VAR} set",
			env:   map[string]string{"MY_ORG": "acme"},
			input: "ghcr.io/${MY_ORG}/app",
			want:  "ghcr.io/acme/app",
		},
		{
			name:  "$VAR set",
			env:   map[string]string{"MY_ORG": "acme"},
			input: "ghcr.io/$MY_ORG/app",
			want:  "ghcr.io/acme/app",
		},
		{
			name:  "${VAR:-default} uses default when unset",
			env:   map[string]string{},
			input: "ghcr.io/${MY_ORG:-my-fallback}/app",
			want:  "ghcr.io/my-fallback/app",
		},
		{
			name:  "${VAR:-default} uses var when set",
			env:   map[string]string{"MY_ORG": "real-org"},
			input: "ghcr.io/${MY_ORG:-my-fallback}/app",
			want:  "ghcr.io/real-org/app",
		},
		{
			name:  "trailing slash trimmed",
			env:   map[string]string{},
			input: "ghcr.io/org/",
			want:  "ghcr.io/org",
		},
		{
			name:  "no interpolation",
			env:   map[string]string{},
			input: "localhost:5001",
			want:  "localhost:5001",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			assert.Equal(t, tc.want, interpolate(tc.input))
		})
	}
}

func TestGetDefaultRepoFromRegistry_Local(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "")
	writeRegistryFile(t, dir, "local: localhost:5001\nci:\n  - ghcr.io/myorg\n")
	assert.Equal(t, "localhost:5001", GetDefaultRepoFromRegistry(dir))
}

func TestGetDefaultRepoFromRegistry_CI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("MY_ORG", "acme")
	writeRegistryFile(t, dir, "ci:\n  - ghcr.io/${MY_ORG:-fallback}\n")
	assert.Equal(t, "ghcr.io/acme", GetDefaultRepoFromRegistry(dir))
}

func TestGetDefaultRepoFromRegistry_CIDefaultFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("MY_ORG", "")
	writeRegistryFile(t, dir, "ci:\n  - ghcr.io/${MY_ORG:-fallback-org}\n")
	assert.Equal(t, "ghcr.io/fallback-org", GetDefaultRepoFromRegistry(dir))
}

func TestGetDefaultRepoFromRegistry_Missing(t *testing.T) {
	assert.Equal(t, "", GetDefaultRepoFromRegistry(t.TempDir()))
}

func TestGetDefaultRepoFromRegistry_LegacyDestinations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GITHUB_ACTIONS", "true")
	writeRegistryFile(t, dir, "destinations:\n  - ghcr.io/legacy-org\n")
	assert.Equal(t, "ghcr.io/legacy-org", GetDefaultRepoFromRegistry(dir))
}

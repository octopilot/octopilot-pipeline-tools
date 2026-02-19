package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveDefaultRepo_FromEnv(t *testing.T) {
	t.Setenv("SKAFFOLD_DEFAULT_REPO", "ghcr.io/env-org")
	assert.Equal(t, "ghcr.io/env-org", ResolveDefaultRepo(t.TempDir()))
}

func TestResolveDefaultRepo_FromRegistryFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SKAFFOLD_DEFAULT_REPO", "")
	t.Setenv("GITHUB_ACTIONS", "")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".registry"), []byte("local: localhost:5001\n"), 0o644))
	assert.Equal(t, "localhost:5001", ResolveDefaultRepo(dir))
}

func TestResolveDefaultRepo_Fallback(t *testing.T) {
	t.Setenv("SKAFFOLD_DEFAULT_REPO", "")
	assert.Equal(t, "localhost:5001", ResolveDefaultRepo(t.TempDir()))
}

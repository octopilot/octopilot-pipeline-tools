package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsValidHelmChartRef(t *testing.T) {
	assert.False(t, isValidHelmChartRef("ttl.sh/158128ecbbfa428c879b748d9c92e31d-chart:0.1.0@sha256:abc"))
	assert.False(t, isValidHelmChartRef("ttl.sh/uuid-chart:1d"))
	assert.True(t, isValidHelmChartRef("ttl.sh/uuid-chart/igniteflux:0.1.0@sha256:abc"))
	assert.True(t, isValidHelmChartRef("ghcr.io/octopilot/igniteflux:0.1.0"))
}

func TestReadChartRef_RewritesHelmPrefix(t *testing.T) {
	dir := t.TempDir()
	chart := filepath.Join(dir, "chart")
	require.NoError(t, os.MkdirAll(chart, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("name: igniteflux\nversion: 0.1.0\n"), 0o644))
	out := t.TempDir()
	// What helm 0.1.3 writes: the prefix plus the chart version, missing /<chart name>.
	require.NoError(t, os.WriteFile(filepath.Join(out, "ref"), []byte("ttl.sh/uuid-chart:0.1.0@sha256:deadbeef\n"), 0o644))

	got, err := readChartRef(out, "ttl.sh/uuid-chart:1d", chart, "ghcr.io/octopilot/igniteflux-chart")
	require.NoError(t, err)
	assert.Equal(t, "ttl.sh/uuid-chart/igniteflux:0.1.0@sha256:deadbeef", got)
}

func TestReadChartRef_KeepsChartPath(t *testing.T) {
	dir := t.TempDir()
	chart := filepath.Join(dir, "chart")
	require.NoError(t, os.MkdirAll(chart, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("name: igniteflux\nversion: 0.1.0\n"), 0o644))
	out := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(out, "ref"), []byte("ttl.sh/uuid-chart/igniteflux:0.1.0@sha256:deadbeef\n"), 0o644))

	got, err := readChartRef(out, "ttl.sh/uuid-chart:1d", chart, "ghcr.io/octopilot/igniteflux-chart")
	require.NoError(t, err)
	assert.Equal(t, "ttl.sh/uuid-chart/igniteflux:0.1.0@sha256:deadbeef", got)
}

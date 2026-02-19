package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferRunOptions_Procfile_PortDefault(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Procfile"),
		[]byte("web: uvicorn main:app --host 0.0.0.0 --port ${PORT:-3000}\n"), 0o644))

	opts := InferRunOptions(dir)
	assert.Equal(t, 3000, opts.ContainerPort)
	assert.Equal(t, "3000", opts.Env["PORT"])
}

func TestInferRunOptions_Procfile_FlagPort(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Procfile"),
		[]byte("web: node server.js --port 4000\n"), 0o644))

	opts := InferRunOptions(dir)
	assert.Equal(t, 4000, opts.ContainerPort)
}

func TestInferRunOptions_Dockerfile_Expose(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM ubuntu:jammy\nEXPOSE 9090\n"), 0o644))

	opts := InferRunOptions(dir)
	assert.Equal(t, 9090, opts.ContainerPort)
}

func TestInferRunOptions_NginxConf(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nginx.conf"),
		[]byte("server { listen 8888; }\n"), 0o644))

	opts := InferRunOptions(dir)
	assert.Equal(t, 8888, opts.ContainerPort)
}

func TestInferRunOptions_ProjectToml_Default(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.toml"),
		[]byte("[build]\n"), 0o644))

	opts := InferRunOptions(dir)
	assert.Equal(t, DefaultContainerPort, opts.ContainerPort)
}

func TestInferRunOptions_NoFiles_Default(t *testing.T) {
	opts := InferRunOptions(t.TempDir())
	assert.Equal(t, DefaultContainerPort, opts.ContainerPort)
	assert.Equal(t, "8080", opts.Env["PORT"])
}

func TestInferRunOptions_ProcfileNonWebLine(t *testing.T) {
	dir := t.TempDir()
	// No "web:" line; first line is "worker:"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Procfile"),
		[]byte("worker: python worker.py --port 5555\n"), 0o644))

	opts := InferRunOptions(dir)
	// Falls back to first line's port
	assert.Equal(t, 5555, opts.ContainerPort)
}

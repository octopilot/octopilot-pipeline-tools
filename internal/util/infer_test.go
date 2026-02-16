package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInferRunOptions(t *testing.T) {
	tmpDir := t.TempDir()

	// Case 1: Procfile
	err := os.WriteFile(filepath.Join(tmpDir, "Procfile"), []byte("web: java -Dserver.port=${PORT:-8081} -jar app.jar"), 0644)
	assert.NoError(t, err)

	opts := InferRunOptions(tmpDir)
	assert.Equal(t, 8081, opts.ContainerPort)
	assert.Equal(t, "8081", opts.Env["PORT"])

	// Case 2: Dockerfile EXPOSE
	tmpDir2 := t.TempDir()
	err = os.WriteFile(filepath.Join(tmpDir2, "Dockerfile"), []byte("FROM alpine\nEXPOSE 9090"), 0644)
	assert.NoError(t, err)

	opts2 := InferRunOptions(tmpDir2)
	assert.Equal(t, 9090, opts2.ContainerPort)
}

func TestInferRunOptionsUnknown(t *testing.T) {
	tmpDir := t.TempDir()
	opts := InferRunOptions(tmpDir)
	// Defaults
	assert.Equal(t, 8080, opts.ContainerPort)
	assert.Equal(t, "8080", opts.Env["PORT"])
}

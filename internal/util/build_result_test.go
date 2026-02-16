package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadBuildResult(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "build_result.json")

	// Case 1: Valid JSON with string tag
	err := os.WriteFile(path, []byte(`{"builds": [{"tag": "my-image:v1"}]}`), 0644)
	assert.NoError(t, err)

	res, err := ReadBuildResult(tmpDir)
	assert.NoError(t, err)
	tag, err := GetFirstTag(res)
	assert.NoError(t, err)
	assert.Equal(t, "my-image:v1", tag)

	// Case 2: Valid JSON with simple string
	err = os.WriteFile(path, []byte(`{"builds": ["simple-tag"]}`), 0644)
	assert.NoError(t, err)

	res, err = ReadBuildResult(tmpDir)
	assert.NoError(t, err)
	tag, err = GetFirstTag(res)
	assert.NoError(t, err)
	assert.Equal(t, "simple-tag", tag)

	// Case 3: Missing file
	_, err = ReadBuildResult(filepath.Join(tmpDir, "missing"))
	assert.Error(t, err)
}

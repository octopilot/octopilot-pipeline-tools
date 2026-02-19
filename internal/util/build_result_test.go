package util

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeBuildResultFixture(t *testing.T, dir string, builds []BuildEntry) {
	t.Helper()
	br := BuildResult{Builds: builds}
	data, err := json.Marshal(br)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, BuildResultFilename), data, 0o644))
}

func TestReadBuildResult(t *testing.T) {
	dir := t.TempDir()
	builds := []BuildEntry{
		{ImageName: "op-base", Tag: "ghcr.io/org/op-base:v1@sha256:aaa"},
		{ImageName: "op", Tag: "ghcr.io/org/op:v1@sha256:bbb"},
	}
	writeBuildResultFixture(t, dir, builds)

	res, err := ReadBuildResult(dir)
	require.NoError(t, err)
	assert.Len(t, res.Builds, 2)
	assert.Equal(t, "op-base", res.Builds[0].ImageName)
	assert.Equal(t, "op", res.Builds[1].ImageName)
}

func TestReadBuildResult_Empty(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, BuildResultFilename),
		[]byte(`{"builds":[]}`), 0o644))
	_, err := ReadBuildResult(dir)
	assert.ErrorContains(t, err, "no builds found")
}

func TestReadBuildResult_Missing(t *testing.T) {
	_, err := ReadBuildResult(t.TempDir())
	assert.Error(t, err)
}

func TestGetFirstTag(t *testing.T) {
	res := &BuildResult{Builds: []BuildEntry{
		{ImageName: "op-base", Tag: "ghcr.io/org/op-base:v1@sha256:aaa"},
		{ImageName: "op", Tag: "ghcr.io/org/op:v1@sha256:bbb"},
	}}
	tag, err := GetFirstTag(res)
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/org/op-base:v1@sha256:aaa", tag)
}

func TestGetFirstTag_Empty(t *testing.T) {
	_, err := GetFirstTag(&BuildResult{})
	assert.Error(t, err)
}

func TestGetTagForImage(t *testing.T) {
	res := &BuildResult{Builds: []BuildEntry{
		{ImageName: "op-base", Tag: "ghcr.io/org/op-base:v1@sha256:aaa"},
		{ImageName: "op", Tag: "ghcr.io/org/op:v1@sha256:bbb"},
	}}

	tag, err := GetTagForImage(res, "op")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/org/op:v1@sha256:bbb", tag)

	tag, err = GetTagForImage(res, "op-base")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/org/op-base:v1@sha256:aaa", tag)
}

func TestGetTagForImage_NotFound(t *testing.T) {
	res := &BuildResult{Builds: []BuildEntry{{ImageName: "op", Tag: "ghcr.io/org/op:v1@sha256:bbb"}}}
	_, err := GetTagForImage(res, "missing")
	assert.ErrorContains(t, err, "missing")
	assert.ErrorContains(t, err, "op")
}

func TestSelectTag_ByName(t *testing.T) {
	res := &BuildResult{Builds: []BuildEntry{
		{ImageName: "op-base", Tag: "ghcr.io/org/op-base:v1@sha256:aaa"},
		{ImageName: "op", Tag: "ghcr.io/org/op:v1@sha256:bbb"},
	}}
	tag, err := SelectTag(res, "op")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/org/op:v1@sha256:bbb", tag)
}

func TestSelectTag_DefaultsToLast(t *testing.T) {
	res := &BuildResult{Builds: []BuildEntry{
		{ImageName: "op-base", Tag: "ghcr.io/org/op-base:v1@sha256:aaa"},
		{ImageName: "op", Tag: "ghcr.io/org/op:v1@sha256:bbb"},
	}}
	// Empty imageName → last entry
	tag, err := SelectTag(res, "")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/org/op:v1@sha256:bbb", tag)
}

func TestSelectTag_SingleArtifact(t *testing.T) {
	res := &BuildResult{Builds: []BuildEntry{
		{ImageName: "my-app", Tag: "ghcr.io/org/my-app:v2@sha256:ccc"},
	}}
	tag, err := SelectTag(res, "")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/org/my-app:v2@sha256:ccc", tag)
}

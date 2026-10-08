package cmd

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// chartDockerConfigDir is where the chart container finds registry credentials. helm push reads docker
// credentials through DOCKER_CONFIG, so pushes to registries that need auth (e.g. Artifact Registry) work.
const chartDockerConfigDir = "/tmp/.docker"

// hostDockerConfigPath is the docker config op itself uses: $DOCKER_CONFIG/config.json, else ~/.docker/config.json.
func hostDockerConfigPath() string {
	if d := os.Getenv("DOCKER_CONFIG"); d != "" {
		return filepath.Join(d, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker", "config.json")
}

// chartAuthConfig returns a docker config holding only the inline "auths" of raw. Credential helpers
// (credsStore, credHelpers) are dropped: their binaries do not exist in the builder image, and helm
// would fail calling them instead of falling back. Returns nil when there are no inline auths.
func chartAuthConfig(raw []byte) []byte {
	var cfg struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Auths) == 0 {
		return nil
	}
	out, err := json.Marshal(map[string]any{"auths": cfg.Auths})
	if err != nil {
		return nil
	}
	return out
}

// chartAuthTar packs config as .docker/config.json for CopyToContainer at /tmp. Mode 0644 so the
// buildpack's user (not root in most builders) can read it.
func chartAuthTar(config []byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: ".docker/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		return nil, err
	}
	if err := tw.WriteHeader(&tar.Header{Name: ".docker/config.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(config))}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(config); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

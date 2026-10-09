package cmd

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
)

func TestChartAuthConfigKeepsOnlyInlineAuths(t *testing.T) {
	raw := []byte(`{"auths":{"us-docker.pkg.dev":{"auth":"b2F1dGgyYWNjZXNzdG9rZW46dG9r"}},"credsStore":"desktop","credHelpers":{"gcr.io":"gcloud"}}`)
	out := chartAuthConfig(raw)
	if out == nil {
		t.Fatal("expected a config")
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["auths"] == nil {
		t.Fatalf("want only auths, got %s", out)
	}
}

func TestChartAuthConfigNoAuths(t *testing.T) {
	for _, raw := range []string{``, `not json`, `{}`, `{"credsStore":"desktop"}`, `{"auths":{}}`} {
		if out := chartAuthConfig([]byte(raw)); out != nil {
			t.Errorf("%q: want nil, got %s", raw, out)
		}
	}
}

func TestChartAuthTar(t *testing.T) {
	cfg := []byte(`{"auths":{}}`)
	b, err := chartAuthTar(cfg)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(b))
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
		if h.Name == ".docker/config.json" {
			if h.Mode != 0o644 {
				t.Errorf("mode %o", h.Mode)
			}
			body, _ := io.ReadAll(tr)
			if !bytes.Equal(body, cfg) {
				t.Errorf("body %s", body)
			}
		}
	}
	if len(names) != 2 || names[1] != ".docker/config.json" {
		t.Fatalf("entries %v", names)
	}
}

func TestHostDockerConfigPath(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", "/tmp/x")
	if got := hostDockerConfigPath(); got != filepath.Join("/tmp/x", "config.json") {
		t.Fatalf("got %s", got)
	}
}

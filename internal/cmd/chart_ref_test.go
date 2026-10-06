package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsValidHelmChartRef(t *testing.T) {
	const digest = "@sha256:d82b8e151a11b2614daeb638d331e0aa724fa7c4563739cc7905d1ca1bd552d5"
	cases := []struct {
		ref  string
		want bool
	}{
		{"ttl.sh/uuid-chart/igniteflux:0.1.0" + digest, true},
		{"ghcr.io/octopilot/igniteflux-chart/igniteflux:0.1.0", true},
		{"localhost:5001/charts/igniteflux:0.1.0", true},
		// image-style refs: a slash and a tag, but not the repository helm pushed to
		{"ttl.sh/uuid-chart:0.1.0" + digest, false},
		{"ghcr.io/octopilot/igniteflux-chart:0.1.0", false},
		{"localhost:5001/igniteflux-chart:0.1.0", false},
		{"igniteflux:0.1.0", false},
	}
	for _, c := range cases {
		if got := isValidHelmChartRef(c.ref, "igniteflux"); got != c.want {
			t.Errorf("isValidHelmChartRef(%q) = %v, want %v", c.ref, got, c.want)
		}
	}
}

func TestReadChartRefNormalisesImageStyleRef(t *testing.T) {
	const digest = "sha256:d82b8e151a11b2614daeb638d331e0aa724fa7c4563739cc7905d1ca1bd552d5"
	ws, out := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "Chart.yaml"), []byte("apiVersion: v2\nname: igniteflux\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "ref"), []byte("ttl.sh/uuid-chart:0.1.0@"+digest+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readChartRef(out, "ttl.sh/uuid-chart:0.1.0", ws, "chart")
	if err != nil {
		t.Fatal(err)
	}
	if want := "ttl.sh/uuid-chart/igniteflux:0.1.0@" + digest; got != want {
		t.Errorf("readChartRef = %q, want %q", got, want)
	}
}

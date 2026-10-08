package util

import "testing"

func TestImageSourceUnsetKeepsReferences(t *testing.T) {
	s := NewImageSource("", nil)
	for _, ref := range []string{"ghcr.io/octopilot/builder-jammy-base:x", "ubuntu:jammy", ""} {
		if got := s.Resolve(ref); got != ref {
			t.Errorf("Resolve(%q) = %q, want unchanged", ref, got)
		}
	}
}

func TestImageSourceRewritesUpstreamImages(t *testing.T) {
	const digest = "sha256:d82b8e151a11b2614daeb638d331e0aa724fa7c4563739cc7905d1ca1bd552d5"
	s := NewImageSource("us-docker.pkg.dev/pw-ctl/ci/", []string{
		"ghcr.io/octopilot/igniteflux", "ghcr.io/octopilot/igniteflux-base", "ghcr.io/octopilot/igniteflux-chart",
	})
	cases := map[string]string{
		"ghcr.io/octopilot/builder-jammy-base:rust-builder-d741287": "us-docker.pkg.dev/pw-ctl/ci/octopilot/builder-jammy-base:rust-builder-d741287",
		"paketobuildpacks/run-jammy-base:latest":                    "us-docker.pkg.dev/pw-ctl/ci/paketobuildpacks/run-jammy-base:latest",
		"ubuntu:jammy":                                              "us-docker.pkg.dev/pw-ctl/ci/library/ubuntu:jammy",
		"docker.io/library/alpine":                                  "us-docker.pkg.dev/pw-ctl/ci/library/alpine",
		"quay.io/org/img@" + digest:                                 "us-docker.pkg.dev/pw-ctl/ci/org/img@" + digest,
		"ghcr.io/octopilot/op:v1.2.0@" + digest:                     "us-docker.pkg.dev/pw-ctl/ci/octopilot/op:v1.2.0@" + digest,
		"registry.k8s.io/pause:3.10":                                "us-docker.pkg.dev/pw-ctl/ci/pause:3.10",
		// never rewritten
		"ghcr.io/octopilot/igniteflux-base:latest":         "ghcr.io/octopilot/igniteflux-base:latest", // this config builds it
		"localhost:5001/x:dev":                             "localhost:5001/x:dev",
		"127.0.0.1:5001/x":                                 "127.0.0.1:5001/x",
		"host.docker.internal:5001/x:1":                    "host.docker.internal:5001/x:1",
		"ttl.sh/uuid-base:1d@" + digest:                    "ttl.sh/uuid-base:1d@" + digest,
		"us-docker.pkg.dev/pw-ctl/platform/pricewhisperer": "us-docker.pkg.dev/pw-ctl/platform/pricewhisperer", // already on the mirror's host
		"not a reference":                                  "not a reference",
	}
	for in, want := range cases {
		if got := s.Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
	if s.Registry() != "us-docker.pkg.dev/pw-ctl/ci" {
		t.Errorf("Registry() = %q (trailing slash should be trimmed)", s.Registry())
	}
}

func TestBuilderRunImage(t *testing.T) {
	cases := []struct {
		label, want string
		wantErr     bool
	}{
		{`{"runImages":[{"image":"ghcr.io/octopilot/run-jammy-base:latest","mirrors":["x/y"]}],"stack":{"runImage":{"image":"old/run"}}}`, "ghcr.io/octopilot/run-jammy-base:latest", false},
		{`{"stack":{"runImage":{"image":"paketobuildpacks/run-jammy-base:latest"}}}`, "paketobuildpacks/run-jammy-base:latest", false},
		{`{"stack":{}}`, "", true},
		{``, "", true},
		{`{`, "", true},
	}
	for _, c := range cases {
		got, err := BuilderRunImage(c.label)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("BuilderRunImage(%q) = %q, %v; want %q, err=%v", c.label, got, err, c.want, c.wantErr)
		}
	}
}

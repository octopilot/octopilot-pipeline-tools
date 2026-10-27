package cmd

import "testing"

func TestSccachePackEnvOff(t *testing.T) {
	t.Setenv("OP_SCCACHE", "")
	t.Setenv("SCCACHE_WEBDAV_ENDPOINT", "http://cache:8080/")
	env := map[string]string{}
	sccachePackEnv(env)
	if len(env) != 0 {
		t.Fatalf("OP_SCCACHE unset must add nothing, got %v", env)
	}
}

func TestSccachePackEnvOn(t *testing.T) {
	t.Setenv("OP_SCCACHE", "true")
	t.Setenv("SCCACHE_WEBDAV_ENDPOINT", "http://cache:8080/")
	t.Setenv("SCCACHE_WEBDAV_PASSWORD", "pw")
	t.Setenv("NOT_SCCACHE", "x")
	env := map[string]string{}
	sccachePackEnv(env)
	if env["BP_RUST_SCCACHE"] != "true" || env["SCCACHE_WEBDAV_ENDPOINT"] != "http://cache:8080/" || env["SCCACHE_WEBDAV_PASSWORD"] != "pw" {
		t.Fatalf("missing forwarded vars: %v", env)
	}
	if _, ok := env["NOT_SCCACHE"]; ok {
		t.Fatalf("forwarded an unrelated var: %v", env)
	}
}

package cmd

import (
	"os"
	"testing"
)

func TestRustCachePackEnvNoCacheImage(t *testing.T) {
	t.Setenv("OP_CACHE_IMAGE", "")
	unsetEnv(t, "BP_RUST_CACHE")
	env := map[string]string{}
	rustCachePackEnv(env)
	if env["BP_RUST_CACHE"] != "false" {
		t.Fatalf("no cache image must turn the rust layer cache off, got %v", env)
	}
}

func TestRustCachePackEnvWithCacheImage(t *testing.T) {
	t.Setenv("OP_CACHE_IMAGE", "registry.example/app/cache:main")
	unsetEnv(t, "BP_RUST_CACHE")
	env := map[string]string{}
	rustCachePackEnv(env)
	if _, ok := env["BP_RUST_CACHE"]; ok {
		t.Fatalf("a cache image must leave BP_RUST_CACHE unset, got %v", env)
	}
}

func TestRustCachePackEnvProjectWins(t *testing.T) {
	t.Setenv("OP_CACHE_IMAGE", "")
	unsetEnv(t, "BP_RUST_CACHE")
	env := map[string]string{"BP_RUST_CACHE": "true"}
	rustCachePackEnv(env)
	if env["BP_RUST_CACHE"] != "true" {
		t.Fatalf("project env must win, got %v", env)
	}
}

func TestRustCachePackEnvOpEnvWins(t *testing.T) {
	t.Setenv("OP_CACHE_IMAGE", "")
	t.Setenv("BP_RUST_CACHE", "true")
	env := map[string]string{}
	rustCachePackEnv(env)
	if env["BP_RUST_CACHE"] != "true" {
		t.Fatalf("op's own BP_RUST_CACHE must win, got %v", env)
	}
}

// unsetEnv removes key for the rest of the test and restores it afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "") // registers the restore
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

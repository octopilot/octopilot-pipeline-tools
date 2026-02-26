# Contributing to Octopilot Pipeline Tools

Thank you for your interest in contributing to `op`!

## Prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| **Go** | 1.25.6+ | Must match the fork's Go version (see `go.mod`). |
| **Docker** | 24+ | Required for building images and running integration tests. |
| **just** | latest | Primary development task runner. `brew install just` or `cargo install just`. |
| **golangci-lint** | latest | Required for linting. `brew install golangci-lint`. |
| **QEMU / binfmt** | — | Required for multi-arch integration tests: `docker run --privileged --rm tonistiigi/binfmt --install all`. |

---

## Quick Start

```bash
# Clone and build
git clone https://github.com/octopilot/octopilot-pipeline-tools
cd octopilot-pipeline-tools
just build          # compiles ./op

# Unit tests
just test

# Lint
just lint

# Install to GOPATH/bin
just install
```

---

## Project Structure

```
octopilot-pipeline-tools/
├── cmd/op/main.go              # CLI entry point
├── internal/
│   ├── cmd/
│   │   ├── build.go            # op build — Skaffold + Pack integration, multi-arch
│   │   ├── promote.go          # op promote-image — crane library
│   │   ├── watch.go            # op watch-deployment — kubectl + flux
│   │   ├── run.go              # op run — docker run wrapper
│   │   └── root.go             # CLI root, config loading (Cobra + Viper)
│   ├── pack/build.go           # Pack library integration (direct build, no subprocess)
│   └── util/                   # Shared helpers: build_result, config, registry, etc.
├── base/Dockerfile             # op-base runtime image (Ubuntu Jammy + CNB user)
├── skaffold.yaml               # Two-artifact build: op-base (Docker) + op (Buildpacks)
├── Justfile                    # Development task runner
└── tests/integration/          # Integration tests (require -tags integration)
```

---

## Build Architecture

### Two-Artifact Build (`skaffold.yaml`)

`op` uses a **two-artifact Skaffold configuration**:

1. **`op-base`** — A lightweight Dockerfile artifact (`base/Dockerfile`) providing the Ubuntu Jammy runtime environment with the `cnb` user. Built per platform using `docker build`.
2. **`op`** — The application artifact built with Cloud Native Buildpacks, using `op-base` as its run image. Built per platform using the Pack library.

When `op build --push --platform linux/amd64,linux/arm64` is run:

- **Docker artifacts** (`op-base`): built per-platform with `docker build --platform X --push`, with `BUILDX_NO_DEFAULT_ATTESTATIONS=1` to suppress BuildKit provenance wrapping. Per-platform images are assembled into a manifest list using `go-containerregistry`.
- **Buildpack artifacts** (`op`): built per-platform with the Pack library directly (`internal/pack/build.go`). The resolved run image (`op-base:latest@sha256:...`) is passed to each platform build, ensuring the correct multi-arch base is used. Per-platform images are also assembled into a manifest list.

Both final manifest lists are written to `build_result.json`:

```json
{
  "builds": [
    {"imageName": "op-base", "tag": "ghcr.io/octopilot/op-base:latest@sha256:..."},
    {"imageName": "op",      "tag": "ghcr.io/octopilot/op:latest@sha256:..."}
  ]
}
```

> Downstream steps that consume a specific image should filter by `imageName`, not rely on array index (`builds[0]` is `op-base`, not `op`).

---

## Running Tests

### Unit Tests

```bash
just test           # go test ./... -v
```

### Integration Tests

Integration tests build a real multi-context buildpack image and push it to a local registry. They require Docker and QEMU.

**1. Start the local TLS registry:**

```bash
docker run -d --rm --name octopilot-registry \
  -p 5001:5001 \
  ghcr.io/octopilot/registry-tls:latest
```

> **Use only `ghcr.io/octopilot/registry-tls`** — a standard `registry:2` container will not have the expected TLS certificates or Envoy setup, causing `x509: certificate signed by unknown authority` errors.

**2. Run:**

```bash
just test-integration
# equivalent to:
# just build && OP_BINARY=$PWD/op go test -tags integration -v ./tests/integration/...
```

**Known issues:**

- **`BUILDX_NO_DEFAULT_ATTESTATIONS=1`**: The integration test fixture sets this env var to prevent BuildKit from wrapping per-platform images in an OCI Index that includes a provenance attestation manifest. Without it, `manifest_list.go` would call `remote.Image()` on an index reference and fail with `"no child with platform <host> in index"`.
- **`OP_PACK_NETWORK=host`**: Required in CI (set in `ci.yml`) so that the pack build lifecycle container can reach the local registry over the host network.
- **`OP_REGISTRY_CA_PATH`**: The test fixture automatically extracts the CA certificate from the running `octopilot-registry` container and sets this env var, which `internal/cmd/build.go` mounts into the pack build container.

---

## CI Pipeline

The GitHub Actions workflow (`.github/workflows/ci.yml`) uses a **three-job release pipeline** to stay within the ~14 GB disk limit on `ubuntu-latest` runners:

```
detect → lint + test → integration
                              ↓
                       build-binaries   ← Go cross-compile; uploads dist/op-* artifact
                              ↓
                       build-container  ← frees ~15 GB of unused toolchains,
                              ↓           downloads binary, builds + pushes Docker,
                              ↓           attests, uploads SBOM artifact
                       publish-release  ← downloads all artifacts, creates GitHub release
```

**Why the split?** A single job combining Go cross-compilation (4 platforms) and multi-arch Docker builds exceeds available disk. The `build-container` job starts with a fresh runner and immediately removes unused toolchains (Android SDK, .NET, Haskell, etc.) before pulling any Docker images.

**Attestation**: The `build-container` job uses `jq -r '.builds[] | select(.imageName == "op") | .tag'` to extract the final container's digest from `build_result.json`, not `builds[0]` (which is `op-base`). The attestation `subject-name` is `ghcr.io/octopilot/op` — the actual GHCR path for the published image.

Local equivalent of the disk-free step:

```bash
just free-disk
```

---

## Dependencies & Forks

`octopilot-pipeline-tools` depends on forked versions of upstream projects to support multi-architecture builds with the Pack and Skaffold Go libraries. Forks are maintained on dedicated branches until changes are accepted upstream.

### 1. Skaffold (`octopilot/skaffold` — branch `buildpacks-publish-fix`)

- **Fork**: [https://github.com/octopilot/skaffold](https://github.com/octopilot/skaffold)
- **Upstream**: `GoogleContainerTools/skaffold`
- **Key changes**:
  - `pkg/skaffold/build/buildpacks/lifecycle.go`: when pushing, uses the requested tag directly (not `:latest`) and sets `Publish: true` so Pack pushes to the registry without a daemon export step — avoids the `containerd` daemon digest error on macOS.
  - `pkg/skaffold/build/buildpacks/build.go`: when `pushImages` is true, returns the pushed tag directly rather than calling `localDocker.Tag` + `Push`.
  - `pkg/skaffold/docker/manifest_list.go`: `fetchSinglePlatformImage()` — when `remote.Image()` fails on a reference that is an OCI Index (created by BuildKit provenance), falls back to `remote.Index()` and extracts the correct platform image from the index, skipping attestation manifests (`os=unknown` or annotated as `vnd.docker.reference.type=attestation-manifest`).
  - `pkg/skaffold/build/docker/docker.go`: sets `BUILDX_NO_DEFAULT_ATTESTATIONS=1` when building a specific platform with `--push`, preventing BuildKit from wrapping single-platform pushes in an OCI Index.
  - `pkg/skaffold/build/buildpacks/fetcher.go`: uses `moby/moby` client directly (instead of `localDocker.RawClient()`) for compatibility with current Docker API.
  - Go version pinned to 1.25.6 for lifecycle compatibility.

### 2. Pack (`octopilot/pack` — branch `containerd-workaround`)

- **Fork**: [https://github.com/octopilot/pack](https://github.com/octopilot/pack)
- **Upstream**: `buildpacks/pack`
- **Key changes**:
  - Exposes `BuildOptions` and internal registry handling as a library so `internal/pack/build.go` can call `client.Build()` directly without a subprocess.
  - Publish-then-pull workaround for `containerd`-backed Docker daemons (fixes [#2272](https://github.com/buildpacks/pack/issues/2272)) when building without `--publish`.
  - Supports specifying the target platform (`Platform` field) in `BuildOptions` for direct multi-arch builds.

### 3. Builder Image (`ghcr.io/octopilot/builder-jammy-base`)

- **Source**: [https://github.com/octopilot/buildpacks](https://github.com/octopilot/buildpacks)
- **Purpose**: Custom Cloud Native Buildpacks builder based on Ubuntu Jammy. Includes the `octopilot/rust` buildpack and is optimised for the pipeline's caching and multi-arch requirements.

### 4. Rust Buildpack (`ghcr.io/octopilot/rust`)

- **Purpose**: Specialized Rust build support for the `builder-jammy-base` stack, filling gaps not covered by standard Paketo buildpacks.

---

## Making Changes to the Forks

Both forks have local checkouts at:
- `/Users/casibbald/Workspace/octopilot/skaffold` (branch `buildpacks-publish-fix`)
- `/Users/casibbald/Workspace/octopilot/pack` (branch `containerd-workaround`)

After pushing changes to a fork branch, update the `replace` directive in `go.mod` with the new pseudo-version:

```bash
# Get the new pseudo-version after pushing the fork branch
go get github.com/octopilot/skaffold/v2@<commit-sha>
go mod vendor
go mod tidy
```

---

## Code Style

- Standard `gofmt` / `goimports` formatting (enforced by `just lint`).
- All new functions in `internal/` must have unit tests.
- Integration tests live in `tests/integration/` and require `-tags integration`.
- No shell scripts — all CI operations go through `just` recipes or Go code.

# Octopilot Pipeline Tools (`op`)

[![License: PolyForm Shield 1.0.0](https://img.shields.io/badge/License-PolyForm%20Shield%201.0.0-blue.svg)](https://polyformproject.org/licenses/shield/1.0.0)

`op` is an organisation-agnostic CLI tool that provides a **"path to production"** for your applications. It streamlines CI/CD pipelines and local development workflows by unifying **Skaffold** and **Cloud Native Buildpacks**.

**Why `op`?**

- **No Dockerfile Required**: Build your applications directly from source code using Cloud Native Buildpacks. `op` handles the complexity, so you rarely need to write or maintain a `Dockerfile` for standard languages (Go, Node.js, Python, Java, etc.). A `Dockerfile` is only needed for custom base images or very specific build requirements.
- **Pipeline Paths to Production**: `op` supports a structured flow for your artifacts, but **you define the pipeline** — whether you need a simple "Build → Deploy" workflow or a complex "Build → Dev → Staging → Production" promotion chain with soak tests, `op` adapts to your requirements. It uses Skaffold's profiles to define these stages, ensuring the *exact same artifact* is promoted through your defined path.
- **Unified Interface**: Whether running locally or in GitHub Actions, `op` provides a consistent command set (`build`, `run`, `promote`), reducing the "works on my machine" problem.
- **Multi-Architecture**: Native support for building and publishing multi-platform images (`linux/amd64`, `linux/arm64`) in a single command.

## Features

- **Standardized Builds**: Integrates directly with the Skaffold and Pack Go libraries to build artifacts and produce a `build_result.json` contract file.
- **Multi-Arch Manifest Lists**: Builds per-platform images and assembles OCI manifest lists using `go-containerregistry`, bypassing Docker daemon limitations on macOS.
- **Artifact Promotion**: Promotes immutable container images between environments (e.g., dev → staging → prod) using the `crane` library — no rebuild required.
- **Deployment Verification**: Watches Kubernetes deployments (Flux/Helm) to ensure successful rollouts.
- **Local Development**: Helpers for running built images locally and managing a local TLS-enabled Docker registry.

## Installation

### Download Binary

Download the latest pre-built binary for your platform from the [Releases page](https://github.com/octopilot/octopilot-pipeline-tools/releases):

```bash
# macOS (Apple Silicon)
curl -L https://github.com/octopilot/octopilot-pipeline-tools/releases/latest/download/op-darwin-arm64 -o op
chmod +x op && sudo mv op /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/octopilot/octopilot-pipeline-tools/releases/latest/download/op-darwin-amd64 -o op
chmod +x op && sudo mv op /usr/local/bin/

# Linux (amd64)
curl -L https://github.com/octopilot/octopilot-pipeline-tools/releases/latest/download/op-linux-amd64 -o op
chmod +x op && sudo mv op /usr/local/bin/
```

### Container Image

The `op` container image is published to GHCR and is used in CI/CD pipelines. It is itself built using `op`, making this a self-hosted/bootstrapping tool:

```bash
docker pull ghcr.io/octopilot/op:latest
```

Run `op` from the container (useful in CI environments):

```bash
docker run --rm \
  -v "$(pwd):/workspace" \
  -w /workspace \
  -e SKAFFOLD_DEFAULT_REPO=ghcr.io/my-org \
  ghcr.io/octopilot/op:latest \
  build --repo ghcr.io/my-org --push
```

### Build from Source

```bash
git clone https://github.com/octopilot/octopilot-pipeline-tools
cd octopilot-pipeline-tools
go build -o op ./cmd/op
```

Or with `just`:

```bash
just build
```

## Usage

Global flags:
- `--config`: Path to config file (default: `.github/octopilot.yaml` or `pipeline.properties`).

---

### 1. `op build`

Builds all artifacts defined in `skaffold.yaml`. When `--push` is used, images are pushed directly to the registry; multi-arch builds produce an OCI manifest list.

```bash
# Local build (no push)
op build

# Push multi-arch to a registry
op build --repo ghcr.io/my-org --push --platform linux/amd64,linux/arm64
```

| Flag | Description |
|------|-------------|
| `--repo` | Target registry/repository (overrides `.github/octopilot.yaml` and env). |
| `--push` | Push images to the registry. Required for multi-arch builds. |
| `--platform` | Comma-separated platform list, e.g. `linux/amd64,linux/arm64`. |
| `--filename` / `-f` | Path to `skaffold.yaml` (default: `skaffold.yaml` in cwd). |
| `--sbom-output` | Directory for generated SBOMs. |
| `--propagation-timeout` | How long to wait for registry image availability after push (default `3m`). |

**Environment variables**: `SKAFFOLD_DEFAULT_REPO`, `DOCKER_METADATA_OUTPUT_VERSION`, `SKAFFOLD_PROFILE`, `SKAFFOLD_LABEL`, `SKAFFOLD_NAMESPACE`.

---

### 2. `build_result.json` — the build contract

`op build --push` writes `build_result.json` in the working directory. This file is the contract between the build step and the downstream promotion/deployment steps. Each entry contains the fully-qualified, immutable image reference (registry + tag + sha256 digest).

```json
{
  "builds": [
    {
      "imageName": "op-base",
      "tag": "ghcr.io/my-org/op-base:latest@sha256:abc123..."
    },
    {
      "imageName": "my-app",
      "tag": "ghcr.io/my-org/my-app:v1.2.3@sha256:def456..."
    }
  ]
}
```

> **Note:** When `skaffold.yaml` defines multiple artifacts (e.g. a base image and an application image), all appear in `builds`. Downstream steps that consume a specific image (e.g. attestation, promotion) should filter by `imageName` using `jq -r '.builds[] | select(.imageName == "my-app") | .tag'`.

---

### 3. `op promote-image`

Promotes (copies) a container image from a source environment to a destination environment without rebuilding. Reads from `build_result.json`.

```bash
op promote-image \
  --source dev \
  --destination prod \
  --build-result-dir .
```

| Flag | Description |
|------|-------------|
| `--source` | Source environment (`dev`, `pp`, `prod`). |
| `--destination` | Destination environment (`pp`, `prod`). |
| `--build-result-dir` | Directory containing `build_result.json`. |

**Configuration**: resolves registry paths from `GOOGLE_GKE_IMAGE_<ENV>_REPOSITORY`, `PROMOTE_SOURCE_REPOSITORY`, or `PROMOTE_DESTINATION_REPOSITORY` env vars (and `.github/octopilot.yaml`).

---

### 4. `op watch-deployment`

Waits for a Flux/Helm deployment to sync and roll out a new image tag.

```bash
op watch-deployment \
  --component my-api \
  --environment dev \
  --namespace default \
  --build-result-dir .
```

| Flag | Description |
|------|-------------|
| `--component` | Name of the Deployment or HelmRelease. |
| `--environment` | Target environment (`dev`, `pp`, `prod`). |
| `--namespace` | Kubernetes namespace (default: `default`). |
| `--timeout` | `kubectl rollout status` timeout (default: `30m`). |
| `--build-result-dir` | Directory containing `build_result.json`. |

---

### 5. Local Development

#### Start Local Registry

Starts a local TLS-enabled Docker registry on port 5001. Generates self-signed certificates and optionally installs them for system and Colima trust.

```bash
op start-registry           # start registry, prompt for cert trust
op start-registry --trust   # install cert for system trust (may prompt for sudo)
```

> Add `127.0.0.1 registry.local` to `/etc/hosts` before running.

#### Run a Context

Runs a built image for a Skaffold context locally using `docker run`, applying ports, env vars, and volumes from `.github/octopilot.yaml`.

```bash
op run context list   # list runnable contexts from skaffold.yaml
op run api            # run the 'api' context
op run frontend       # run the 'frontend' context
```

---

## Configuration

`op` reads configuration from `.github/octopilot.yaml` (preferred) or `pipeline.properties` in the working directory. Environment variables override file values. Pass a custom path with `--config <path>` or `OCTOPILOT_PIPELINE_PROPERTIES=<path>`.

### `skaffold.yaml`

`op` relies on a standard `skaffold.yaml`. The recommended pattern for a Go application with a custom run image is a **two-artifact setup**: a base image (Docker) and the application image (Buildpacks).

```yaml
apiVersion: skaffold/v4beta1
kind: Config
metadata:
  name: my-application
build:
  artifacts:
    # 1. Custom run image (Dockerfile) — provides the runtime environment.
    #    Built with docker build per platform; assembled into a manifest list.
    - image: my-app-base
      context: base
      docker:
        dockerfile: Dockerfile

    # 2. Application image (Cloud Native Buildpacks) — no Dockerfile required.
    #    Uses the custom base image as the run image.
    - image: my-app
      context: .
      buildpacks:
        builder: ghcr.io/octopilot/builder-jammy-base:latest
        runImage: my-app-base
        env:
          - BP_GO_BUILD_FLAGS=-buildvcs=false
```

> `op build --push --platform linux/amd64,linux/arm64` handles both artifact types: Dockerfile artifacts are built per-platform and assembled into a manifest list; buildpack artifacts are built per-platform with the Pack library.

### `.github/octopilot.yaml`

```yaml
# Default registry for builds and local dev
default_repo: localhost:5001

# Per-context run configuration (used by `op run`)
contexts:
  api:
    ports: ["8081:8080"]
    env:
      PORT: "8080"
  frontend:
    ports: ["8080:8080"]
    env:
      PORT: "8080"
```

---

## Development Workflow (`just`)

The `Justfile` provides the primary development interface:

```bash
just build            # compile op binary to ./op
just test             # run unit tests
just test-integration # build op, start registry, run integration tests
just lint             # run golangci-lint
just clean            # remove ./op, build_result.json, dist/
just clean-all        # clean + docker system prune
just free-disk        # free ~15 GB on a GitHub Actions runner
just install          # install op to GOPATH/bin
just deps             # go mod download + tidy + vendor
```

---

## References

- [Cloud Native Buildpacks](https://buildpacks.io/)
- [Skaffold](https://skaffold.dev/)
- [Octopilot Actions](https://github.com/octopilot/actions)
- [Octopilot Builder](https://github.com/octopilot/buildpacks)
- [go-containerregistry / crane](https://github.com/google/go-containerregistry)
- [Custom TLS Registry](https://github.com/octopilot/registry-tls)

# Octopilot Pipeline Tools (`op`)

`op` is an organisation-agnostic CLI tool that provides a **"path to production"** for your applications. It streamlines CI/CD pipelines and local development workflows by unifying **Skaffold** and **Cloud Native Buildpacks**.

**Why `op`?**

-   **No Dockerfile Required**: Build your applications directly from source code using Cloud Native Buildpacks. `op` handles the complexity, meaning you often don't need to write or maintain a `Dockerfile` for standard languages (Go, Node.js, Python, Java, etc.). A `Dockerfile` is only needed for very specific custom builds.
-   **Pipeline Paths to Production**: `op` supports a structured flow for your artifacts, but **you define the pipeline**. whether you need a simple "Build → Deploy" workflow or a complex "Build → Dev → Staging → Production" promotion chain with soak tests, `op` adapts to your requirements. It uses Skaffold's profiles to define these stages, ensuring that the *exact same artifact* is promoted through your defined path.
-   **Unified Interface**: Whether running locally or in GitHub Actions, `op` provides a consistent command set (`build`, `run`, `promote`), reducing the "works on my machine" problem.

## Features

- **Standardized Builds**: Wraps `skaffold` to ensure consistent build configurations and artifact output (`build_result.json`).
- **Artifact Promotion**: Promotes immutable container images between environments (e.g., dev -> stage -> prod) using `crane`.
- **Deployment Verification**: Watches Kubernetes deployments (Flux/Helm) to ensure successful rollouts.
- **Local Development**: Helpers for running built images locally and managing a local TLS-enabled Docker registry.

## Installation

### GitHub Packages

Download the latest binary from the [Releases page](https://github.com/octopilot/octopilot-pipeline-tools/releases) or use the container image:

### Docker

The tool is available as a Docker image, typically used in CI/CD pipelines:

```bash
docker run -v $(pwd):/workspace -w /workspace octopilot/pipeline-tools op <command>
```

## Usage

Global flags:
- `--config`: Path to config file (default: `pipeline.properties` or `.github/octopilot.yaml`).

### 1. Build

Builds artifacts using Skaffold.

```bash
op build --repo <registry-repo>
```

-   **--repo**: Target container registry repository (overrides default).
-   **Env Vars**: `SKAFFOLD_PROFILE`, `SKAFFOLD_LABEL`, `SKAFFOLD_NAMESPACE`.
-   **Output**: Generates `build_result.json` containing the built image tags.

### 2. Build Artifacts (`build_result.json`)

The `build_result.json` file is the contract between the build and promotion steps. It contains the fully qualified, immutable image references (including sha256 digests) produced by Skaffold.

**Example content (Local Development):**
*Note: In this example, the images are pushed to a local registry (`localhost:5001`). In a CI/CD environment (like GitHub Actions), the `tag` will point to your production container registry (e.g., `ghcr.io/my-org/my-app:tag...`).*

```json
{
  "builds": [
    {
      "imageName": "sample-react-node-frontend",
      "tag": "localhost:5001/sample-react-node-frontend:76bce40@sha256:..."
    }
  ]
}
```

**Example content (CI/CD):**

```json
{
  "builds": [
    {
      "imageName": "my-app-frontend",
      "tag": "ghcr.io/my-org/my-app-frontend:v1.2.3@sha256:..."
    }
  ]
}
```

-   **`tag`**: The specific image reference to be promoted or deployed.
-   **`imageName`**: The Skaffold artifact name (optional in some contexts, but `tag` is mandatory).

### 3. Promote Image

Promotes (copies) a container image from a source environment to a destination environment without rebuilding. relies on `build_result.json`.

```bash
op promote-image --source <env> --destination <env> --build-result-dir <dir>
```

-   **--source**: Source environment (e.g., `dev`).
-   **--destination**: Destination environment (e.g., `prod`).
-   **--build-result-dir**: Directory containing `build_result.json` from the build step.
-   **Configuration**: Requires `GOOGLE_GKE_IMAGE_<ENV>` env vars or config to resolve registry paths.

### 3. Watch Deployment

Waits for a Flux/Helm deployment to sync and roll out the new image tag.

```bash
op watch-deployment --component <name> --environment <env> --namespace <ns> --build-result-dir <dir>
```

-   **--component**: Name of the Deployment or HelmRelease.
-   **--environment**: Target environment.
-   **--namespace**: Kubernetes namespace (default: `default`).
-   **--timeout**: Timeout for rollout status (default: `30m`).

### 4. Local Development

#### Start Local Registry

Starts a local Docker registry on port 5001 with auto-generated TLS certificates. Configures trust for Docker clients (and Colima on macOS).

```bash
op start-registry --trust
```

#### Run Context

Runs a specific Skaffold context locally using `docker run`, automatically handling port forwarding and environment variables defined in your configuration.

```bash
# List available contexts
op run context list

# Run a context
op run <context-name>
```

## Configuration

`op` reads configuration from `pipeline.properties` or `.github/octopilot.yaml`. It also supports standard environment variables for seamless integration with GitHub Actions and other CI systems.

## References

- [Cloud Native Buildpacks](https://buildpacks.io/)
- [Skaffold](https://skaffold.dev/)
- [Docker Registry](https://docs.docker.com/registry/)
- [Octopilot Actions](https://github.com/octopilot/octopilot-actions)

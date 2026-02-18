# Contributing to Octopilot Pipeline Tools

Thank you for your interest in contributing to `op`!

## Development Setup

### Prerequisites

-   **Go**: Version 1.26 or later.
-   **Docker**: Required for running tests and building images.

### Building from Source

To build and install the CLI from source:

```bash
go install github.com/octopilot/octopilot-pipeline-tools/cmd/op@latest
```

Or clone the repository and run:

```bash
go build -o op ./cmd/op
```

### Running Tests

Run all unit tests:

```bash
go test ./...
```

### Running Integration Tests Locally (macOS)

Running integration tests on macOS can be tricky due to Docker networking limitations (VM) and TLS certificates. We use a local registry with self-signed certificates (`ghcr.io/octopilot/registry-tls`) to simulate a production-like environment.

**Prerequisites:**
1.  Run the registry container:
    ```bash
    docker run -d --rm --name octopilot-registry -p 5001:5001 ghcr.io/octopilot/registry-tls:latest
    ```
2.  Ensure you have `go` 1.26+ installed.

**Running the Test:**
```bash
go test -tags integration -v -run TestIntegration_BuildpackMultiContext ./tests/integration/...
```

**Known Issues & Workarounds:**
*   **TLS Verification (`x509: certificate signed by unknown authority`)**: The `tests/integration/main_test.go` fixture automatically extracts the CA certificate from the running `octopilot-registry` container and mounts it into the `pack` build container via `OP_REGISTRY_CA_PATH`. **Do not use a standard `registry:2` container**, as it won't have the expected certs or Envoy proxy setup.
*   **Image Has No Layers (OCI Index Error)**: By default, Docker BuildKit on some platforms generates OCI Image Indexes (manifest lists) that include build attestations (provenance). The `pack` lifecycle v0.17+ (used internally) has issues handling these indexes when pulling from an insecure registry in this specific setup. The test fixture sets `BUILDX_NO_DEFAULT_ATTESTATIONS=1` to force standard single-manifest images.

## Dependencies & Forks

`octopilot-pipeline-tools` relies on several forked repositories and custom artifacts to support specific requirements (primarily multi-architecture builds via direct `pack` integration) that are not yet available upstream.

Dependencies will remain on these forks until the changes are accepted upstream.

### 1. Skaffold (`octopilot/skaffold`)

-   **Fork URL**: [https://github.com/octopilot/skaffold](https://github.com/octopilot/skaffold)
-   **Upstream**: `GoogleContainerTools/skaffold`
-   **Key Changes**:
    -   Modified artifact builders to support direct integration with the `pack` library for multi-arch builds, bypassing the Docker daemon limitation.
    -   Adjustments to support buildpacks configuration required for our custom builders.
    -   Downgraded Go version to 1.25.6 for compatibility.

### 2. Pack (`octopilot/pack`)

-   **Fork URL**: [https://github.com/octopilot/pack](https://github.com/octopilot/pack)
-   **Upstream**: `buildpacks/pack`
-   **Key Changes**:
    -   Exposed internal `BuildOptions` and registry handling logic to allow usage as a library within `op`.
    -   Customizations to support specific lifecycle versions and registry authentication flows.

### 3. Builder Image (`octopilot/builder-jammy-base`)

-   **Registry**: `ghcr.io/octopilot/builder-jammy-base`
-   **Source**: [https://github.com/octopilot/buildpacks](https://github.com/octopilot/buildpacks)
-   **Purpose**:
    -   A custom Cloud Native Buildpacks builder based on Ubuntu Jammy.
    -   Includes the `octopilot/rust` buildpack.
    -   Optimized for our pipeline's caching and multi-arch requirements.

### 4. Rust Buildpack (`octopilot/rust`)

-   **Registry**: `ghcr.io/octopilot/rust`
-   **Purpose**:
    -   Provides specialized Rust build support not fully covered by standard Paketo buildpacks.
    -   Ensures compatibility with our `builder-jammy-base` stack.

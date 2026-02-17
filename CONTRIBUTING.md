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

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

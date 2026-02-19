# Default to listing available commands
default:
    @just --list

# Build the op binary locally
build:
    go build -o op ./cmd/op

# Run unit tests

# Run unit tests (skips integration tests)
test:
    go test ./... -v

# Run integration tests (requires OP_BINARY)
test-integration: build
    export OP_BINARY=$PWD/op && go test -tags integration -v ./tests/integration/...

# Run linting (golangci-lint). Install once with: just install-tools
lint:
    #!/usr/bin/env bash
    export PATH="$(go env GOPATH)/bin:$PATH"
    golangci-lint run

# Clean build artifacts (local)
clean:
    rm -f op build_result.json
    rm -rf dist/

# Deep clean: also prune Docker images and builder cache
clean-all: clean
    -docker system prune -af 2>/dev/null || true
    -docker builder prune -af 2>/dev/null || true

# Free GitHub Actions runner disk space before large Docker builds.
# Removes pre-installed SDKs/toolchains not needed for container builds
# (~15 GB freed on ubuntu-latest: Android SDK, .NET, Haskell, unused tool caches).
# Safe to run anywhere; silently skips paths that do not exist.
free-disk:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "=== Disk usage before cleanup ==="
    df -h /
    sudo rm -rf \
        /usr/local/lib/android \
        /usr/share/dotnet \
        /opt/ghc \
        /usr/local/.ghcup \
        /usr/lib/jvm \
        /usr/share/swift \
        /usr/local/share/boost \
        /usr/share/gradle-8.8 \
        /opt/hostedtoolcache/go \
        /opt/hostedtoolcache/node \
        /opt/hostedtoolcache/PyPy \
        /opt/hostedtoolcache/Python \
        /opt/hostedtoolcache/Ruby \
        2>/dev/null || true
    echo "=== Disk usage after cleanup ==="
    df -h /

# Install dependencies and tools
deps:
    go mod download
    go mod tidy
    go mod vendor

# Install dev tools (golangci-lint). Run once after cloning.
# GOTOOLCHAIN pins the compiler version to match go.mod so golangci-lint
# is built with the same Go that the project targets.
install-tools:
    #!/usr/bin/env bash
    GOVERSION=$(grep '^go ' go.mod | awk '{print $2}')
    GOTOOLCHAIN="go${GOVERSION}" go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Install the binary to GOPATH/bin
install:
    go install ./cmd/op

#

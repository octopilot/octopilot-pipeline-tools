# Default to listing available commands
default:
  @just --list

# Build the op binary locally
build:
  go build -o op ./cmd/op

# Run unit tests
test:
  go test ./... -v

# Run linting (golangci-lint)
lint:
  golangci-lint run

# Clean build artifacts
clean:
  rm -f op build_result.json

# Install dependencies and tools
deps:
  go mod download
  go mod tidy
  go mod vendor

# Install the binary to GOPATH/bin
install:
  go install ./cmd/op

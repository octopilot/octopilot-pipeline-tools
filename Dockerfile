# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o /octopilot-pipeline-tools ./cmd/octopilot-pipeline-tools

# Final stage
FROM alpine:latest

WORKDIR /

# Copy the binary from the builder stage
COPY --from=builder /octopilot-pipeline-tools /octopilot-pipeline-tools

ENTRYPOINT ["/octopilot-pipeline-tools"]

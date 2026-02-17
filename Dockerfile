# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /op ./cmd/op

# Final stage
FROM alpine:latest

WORKDIR /

# Install git (required for op build / skaffold)
RUN apk add --no-cache git


# Copy the binary from the builder stage
COPY --from=builder /op /op

ENTRYPOINT ["/op"]

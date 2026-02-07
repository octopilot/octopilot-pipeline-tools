# OctoPilot pipeline tools: run Skaffold/build_result/watch-deployment in CI or locally.
# Use as a container so the toolchain is not copied into each app repo.
# Supports linux/amd64 and linux/arm64 (e.g. Apple Silicon Macs via Docker Desktop).
FROM python:3.11-slim

# Install Skaffold, flux, kubectl, crane for full pipeline (optional: multi-stage or slim image without these for CLI-only).
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Multi-arch: TARGETARCH is set by Docker (amd64 or arm64). Pick the right binary per arch.
ARG TARGETARCH
ARG SKAFFOLD_VERSION=v2.14.0
# Skaffold: skaffold-linux-amd64 or skaffold-linux-arm64
RUN curl -sSL -o /usr/local/bin/skaffold "https://storage.googleapis.com/skaffold/releases/${SKAFFOLD_VERSION}/skaffold-linux-${TARGETARCH}" \
    && chmod +x /usr/local/bin/skaffold

# kubectl: bin/linux/amd64 or bin/linux/arm64
RUN curl -sSL "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${TARGETARCH}/kubectl" -o /usr/local/bin/kubectl \
    && chmod +x /usr/local/bin/kubectl

# Flux: flux_*_linux_amd64.tar.gz or flux_*_linux_arm64.tar.gz
ARG FLUX_VERSION=2.7.5
RUN curl -sSL "https://github.com/fluxcd/flux2/releases/download/v${FLUX_VERSION}/flux_${FLUX_VERSION}_linux_${TARGETARCH}.tar.gz" | tar xz -C /usr/local/bin

# Crane: Linux_x86_64 (amd64) or Linux_arm64 (arm64)
RUN case "${TARGETARCH}" in amd64) CRANE_ARCH=x86_64;; arm64) CRANE_ARCH=arm64;; *) CRANE_ARCH="${TARGETARCH}";; esac \
    && curl -sSL "https://github.com/google/go-containerregistry/releases/latest/download/go-containerregistry_Linux_${CRANE_ARCH}.tar.gz" | tar xz -C /usr/local/bin crane

# Pack (Buildpacks CLI) for op build-push (linux or linux-arm64)
ARG PACK_VERSION=0.40.0
RUN case "${TARGETARCH}" in \
    amd64) PACK_ARCH=linux;; \
    arm64) PACK_ARCH=linux-arm64;; \
    *) PACK_ARCH=linux;; \
    esac \
    && curl -sSL "https://github.com/buildpacks/pack/releases/download/v${PACK_VERSION}/pack-v${PACK_VERSION}-${PACK_ARCH}.tgz" | tar xz -C /usr/local/bin pack \
    && chmod +x /usr/local/bin/pack

WORKDIR /workspace

# Install Python package
COPY pyproject.toml ./
COPY src ./src
RUN pip install --no-cache-dir .

# Default: run CLI (both op and octopipeline are installed; using short alias)
ENTRYPOINT ["op"]
CMD ["--help"]

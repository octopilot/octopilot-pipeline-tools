# OctoPilot pipeline tools: run Skaffold/build_result/watch-deployment in CI or locally.
# Use as a container so the toolchain is not copied into each app repo.
FROM python:3.11-slim

# Install Skaffold, flux, kubectl, crane for full pipeline (optional: multi-stage or slim image without these for CLI-only).
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Skaffold
ARG SKAFFOLD_VERSION=v2.14.0
RUN curl -Lo /usr/local/bin/skaffold https://storage.googleapis.com/skaffold/releases/${SKAFFOLD_VERSION#v}/skaffold-linux-amd64 \
    && chmod +x /usr/local/bin/skaffold

# kubectl (for watch-deployment)
RUN curl -sSL "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" -o /usr/local/bin/kubectl \
    && chmod +x /usr/local/bin/kubectl

# Flux CLI (for watch-deployment)
ARG FLUX_VERSION=2.3.0
RUN curl -sSL "https://github.com/fluxcd/flux2/releases/download/v${FLUX_VERSION}/flux_${FLUX_VERSION}_linux_amd64.tar.gz" | tar xz -C /usr/local/bin

# Crane (for promote-image)
RUN curl -sSL "https://github.com/google/go-containerregistry/releases/latest/download/go-containerregistry_Linux_x86_64.tar.gz" | tar xz -C /usr/local/bin crane

WORKDIR /workspace

# Install Python package
COPY pyproject.toml ./
COPY src ./src
RUN pip install --no-cache-dir .

# Default: run CLI
ENTRYPOINT ["octopilot-pipeline"]
CMD ["--help"]

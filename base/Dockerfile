FROM ubuntu:jammy

# Docker CLI (client only) from the static tarball: the octopilot action mounts the runner docker socket into this
# container, so with a client present op can build docker: artifacts in the integration fan-out. Static rather than the
# apt repo: the repo pulls gnupg and a libc6 update, and libc-bin's postinst segfaults under qemu on arm64 builds.
ARG DOCKER_VERSION=28.5.1
ARG BUILDX_VERSION=0.28.0
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    jq \
    bash \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && arch="$(uname -m)" \
    && curl -fsSL "https://download.docker.com/linux/static/stable/${arch}/docker-${DOCKER_VERSION}.tgz" \
       | tar xz -C /usr/local/bin --strip-components=1 docker/docker \
    && case "$arch" in x86_64) bx=amd64;; aarch64) bx=arm64;; *) bx="$arch";; esac \
    && mkdir -p /usr/local/lib/docker/cli-plugins \
    && curl -fsSL "https://github.com/docker/buildx/releases/download/v${BUILDX_VERSION}/buildx-v${BUILDX_VERSION}.linux-${bx}" \
       -o /usr/local/lib/docker/cli-plugins/docker-buildx \
    && chmod +x /usr/local/lib/docker/cli-plugins/docker-buildx

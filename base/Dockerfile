FROM ubuntu:jammy

# Docker CLI (client only): the octopilot action mounts the runner docker socket into this container, so with a client
# present op can build docker: artifacts in the integration fan-out (before this, docker: artifacts only built with
# build_bypass, where op ran on the runner itself).
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    jq \
    bash \
    ca-certificates \
    gnupg \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu jammy stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update && apt-get install -y --no-install-recommends docker-ce-cli docker-buildx-plugin \
    && rm -rf /var/lib/apt/lists/*

FROM ubuntu:jammy

USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    jq \
    bash \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*
USER cnb

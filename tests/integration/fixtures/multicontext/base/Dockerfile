# We need a base image that is compatible with the buildpack stack (Ubuntu Bionic usually for v1 builder)
# Or we can just use the official run image as a base and add something trivial
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
RUN touch /base-image-marker

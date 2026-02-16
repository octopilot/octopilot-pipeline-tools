# Dependencies

Tool versions used in the **container image** (see [Dockerfile](Dockerfile)). Kept in sync for reproducible builds and upgrade reference. The image is built for **linux/amd64** and **linux/arm64** (e.g. Apple Silicon Macs get the arm64 image when pulling from GHCR).

| Tool | Version | Notes |
|------|---------|--------|
| **Python** | 3.11-slim | Base image; also used for the CLI package. |
| **Skaffold** | v2.14.0 | [GCS releases](https://storage.googleapis.com/skaffold/releases/); path uses `v` prefix. |
| **kubectl** | stable | From `dl.k8s.io/release/stable.txt` at image build time. |
| **Flux CLI** | 2.7.5 | [flux2 releases](https://github.com/fluxcd/flux2/releases). |
| **crane** | latest | From [go-containerregistry](https://github.com/google/go-containerregistry/releases) at image build time. |
| **pack** | 0.40.0 | [Buildpacks pack CLI](https://github.com/buildpacks/pack/releases); used by `op build-push`. |

Python package dependencies (CLI) are in [pyproject.toml](pyproject.toml) (e.g. `click`, `pyyaml`). Dev tools (pytest, ruff) are listed under `[project.optional-dependencies]` there and in [CONTRIBUTING.md](CONTRIBUTING.md).

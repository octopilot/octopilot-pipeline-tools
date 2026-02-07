# OctoPilot Pipeline Tools

Organisation-agnostic CLI for **Skaffold/Buildpacks** pipelines: **build**, **push**, **build_result.json**, **watch-deployment**, **promote-image**. Invoke as **`octopilot-pipeline`** (install the package or run the Docker image). Designed to run in **Docker** or **GitHub Actions** so app repos get a single toolchain without copying scripts.

## Commands (standalone CLI: `octopilot-pipeline`)

| Command | Description |
|---------|-------------|
| `octopilot-pipeline build` | Run `skaffold build`. Uses `SKAFFOLD_DEFAULT_REPO` or registry env vars from config. |
| `octopilot-pipeline push` | Run `skaffold build` with profile (e.g. `push`), push to registry, write **build_result.json**. Registry from **--default-repo**, env, or **.registry** (--destination local\|ci\|all\|auto). Use **--push-all** to crane copy to all CI registries. |
| `octopilot-pipeline watch-deployment` | Read image tag from build_result.json; loop `flux reconcile helmrelease` until deployment image matches; then `kubectl rollout status`. |
| `octopilot-pipeline promote-image` | Read tag from build_result.json; **crane copy** from source env registry to destination (e.g. dev → pp). |

## .registry file (push destinations)

In the **repo root**, add a **`.registry`** YAML file to define where to push (local vs CI / multiple registries):

```yaml
# Local development
local: localhost:5000

# CI: use env interpolation so GitHub Actions (or any CI) can fill org/repo
#   ${VAR}  or  $VAR  → value of VAR
#   ${VAR:-default}   → VAR if set, else "default"
#   $$                → literal $
ci:
  - ghcr.io/${GITHUB_REPOSITORY_OWNER:-my-org}   # GitHub Actions sets GITHUB_REPOSITORY_OWNER
  - europe-west1-docker.pkg.dev/${GCP_PROJECT}/${GAR_REPO}
  - docker.io/${GITHUB_ACTOR}
  - url: ghcr.io/${GITHUB_REPOSITORY_OWNER}
    name: ghcr
```

- **push** resolves registry in order: **--default-repo** → env (e.g. `SKAFFOLD_DEFAULT_REPO`) → **.registry**.
- **--destination** `local` | `ci` | `all` | `auto`: which entry to use (default **auto**: in CI use `ci`, else `local`).
- **--push-all**: after pushing to the first registry, **crane copy** the image to every other `ci` destination.

So you can have one file for local + GHCR + GAR + ECR + Docker Hub and choose per run.

## Registry / default-repo

Your CI or workflow can set the push registry in env (e.g. from a properties file or GitHub Actions). **push** uses **--default-repo** if given, otherwise env (e.g. `SKAFFOLD_DEFAULT_REPO`), or the **.registry** file (see above).

## Install (local)

```bash
cd octopilot-pipeline-tools
pip install -e ".[dev]"
octopilot-pipeline --help
octopilot-pipeline push --default-repo localhost:5000 --help
```

## Run in Docker

Build the image (includes Skaffold, flux, kubectl, crane):

```bash
docker build -t octopilot-pipeline-tools .
docker run --rm -v "$(pwd):/workspace" -w /workspace \
  -e SKAFFOLD_DEFAULT_REPO=localhost:5000 \
  octopilot-pipeline-tools push --default-repo localhost:5000
```

Use in **GitHub Actions** (or any CI) by running the container and mounting the repo.

## Config (env or properties file)

- **OCTOPILOT_PIPELINE_PROPERTIES** or **--config** path to a `.properties` file (key=value, # comments).
- Env vars override file values. Common keys: `SKAFFOLD_DEFAULT_REPO`, and for multi-environment setups `GOOGLE_GKE_IMAGE_REPOSITORY`, `GOOGLE_GKE_IMAGE_PP_REPOSITORY`, `GOOGLE_GKE_IMAGE_PROD_REPOSITORY`, `WATCH_DESTINATION_REPOSITORY`, `PROMOTE_SOURCE_REPOSITORY`, `PROMOTE_DESTINATION_REPOSITORY`.

## Lint and tests

**Ruff (strict linting, line-length 120):**
```bash
pip install -e ".[dev]"
ruff check src tests && ruff format src tests --check
ruff check src tests --fix && ruff format src tests   # fix and format
```

**Tests and coverage (minimum 80%):**
```bash
pip install -e ".[dev]"
pytest tests/ -v --cov=src/octopilot_pipeline_tools --cov-report=term-missing --cov-branch
```

## Compatibility

The CLI is compatible with workflows that use a `.properties`-style config and Skaffold. Use the package or Docker image so each repo does not need to ship its own pipeline scripts.

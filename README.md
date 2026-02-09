# OctoPilot Pipeline Tools

Organisation-agnostic CLI for **Skaffold/Buildpacks** pipelines: **build**, **push**, **build_result.json**, **watch-deployment**, **promote-image**. Use the **`octopipeline`** command or the short alias **`op`** (same behaviour). Runs in **Docker** or **GitHub Actions** so app repos get a single toolchain without copying scripts.

## Alias

- **`op`** — short alias (recommended in CI and when typing by hand).
- **`octopipeline`** — full name.

Both are installed by the package; the container image uses **`op`** as its default entrypoint. Examples below use **`op`**; you can substitute **`octopipeline`** anywhere.

---

## How it fits Skaffold Buildpacks

Use **octopipeline** in app repos that build with **Skaffold** and **Cloud Native Buildpacks** (no Dockerfile):

1. Your repo has a **`skaffold.yaml`** that uses a Buildpacks builder (e.g. Paketo). No Dockerfile required.
2. **`op build`** — runs `skaffold build` (local build, optional push).
3. **`op push`** — runs `skaffold build` with a push profile, pushes images to your registry(ies), and writes **`build_result.json`** with image refs for CD.
4. **`op watch-deployment`** — reads **`build_result.json`**, reconciles the Flux HelmRelease, then waits for `kubectl rollout status`.
5. **`op promote-image`** — reads **`build_result.json`** and uses **crane** to copy the image from one environment registry to another (e.g. dev → pp → prod).

So: **Skaffold + Buildpacks** do the build; **octopipeline** wraps Skaffold, manages registries, and produces/consumes **build_result.json** for Flux and promotion. Same workflow works for [octopilot-samples](https://github.com/octopilot/octopilot-samples) and any repo with a Buildpacks-based **skaffold.yaml**. To design your own pipeline (single- or multi-environment), see [WORKFLOW.md](WORKFLOW.md). For how **skaffold.yaml** relates to op, buildpacks, and config, see **[skaffold.md](skaffold.md)**.

---

## Procfile and project.toml (Buildpacks)

Buildpacks (and thus **op build-push** / **op build** / **op push**) decide how to run your app from files in each artifact’s **build context** (e.g. `api/`, `frontend/`). Two common mechanisms:

### Procfile

A **Procfile** declares process types. Many Cloud Native Buildpacks (e.g. Paketo, Heroku) use it to set the container start command.

- **Format:** one line per process type: `process_type: command`. The **web** process type is the default for HTTP apps.
- **When to use it:** Use a Procfile when the buildpack does **not** auto-detect your start command, or when you need to **override** it (e.g. custom host/port, or a specific entrypoint like `uvicorn main:app --host 0.0.0.0 --port ${PORT:-8080}`).
- **Where:** Place **Procfile** in the **root of the build context** for that artifact (the same directory as `context` in `skaffold.yaml`). Example: if `skaffold.yaml` has `context: api`, put `Procfile` in `api/Procfile`.

**Example (single API server):**

```text
web: uvicorn main:app --host 0.0.0.0 --port ${PORT:-8080}
```

**Example (two process types, e.g. web + worker):**

```text
web: uvicorn main:app --host 0.0.0.0 --port ${PORT:-8080}
worker: python -m tasks.run
```

The **web** process is typically the default for HTTP; other types (e.g. **worker**) can be used for background jobs or secondary services. In Kubernetes you would run each process type as a separate Deployment or Job.

If you don’t add a Procfile and the buildpack can infer the start command (e.g. from `package.json` scripts or language conventions), the image still runs; add a Procfile when you need an explicit or custom command.

### project.toml

**project.toml** (Buildpacks [project descriptor](https://buildpacks.io/docs/reference/config/project-descriptor/)) configures build-time behaviour: env vars for the build, buildpack options, and (optionally) process types. Use it when you need to pass **build** or **launch** options to the buildpack (e.g. `BP_WEB_SERVER=nginx`, `BP_WEB_SERVER_ROOT=public` for static sites) or to define process types in TOML instead of a Procfile.

- **When to use it:** Use **project.toml** for buildpack-specific env vars (e.g. which web server, paths). Use **Procfile** when you only need a custom **start command** and the buildpack supports it.
- **Where:** Same as Procfile — in the **root of the artifact’s build context** (e.g. `frontend/project.toml`, `api/project.toml`).

**Summary:** Use **Procfile** to define or override the **run** command when the buildpack doesn’t pick the right one. Use **project.toml** for **build** and **launch** configuration (env, options). Both are optional if the buildpack already does the right thing.

For full detail (Procfile vs Dockerfile, per-artifact and hybrid setups, how op uses it), see **[procfile.md](procfile.md)**.

---

## Commands

| Command | Description |
|--------|-------------|
| `op build` | Run `skaffold build`. Uses `SKAFFOLD_DEFAULT_REPO` or registry env vars from config. |
| `op start-registry` | Start local registry with TLS on 5001 (replaces existing). Copies certs out; optionally install for system trust (`--trust-cert`) or Colima VM trust (`--trust-cert-colima`). Use before `op build-push` so localhost:5001 is trusted. |
| `op build-push` | Build and push using **pack** CLI with `--publish` for each artifact in skaffold.yaml; write **build_result.json**. Use when `op build` / `op push` fail (Mac containerd digest, Linux /layers permission). Default registry is **localhost:5001** (override with `--repo` or `SKAFFOLD_DEFAULT_REPO`). |
| `op push` | Run `skaffold build` with profile (e.g. `push`), push to registry, write **build_result.json**. Registry from **--default-repo**, env, or **.registry** (--destination local\|ci\|all\|auto). Use **--push-all** to crane copy to all CI registries. |
| `op watch-deployment` | Read image tag from build_result.json; loop `flux reconcile helmrelease` until deployment image matches; then `kubectl rollout status`. |
| `op promote-image` | Read tag from build_result.json; **crane copy** from source env registry to destination (e.g. dev → pp). |
| `op run` | **Local dev only:** run a built image for a Skaffold context via `docker run` with preconfigured ports/env/volumes from **.run.yaml**. **`op run context list`** lists contexts; **`op run <context>`** runs that context. Not for production; does not replace docker-compose, Kubernetes (e.g. kind), or full deploy workflows. |

---

## Usage

### macOS / Local development (Buildpacks)

**Known limitation:** On **macOS**, both **Docker Desktop** and **Colima** use a containerd-backed Docker daemon. When using **Skaffold + Buildpacks**, `op build` and `op push` fail with a digest error when the image is written to the daemon ([pack#2272](https://github.com/buildpacks/pack/issues/2272)). Skaffold invokes pack without `--publish`, so the image is always exported to the daemon first—even with `op push`—and the failure occurs before any push.

**Recommendations for Mac developers:**

- **Full pipeline (all samples):** Run **`op build-push`** (defaults to localhost:5001; start a local registry on 5001 first). For CI, use **`op build-push --repo <registry>`** or **`op build`** / **`op push`** on Linux.
- **Single-app local testing:** Run **`op build-push`** after starting a local registry on 5001, or use **pack** directly as in the samples README.

For an assessment of using **Apple's [container](https://github.com/apple/container)** (Mac VM-based OCI runtime) to reduce reliance on Docker/Colima, see **[docs/AUDIT-APPLE-CONTAINER-BUILDPACKS.md](docs/AUDIT-APPLE-CONTAINER-BUILDPACKS.md)**.

**Using a local pack build (containerd workaround):** The [octopilot/pack](https://github.com/octopilot/pack) fork includes a publish-then-pull workaround for containerd-backed daemons when building *without* `--publish`. To use it:

1. Build pack from the fork: `cd pack && make build` → binary at `pack/out/pack`.
2. Point **op** at it: `op build-push --pack /path/to/pack/out/pack` or `export PACK_CMD=/path/to/pack/out/pack` then `op build-push`.
3. **op build-push** always invokes pack with **--publish**, so the daemon export path is avoided and the workaround is not used there. The fork’s workaround applies when running **pack** directly without `--publish` (e.g. Skaffold or `pack build myapp:latest`); then a local registry (e.g. `localhost:5001`) is required. See [AUDIT-APPLE-CONTAINER-BUILDPACKS.md](docs/AUDIT-APPLE-CONTAINER-BUILDPACKS.md).

### Linux / Skaffold buildpacks

On **Linux**, `op build` and `op push` can fail with **`/layers/group.toml: permission denied`** when the Buildpacks lifecycle runs inside the builder container ([Skaffold #5407](https://github.com/GoogleContainerTools/skaffold/issues/5407)). The Docker host and group permissions are fine; the issue is how Skaffold mounts the layers volume.

**Recommendation:** Use **`op build-push`** so each artifact is built with **pack build … --publish** (no daemon export, no layers volume from Skaffold). Default repo is **localhost:5001**. Install [pack](https://buildpacks.io/docs/tools/pack/) and run from the app repo:

```bash
# Local registry with TLS (then build; op build-push defaults to localhost:5001)
# From the registry-tls repo: docker build -t registry-tls . && docker run -d -p 5001:5001 -v registry-data:/var/lib/registry --restart=unless-stopped --name registry registry-tls (or use the published image ghcr.io/octopilot/registry-tls:latest)
op build-push
```

This writes **build_result.json** and pushes all images from skaffold.yaml to the registry. Use the same **build_result.json** for watch-deployment and promote-image.

**Local HTTP registry:** If you use a local registry on port 5001 (e.g. `registry:2`), it serves HTTP. Add it to Docker’s **insecure-registries** so pack can push: Docker Desktop → Settings → Docker Engine → add `"insecure-registries": ["host.docker.internal:5001"]` (Mac) or on Linux in `/etc/docker/daemon.json` add `"insecure-registries": ["localhost:5001"]` (or the host IP), then restart Docker.

### op run and .run.yaml (quick local run)

**For local development only.** **`op run`** runs a single built image with **docker run** and preconfigured ports/env/volumes so you don't have to type `-p ... -e ...` by hand. It is **not for production** and **does not replace** docker-compose, Kubernetes (e.g. kind), or full deploy workflows—use those for multi-container or production runs.

After building (e.g. **`op build-push`** or **skaffold build**), run one artifact with **`op run <context>`**. Contexts come from your **skaffold.yaml** (one per artifact).

- **List runnable contexts:** **`op run context list`**
- **Run one context:** **`op run api`** or **`op run frontend`** (example names)

Ports, env vars, and volume mounts are **preconfigured** in **.run.yaml** (or defaults apply). Put **.run.yaml** in your app repo root (next to **skaffold.yaml**):

```yaml
# .run.yaml (optional; defaults: -p 8080:8080 -e PORT=8080 per context)
default_repo: localhost:5001
tag: latest

contexts:
  api:
    ports: ["8081:8080"]
    env:
      PORT: "8080"
  frontend:
    ports: ["8080:8080"]
    env:
      PORT: "8080"
    # volumes: ["./public:/app/public"]   # optional, for basic bind-mount
```

- **default_repo** / **tag** — used to form the image name (`<default_repo>/<image>:<tag>`). Resolved in order: this file → **SKAFFOLD_DEFAULT_REPO** / config → **.registry** `local` → **localhost:5001**.
- **contexts.*** — per-context (Skaffold artifact `context` name) **ports**, **env**, and optional **volumes** (Docker `-v` style, e.g. `host:container`). If a context is omitted, defaults are **ports: ["8080:8080"]**, **env: { PORT: "8080" }**.

**Reminder:** **op run** is for quick local dev only. For production, multi-container, or real orchestration use docker-compose, Kubernetes (kind, etc.), or your normal deploy path.

---

### Local registry (for push/build testing)

**Recommended:** use **`op start-registry`** to start the TLS registry on 5001, replace any existing registry container, copy certs out, and optionally install them for system trust (you may be prompted for your password on macOS/Linux):

```bash
op start-registry
# Optionally: op start-registry --trust-cert   # install cert (may ask for sudo)
# Or run without --trust-cert and answer y when asked to install for system trust.
```

This uses the image **ghcr.io/octopilot/registry-tls:latest** by default (override with `--image` or `REGISTRY_TLS_IMAGE`). Certs are copied to `~/.config/registry-tls/certs` (or `--certs-dir`). On macOS, use **`--user-keychain`** to add the cert to your login keychain instead of the system keychain (avoids sudo).

**Colima:** So that the Docker daemon and pack build lifecycle inside the VM trust the registry, run **`op start-registry --trust-cert-colima`**. This installs the cert into the Colima VM at `/etc/docker/certs.d/localhost:5001/` and restarts Colima by default (use **`--no-restart-colima`** to skip the restart and run `colima restart` yourself).

Alternatively, run the image manually:

```bash
docker run -d -p 5001:5001 -v registry-data:/var/lib/registry --restart=unless-stopped --name registry ghcr.io/octopilot/registry-tls:latest
```

Then use **`--default-repo localhost:5001`** (or set `local: localhost:5001` in your app’s **.registry** file). To stop and remove the container: `docker stop registry && docker rm registry`.

### Local (install from source)

```bash
cd octopilot-pipeline-tools
pip install -e ".[dev]"
op --help
op push --default-repo localhost:5001 --help
```

Using a **config file** (see [Config (.properties file)](#config-properties-file) below):

```bash
# Path via option
op --config ./pipeline.properties push

# Or path via env (same effect)
export OCTOPILOT_PIPELINE_PROPERTIES=./pipeline.properties
op push
```

### Packages (Homebrew, Chocolatey, deb, rpm)

For delivery as native packages:

- **Homebrew** (macOS/Linux): Formula in `packaging/homebrew/` — add to your tap and `brew install octopilot-pipeline-tools`.
- **Chocolatey** (Windows): nuspec and scripts in `packaging/chocolatey/` — `choco pack` then publish; install with `choco install octopilot-pipeline-tools`.
- **deb** (Debian/Ubuntu) / **rpm** (RHEL/Fedora): build with `./packaging/deb-rpm/build-deb-rpm.sh`; install the resulting `.deb` or `.rpm` from `dist/`.

See **[packaging/README.md](packaging/README.md)** for versioning, release steps, and per-format details.

### Docker (no install; includes Skaffold, pack, flux, kubectl, crane)

The published image is **multi-arch** (`linux/amd64` and `linux/arm64`). On Apple Silicon Macs, `docker pull` gets the arm64 image so you can run it without `--platform` or a separate build.

Build the image (for both architectures when using buildx):

```bash
docker build -t octopipeline .
```

Run (default entrypoint is **`op`**):

```bash
docker run --rm -v "$(pwd):/workspace" -w /workspace \
  -e SKAFFOLD_DEFAULT_REPO=localhost:5001 \
  octopipeline push --default-repo localhost:5001
```

Or use the published image from GHCR (after [publishing the container](.github/workflows/publish-image.yml)):

```bash
docker run --rm -v "$(pwd):/workspace" -w /workspace \
  -e SKAFFOLD_DEFAULT_REPO=ghcr.io/my-org \
  ghcr.io/octopilot/octopipeline push --default-repo ghcr.io/my-org
```

Using a **config file** in the container: mount the file and pass its path (env or option). Environment variables still override values from the file.

```bash
# Path via env (file must be on mounted volume, e.g. repo root)
docker run --rm -v "$(pwd):/workspace" -w /workspace \
  -e OCTOPILOT_PIPELINE_PROPERTIES=/workspace/pipeline.properties \
  octopipeline push

# Or path via option
docker run --rm -v "$(pwd):/workspace" -w /workspace \
  octopipeline --config /workspace/pipeline.properties push
```

### Testdrive with octopilot-samples

From the [octopilot-samples](https://github.com/octopilot/octopilot-samples) repo (clone it next to this one or use your own):

1. **Local registry** (if you don’t have one):
   From `registry-tls` repo: build and run the TLS registry on 5001 (see "Local registry" above).

2. **Config** in the samples repo: copy `.registry.example` to `.registry` and set `local: localhost:5001` (or use `--default-repo` below).

3. **Run the container** from the **octopilot-samples** directory (mount the repo and, for building, the Docker socket):

   ```bash
   cd octopilot-samples
   docker run --rm -v "$(pwd):/workspace" -w /workspace \
     -e SKAFFOLD_DEFAULT_REPO=localhost:5001 \
     octopipeline push --default-repo localhost:5001
   ```

   The **published** image supports both amd64 and arm64; on Apple Silicon, `docker pull ghcr.io/octopilot/octopipeline:latest` will use the arm64 image. If you **build locally** on an Apple Silicon Mac with a single-arch build, you get arm64 by default (no need for `--platform linux/amd64`).

   (`host.docker.internal` is so the container can reach a registry on the host.)

4. **Output:** `op push` runs Skaffold build (with `--push`), writes **build_result.json**, and pushes images to the given registry.

---

### GitHub Actions (or other CI)

Run the **octopipeline** container, mount your app repo, and pass the command (e.g. `op push`). Use **.registry** or **--default-repo** so push targets the right registry. The container provides Skaffold, pack, flux, kubectl, and crane so app repos don’t need to install them.

---

## .registry file (push destinations)

In the **repo root** of your **app** (not this repo), add a **`.registry`** YAML file to define where to push (local vs CI / multiple registries). Use **port 5001** for a local registry on macOS (port 5000 is often used by system services).

```yaml
# Local development (5001 avoids conflict with macOS AirPlay on 5000)
local: localhost:5001

# CI: use env interpolation so GitHub Actions (or any CI) can fill org/repo
#   ${VAR}  or  $VAR  → value of VAR
#   ${VAR:-default}   → VAR if set, else "default"
#   $$                → literal $
ci:
  - ghcr.io/${GITHUB_REPOSITORY_OWNER:-my-org}
  - europe-west1-docker.pkg.dev/${GCP_PROJECT}/${GAR_REPO}
  - docker.io/${GITHUB_ACTOR}
  - url: ghcr.io/${GITHUB_REPOSITORY_OWNER}
    name: ghcr
```

- **push** resolves registry in order: **--default-repo** → env (e.g. `SKAFFOLD_DEFAULT_REPO`) → **.registry**.
- **--destination** `local` | `ci` | `all` | `auto`: which entry to use (default **auto**: in CI use `ci`, else `local`).
- **--push-all**: after pushing to the first registry, **crane copy** the image to every other `ci` destination.

---

## Config (.properties file)

Pipeline settings (registries, Skaffold options) can come from a **`.properties`** file. You give **op** the path either with **`--config <path>`** or with the env var **`OCTOPILOT_PIPELINE_PROPERTIES=<path>`**. Environment variables override any value from the file.

**Format:** one key per line, `key=value`. Lines starting with `#` and empty lines are ignored.

**Example `pipeline.properties`** (in your app repo root or a path you pass):

```properties
# Build / push: default registry for Skaffold (op build, op push)
SKAFFOLD_DEFAULT_REPO=ghcr.io/my-org

# Optional: Skaffold profile/label/namespace
# SKAFFOLD_PROFILE=push
# SKAFFOLD_LABEL=my-app

# watch-deployment: registry where the image is deployed (per environment)
GOOGLE_GKE_IMAGE_REPOSITORY=ghcr.io/my-org
GOOGLE_GKE_IMAGE_PP_REPOSITORY=europe-west1-docker.pkg.dev/my-project/pp-registry
GOOGLE_GKE_IMAGE_PROD_REPOSITORY=europe-west1-docker.pkg.dev/my-project/prod-registry
# Fallback if env-specific above not set:
# WATCH_DESTINATION_REPOSITORY=ghcr.io/my-org

# promote-image: same keys define source/dest per env (dev → pp → prod)
# PROMOTE_SOURCE_REPOSITORY / PROMOTE_DESTINATION_REPOSITORY override for a one-off promote
```

So: **build/push** use `SKAFFOLD_DEFAULT_REPO`; **watch-deployment** and **promote-image** use the `GOOGLE_GKE_IMAGE_*` (or `WATCH_DESTINATION_REPOSITORY` / `PROMOTE_*`) keys from this file, unless overridden by env.

**How to use it:**

```bash
# Option 1: path via --config (path is relative to cwd or absolute)
op --config ./pipeline.properties build
op --config ./pipeline.properties push
op --config ./pipeline.properties watch-deployment --component my-app --environment dev
op --config ./pipeline.properties promote-image --source dev --destination pp

# Option 2: path via env (handy in CI so you don’t repeat --config)
export OCTOPILOT_PIPELINE_PROPERTIES=./pipeline.properties
op push
op watch-deployment --component my-app --environment dev
```

**Precedence:** Values from the file are loaded first; then every env var (including `SKAFFOLD_DEFAULT_REPO`, `GOOGLE_GKE_IMAGE_*`, etc.) overrides the same key from the file. So you can ship a default `pipeline.properties` and override only what’s needed in CI (e.g. `SKAFFOLD_DEFAULT_REPO`) via env.

Tool versions used in the container image are listed in [DEPENDENCIES.md](DEPENDENCIES.md). For lint and test commands, see [CONTRIBUTING.md](CONTRIBUTING.md).

---

## Compatibility

The CLI is compatible with workflows that use a `.properties`-style config and Skaffold (including Skaffold + Buildpacks). Use the package or Docker image so each app repo does not need to ship its own pipeline scripts.

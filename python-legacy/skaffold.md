# Skaffold with op and Buildpacks

This document explains how **Skaffold** is used by the **op** (OctoPilot pipeline tools) CLI, how your **skaffold.yaml** drives builds and runs, and how it fits with **Cloud Native Buildpacks**, **op build-push**, **op run**, and related config (**.registry**, **.github/octopilot.yaml**, **build_result.json**).

---

## 1. What is Skaffold and why does op use it?

**Skaffold** is a tool that builds, pushes, and (optionally) deploys container images from a declarative config file—**skaffold.yaml**—in your app repo. It supports multiple builders (Docker, Buildpacks, Kaniko, etc.) and multiple artifacts per repo.

**op** uses Skaffold as the **source of truth** for:

- **What to build:** the list of artifacts (images) and how each is built (buildpacks vs docker).
- **Where to build from:** the **context** directory for each artifact (e.g. `api/`, `frontend/`).
- **Default registry:** op passes **SKAFFOLD_DEFAULT_REPO** (or equivalent) into Skaffold when running **op build** and **op push**.

op does **not** replace Skaffold; it **wraps** it for build/push, adds **op build-push** (pack with `--publish`) when Skaffold + Buildpacks fail on Mac/Linux, and adds **op run** (local dev), **watch-deployment**, and **promote-image** using **build_result.json**.

---

## 2. skaffold.yaml structure (what op cares about)

op reads **skaffold.yaml** in your app repo root (or the path you pass with **--skaffold-file**). The parts that matter for op are under **build** and **artifacts**.

### Minimal example (Buildpacks only)

```yaml
apiVersion: skaffold/v2beta29
kind: Config
metadata:
  name: myapp

build:
  local: {}
  artifacts:
    - image: myapp-api
      context: api
      buildpacks:
        builder: ghcr.io/octopilot/builder-jammy-base:latest
    - image: myapp-frontend
      context: frontend
      buildpacks:
        builder: ghcr.io/octopilot/builder-jammy-base:latest

deploy: {}
```

- **build.artifacts** — list of images to build. Each has:
  - **image** — image name (no registry). op forms the full image as `<default_repo>/<image>:<tag>`.
  - **context** — directory containing the app source (e.g. `api`, `frontend`). Must exist relative to the repo root. This is the **build context** for that artifact; Procfile and project.toml live in this directory when using buildpacks (see [procfile.md](procfile.md)).
  - **buildpacks** or **docker** — how to build:
    - **buildpacks.builder** — For the **op** toolchain you must use **`ghcr.io/octopilot/builder-jammy-base`** (with a tag such as `latest`). Other builders (e.g. `paketobuildpacks/builder-jammy-base`, `gcr.io/buildpacks/builder`) are **not compatible** with op. No Dockerfile; the buildpack uses Procfile/project.toml in the context.
    - **docker.dockerfile** — path to a Dockerfile in that context. op **build-push** skips these; **op build** and **op push** still build them via Skaffold.

- **build.local** — empty `{}` is typical; op and Skaffold use the local Docker (or pack, for **op build-push**) environment.

- **deploy** — op does not run Skaffold deploy; you use Flux, Helm, or your own deploy. So **deploy: {}** is fine.

### Hybrid example (Buildpacks + Docker)

```yaml
build:
  local: {}
  artifacts:
    - image: myapp-frontend
      context: frontend
      docker:
        dockerfile: Dockerfile
    - image: myapp-api
      context: api
      buildpacks:
        builder: ghcr.io/octopilot/builder-jammy-base:latest
```

- **frontend** is built with **Docker** (Dockerfile in `frontend/`). **op build-push** only builds buildpacks artifacts, so it would build only **api**; use **op build** or **skaffold build** to build both.
- **api** is built with **Buildpacks**; Procfile/project.toml in **api/** apply (see [procfile.md](procfile.md)).

---

## 3. How each op command uses Skaffold

| Command | How it uses skaffold.yaml |
|--------|---------------------------|
| **op build** | Runs **skaffold build**. Passes **--default-repo** (from config / **SKAFFOLD_DEFAULT_REPO** / .registry). Builds **all** artifacts (buildpacks and docker). |
| **op push** | Runs **skaffold build** with a push profile, pushes to the resolved registry, writes **build_result.json**. Uses the same **--default-repo** resolution. Builds all artifacts. |
| **op build-push** | **Does not run Skaffold.** Reads skaffold.yaml, finds every artifact with **buildpacks.builder**, and for each runs **pack build … --publish** with that artifact’s **context** and **image** name. Writes **build_result.json**. Skips artifacts that use **docker** only. Use when **op build** / **op push** fail (Mac containerd digest, Linux /layers permission). |
| **op run** | Reads skaffold.yaml to get **all** artifacts’ **context** and **image** names. **op run context list** lists contexts; **op run &lt;context&gt;** runs the image for that context with **docker run** and options from **.github/octopilot.yaml** (local dev only). |
| **op watch-deployment** | Does not read skaffold.yaml; uses **build_result.json** (produced by **op push** or **op build-push**). |
| **op promote-image** | Does not read skaffold.yaml; uses **build_result.json** and config for source/destination registries. |

So: **op build** and **op push** delegate the build to **Skaffold**. **op build-push** and **op run** only **read** skaffold.yaml to know what to build or run.

---

## 4. Buildpacks in Skaffold: context, builder, Procfile, project.toml

When an artifact has **buildpacks.builder** in skaffold.yaml:

- Skaffold (or **op build-push** via pack) runs the buildpack lifecycle against the artifact’s **context** directory.
- The buildpack looks for **Procfile**, **project.toml**, and language files (e.g. **package.json**, **requirements.txt**) in the **root of that context** (e.g. **api/**, **frontend/**). It uses them to set the run image and start command.

So the **context** in skaffold.yaml is the same directory where you put **Procfile** and **project.toml** for that artifact. For full detail, see [procfile.md](procfile.md).

**Builder image (op toolchain):** Only **`ghcr.io/octopilot/builder-jammy-base`** may be used with the op toolchain. Other builders (e.g. `paketobuildpacks/builder-jammy-base`, `gcr.io/buildpacks/builder`) are **not compatible** with op's Pack integration and must not be used.

---

## 5. When Skaffold works vs when to use op build-push

- **When Skaffold works:** On many Linux CI environments, **op build** and **op push** (which run **skaffold build**) succeed. Use them so all artifacts (buildpacks and docker) are built in one go.

- **When it fails and you need op build-push:**
  - **macOS:** Docker Desktop and Colima use a containerd-backed daemon; Skaffold + Buildpacks often fail with a digest error when writing the image to the daemon ([pack#2272](https://github.com/buildpacks/pack/issues/2272)). Skaffold invokes pack without **--publish**, so the image is always exported to the daemon first.
  - **Linux:** Sometimes **/layers/group.toml: permission denied** inside the builder ([Skaffold #5407](https://github.com/GoogleContainerTools/skaffold/issues/5407)) due to how Skaffold mounts the layers volume.

**op build-push** bypasses Skaffold for the **build** step: it runs **pack build … --publish** for each buildpacks artifact, so the image goes straight to the registry (no daemon export, no Skaffold layers mount). Use **op build-push** in those environments. It only builds artifacts that have **buildpacks.builder**; docker artifacts must be built separately (e.g. **skaffold build** or **op build**) if needed.

---

## 6. Default repo and registry (Skaffold + op)

Skaffold’s **--default-repo** (or **SKAFFOLD_DEFAULT_REPO**) is the registry/repo prefix used to tag images: `<default_repo>/<image>:<tag>`.

op resolves the default repo in this order (for **op build**, **op push**, and when forming the image name for **op run**):

1. **--default-repo** / **--repo** (CLI option, where applicable).
2. **SKAFFOLD_DEFAULT_REPO** (env or from **pipeline.properties** via **--config**).
3. **.registry** file: **local** (for local dev) or **ci** (in CI, e.g. when **GITHUB_ACTIONS** is set), depending on **--destination** for **op push**.
4. For **op run**: **.github/octopilot.yaml** **default_repo** (if present), then the same as above, then **localhost:5001**.

So your **skaffold.yaml** does **not** contain the registry; it only has **image** names. The registry comes from op’s config, .registry, or env. See [README: .registry file](README.md#registry-file-push-destinations) and [README: Config](README.md#config-properties-file).

---

## 7. build_result.json (output of build/push, input for watch/promote)

**op push** and **op build-push** write **build_result.json** in the repo root (or the path you pass). It lists the built image tags (e.g. `localhost:5001/myapp-api:latest`). **op watch-deployment** and **op promote-image** read this file to know which image to watch or promote; they do not read skaffold.yaml.

So the flow is: **skaffold.yaml** (and optionally **.github/octopilot.yaml**) define what to build and how to run locally; **build_result.json** is the handoff from build/push to deploy/watch/promote.

---

## 8. op run and skaffold contexts (local dev only)

**op run** uses skaffold.yaml only to get the list of **context** names and **image** names. It then:

- **op run context list** — prints each artifact’s **context** (e.g. `api`, `frontend`).
- **op run &lt;context&gt;** — runs **docker run** with the image `<default_repo>/<image>:<tag>`, using ports/env/volumes from **.github/octopilot.yaml** (or defaults). Local dev only; does not replace docker-compose, Kubernetes (e.g. kind), or production deploy. See [README: op run and .github/octopilot.yaml](README.md#op-run-and-githuboctopilotyaml-quick-local-run).

---

## 9. Summary

| Topic | Where it’s defined | Used by |
|-------|--------------------|--------|
| What to build (artifacts, context, builder) | **skaffold.yaml** **build.artifacts** | **op build**, **op push**, **op build-push**, **op run** |
| How to run (start command) | **Procfile** / **project.toml** in each artifact’s **context** | Buildpack (during build); see [procfile.md](procfile.md) |
| Where to push (registry) | **.registry**, **pipeline.properties**, **SKAFFOLD_DEFAULT_REPO**, **--default-repo** | **op build**, **op push**; **op run** also uses **.github/octopilot.yaml** **default_repo** |
| Local run (ports, env, volumes) | **.github/octopilot.yaml** **contexts.*** | **op run** (local dev only) |
| Built image refs for CD | **build_result.json** (written by **op push** / **op build-push**) | **op watch-deployment**, **op promote-image** |

For Procfile and project.toml in buildpack contexts, see **[procfile.md](procfile.md)**. For the full op command set and usage, see **[README.md](README.md)**.

---

## 10. References

- [Skaffold documentation](https://skaffold.dev/docs/)
- [Skaffold build configuration](https://skaffold.dev/docs/references/yaml/#build-artifacts)
- [Skaffold Buildpacks builder](https://skaffold.dev/docs/pipeline-stages/builders/#buildpacks)
- [Buildpacks (buildpacks.io)](https://buildpacks.io/)
- [Paketo Buildpacks](https://paketo.io/)

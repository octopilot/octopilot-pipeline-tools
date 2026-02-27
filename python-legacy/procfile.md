# Procfile with Skaffold, Buildpacks, and op

This document explains how **Procfile** fits into builds that use **Skaffold**, **Cloud Native Buildpacks**, and the **op** (OctoPilot pipeline tools) CLI. It covers when and why to use a Procfile, how it relates to the container image produced by buildpacks (and to using a Dockerfile instead), and how to use one or more Procfiles in a repo that has multiple artifacts or stacks. **Prefer buildpacks (and Procfile/project.toml) where possible; use a Dockerfile only when buildpacks cannot meet your needs** (see section 8).

---

## 1. What is a Procfile?

A **Procfile** is a plain-text file that declares **process types** and the command to run for each. It originated with Heroku and is supported by many Cloud Native Buildpacks (e.g. Paketo, Heroku buildpacks). The format is one line per process type:

```text
process_type: command
```

- **process_type** is a name (e.g. `web`, `worker`, `clock`). **`web`** is the default process type for HTTP services and is what most platforms run by default.
- **command** is the executable and arguments that start the process (e.g. `uvicorn main:app --host 0.0.0.0`).

The buildpack uses the Procfile at **build time** to record the start command into the image metadata. At **run time**, the container runtime (Kubernetes, Docker, etc.) executes that command. If you don’t provide a Procfile, the buildpack may infer a default command from your app (e.g. from `package.json` scripts or language conventions).

---

## 2. Where does the Procfile live?

The Procfile must live in the **root of the build context** for the artifact that uses it. In Skaffold terms, that’s the directory specified by **`context`** for that artifact in `skaffold.yaml`.

**Example:** If `skaffold.yaml` has:

```yaml
artifacts:
  - image: myapp-api
    context: api
    buildpacks:
      builder: ghcr.io/octopilot/builder-jammy-base:latest
```

then the Procfile for that artifact is **`api/Procfile`**. (The op toolchain requires `ghcr.io/octopilot/builder-jammy-base`; other builders are not compatible.) Same for `frontend/` — a Procfile for the frontend artifact would be **`frontend/Procfile`**.

So: **one Procfile per artifact** that uses buildpacks and needs an explicit process type. Each artifact has its own context directory; put the Procfile in that directory’s root.

---

## 3. Buildpacks vs Dockerfile: no “implicit Dockerfile”

Buildpacks **do not use a Dockerfile**. They use a **builder** (a stack = build image + run image). The lifecycle:

1. **Build:** Runs buildpacks against your app source in the **build image**, produces layers and metadata.
2. **Export:** Assembles the final image from the **run image** (the base) plus your app layers and metadata (including the start command).

So there is no “implicit Dockerfile” in the sense of a file you could edit. What you get instead is:

- An **implicit run image** chosen by the builder (e.g. `paketobuildpacks/run-jammy-base`). That run image defines the OS and runtime (e.g. Python, Node), but not your app’s start command.
- An **implicit or explicit start command.** If you don’t provide a Procfile (or process in `project.toml`), the buildpack may set a default (e.g. from `package.json` or `requirements.txt`). If you **do** provide a Procfile, the buildpack writes that command into the image as the default process type (**web** is usually the one that gets run by default in Kubernetes/Docker).

So:

- **With buildpacks:** You control the **start command** via **Procfile** (or `project.toml` process types). You do **not** write a Dockerfile; the “base” and runtime come from the builder’s run image.
- **With a Dockerfile:** You control the **start command** via **CMD** / **ENTRYPOINT** in the Dockerfile. There is no Procfile for that artifact.

---

## 4. When to use a Procfile

Use a **Procfile** when:

- The buildpack **does not** infer the right start command (e.g. no `package.json` start script, or you want a different executable).
- You want to **override** the default (e.g. bind to `0.0.0.0`, use a specific port, or pass flags).
- You want **multiple process types** in the same image (e.g. `web` and `worker`); in Kubernetes you’d run each as a separate Deployment or Job using the same image but different process types.

You can **omit** a Procfile when the buildpack already infers the correct command (e.g. standard Node or Python apps with the usual scripts or entrypoints).

---

## 5. Single vs multiple process types

**Single process type (typical API):**

```text
web: uvicorn main:app --host 0.0.0.0 --port ${PORT:-8080}
```

**Multiple process types (e.g. web + worker):**

```text
web: uvicorn main:app --host 0.0.0.0 --port ${PORT:-8080}
worker: python -m tasks.run
```

- **web** is the default for HTTP and is what most platforms run when you don’t specify a process type.
- Other types (**worker**, **clock**, etc.) are for background jobs or secondary processes. In Kubernetes you’d typically run them as separate Deployments or Jobs, each specifying the process type (how you do that depends on your manifest or Helm chart; the image contains all process types).

---

## 6. Procfile vs project.toml

| Mechanism   | Use for |
|------------|---------|
| **Procfile** | Defining or overriding the **run** command (process types). Simple, one line per type. |
| **project.toml** | **Build** and **launch** configuration: env vars for the buildpack (e.g. `BP_WEB_SERVER=nginx`, `BP_WEB_SERVER_ROOT=public`), and optionally process types in TOML form. |

- Use **Procfile** when you only need a custom start command and the buildpack supports it.
- Use **project.toml** when you need buildpack-specific options (e.g. static site server, paths). You can define process types in **project.toml** instead of a Procfile if you prefer TOML; both are in the same build context root.

Static frontends (e.g. NGINX serving `public/`) often need only **project.toml** (to set `BP_WEB_SERVER`, `BP_WEB_SERVER_ROOT`) and **no** Procfile, because the buildpack’s default for that stack is correct. API backends often need a **Procfile** to set the web command (e.g. uvicorn, gunicorn).

---

## 7. Per-artifact and per-stack: one context, one Procfile (or none)

In Skaffold you have one or more **artifacts**. Each artifact has:

- An **image** name
- A **context** directory (e.g. `api`, `frontend`)
- A **builder**: either **buildpacks** (with a `builder` image) or **docker** (with a `dockerfile` path)

So:

- **Per artifact:** Each artifact has exactly one context. If that artifact uses **buildpacks**, you may put at most one **Procfile** in that context’s root (and optionally **project.toml**). If the artifact uses **docker**, you use a **Dockerfile** in that context and no Procfile.
- **Per “stack”:** In buildpacks terminology, a **stack** is the pair (build image + run image) provided by the builder. You don’t “choose a Procfile per stack”; you choose a **builder** per artifact in `skaffold.yaml`, and the Procfile (if any) in that artifact’s context is used when that builder runs. Different artifacts can use different builders (e.g. different Paketo builders) and each can have its own Procfile in its own context.

**Example (two buildpack artifacts, one Procfile only for the API):**

```yaml
# skaffold.yaml
artifacts:
  - image: myapp-frontend
    context: frontend
    buildpacks:
      builder: ghcr.io/octopilot/builder-jammy-base:latest
  - image: myapp-api
    context: api
    buildpacks:
      builder: ghcr.io/octopilot/builder-jammy-base:latest
```

- **frontend:** No Procfile; use **project.toml** with `BP_WEB_SERVER=nginx`, `BP_WEB_SERVER_ROOT=public` (or similar) so the buildpack serves static files.
- **api:** **api/Procfile** with `web: uvicorn main:app --host 0.0.0.0 --port ${PORT:-8080}`.

---

## 8. When to use buildpacks vs your own Dockerfile

**Prefer buildpacks where possible.** Use Buildpacks (with Procfile or project.toml as needed) for each artifact that fits the model: they give you a consistent stack, automatic dependency detection, and no Dockerfile to maintain. Only add a **Dockerfile** for an artifact when buildpacks cannot do the job.

### Why you might need a Dockerfile

Use a Dockerfile for an artifact when one of the following applies:

- **Your language or runtime isn’t supported by buildpacks.** For example, **Rust** does not yet have broad, production-ready Cloud Native Buildpack support in the mainline Paketo/Heroku stacks. For a Rust API or binary, you build with a **Dockerfile** (e.g. multi-stage: build with a Rust image, copy the binary into a minimal run image) and define **CMD** in that Dockerfile. No Procfile for that artifact.
- **Your project layout or build is too specific for the automatic setup.** Buildpacks expect certain conventions (e.g. `package.json` at context root, or `requirements.txt`, or a standard static layout). If you have a non-standard layout, custom build steps, or artifacts (e.g. generated files, native libs, or tooling) that the buildpack doesn’t account for, the automatic Procfile/buildpack path may not be enough. In that case a **Dockerfile** lets you encode the exact build and run steps (COPY, RUN, CMD/ENTRYPOINT) for that artifact.
- **You need full control over the image.** Some teams need a fixed base image, specific system packages, or security hardening that’s easier to express in a Dockerfile than via buildpack env vars or custom buildpacks. That’s a valid reason to use a Dockerfile for that artifact.

In all these cases, the **start command** for that artifact comes from **CMD** / **ENTRYPOINT** in the Dockerfile; there is no Procfile for that artifact.

### How Dockerfile artifacts fit in

- **One Dockerfile per artifact:** e.g. `frontend/Dockerfile`, `api/Dockerfile`. In `skaffold.yaml` you set `context` and `docker.dockerfile` for each artifact.
- **Single Dockerfile (multi-stage):** One Dockerfile at repo root (or in one context) can use multi-stage builds to produce one or more images. Skaffold can target those images; each stage uses CMD/ENTRYPOINT, not Procfile.
- **Hybrid repo (buildpacks + Docker):** Some artifacts use buildpacks (Procfile/project.toml), others use a Dockerfile. For example: API in a supported language with buildpacks, frontend or Rust service with a Dockerfile.

**Example (hybrid: buildpacks for API, Dockerfile for Rust or custom frontend):**

```yaml
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

- **frontend:** Built with **Docker** (e.g. custom toolchain or Rust); **frontend/Dockerfile** defines the image and start command. No Procfile.
- **api:** Built with **buildpacks**; **api/Procfile** (and/or **api/project.toml**) defines the start command.

**Summary:** Use **buildpacks natively where possible**; use a **Dockerfile only when necessary** (unsupported runtime, non-standard layout, or need for full control). Procfile applies only to buildpacks-built artifacts.

---

## 9. How op uses Procfile (op build-push, op build, op push)

- **op build** and **op push** run **Skaffold** (`skaffold build`). Skaffold invokes the builder for each artifact: for buildpacks artifacts it runs **pack** (or the Skaffold buildpacks integration). The Procfile in each artifact’s context is read by **pack** / the buildpack during the build; op does not read the Procfile itself.
- **op build-push** bypasses Skaffold for the **build** step: it reads **skaffold.yaml**, finds every artifact that has **buildpacks.builder**, and for each runs **pack build** with `--path` set to that artifact’s context. **pack** then runs the buildpack lifecycle; the buildpack sees the Procfile (and project.toml) in that context and uses them. So:
  - **Procfile** is always in the **artifact’s context directory** (same as in Skaffold).
  - **op build-push** does not parse or modify the Procfile; it just runs **pack** against each buildpacks context. The buildpack inside the builder is what uses the Procfile.

If an artifact is defined with **docker** (no buildpacks), **op build-push** **skips** it (it only builds artifacts that have `buildpacks.builder`). To build Dockerfile-based artifacts you use **op build** (skaffold build) or **skaffold build** directly.

---

## 10. Summary table

| Artifact built with | Start command from        | Procfile? |
|---------------------|---------------------------|-----------|
| Buildpacks          | Procfile or project.toml (or buildpack default) | Optional; use when you need an explicit/custom command. |
| Dockerfile          | CMD / ENTRYPOINT in Dockerfile | No; Procfile is ignored for that artifact. |

| op command    | Buildpacks artifacts      | Docker artifacts      |
|---------------|---------------------------|------------------------|
| op build-push | Built with **pack** (Procfile used by buildpack) | Skipped                 |
| op build      | Built via Skaffold (pack) | Built via Skaffold (docker) |
| op push       | Built via Skaffold (pack) | Built via Skaffold (docker) |

---

## 11. References

- [Buildpacks project descriptor (project.toml)](https://buildpacks.io/docs/reference/config/project-descriptor/)
- [Heroku Procfile](https://devcenter.heroku.com/articles/procfile)
- [Paketo process types](https://paketo.io/docs/howto/configuration/#procfile)
- [Skaffold buildpacks](https://skaffold.dev/docs/pipeline-stages/builders/#buildpacks) and [Skaffold artifacts](https://skaffold.dev/docs/references/yaml/#build-artifacts)

For a short recap in this repo, see the README section [Procfile and project.toml (Buildpacks)](README.md#procfile-and-projecttoml-buildpacks). For how Skaffold and skaffold.yaml fit with op and buildpacks, see [skaffold.md](skaffold.md).

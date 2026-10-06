# Image launch and builder tags

Status: active (2026-10-06). This is the source of truth for where a built
binary lives inside an Octopilot image, which builder tag produces that
layout, and which repository has to move when the tag changes.

The failure this document closes: generated images contain a binary that the
CNB launcher does not start. Callers then see `no default process type`,
`chdir /workspace: permission denied`, or `/cnb/process/<name>` missing.
The binary is usually on disk. The process metadata, the builder that
produced it, and the tag the next repo pulls are three different things,
and they have been allowed to drift.

## Contract

A Rust app image is started by the CNB launcher, not by a path on `PATH`.
The user-facing version of this contract is
[actions/docs/rust-images.md](https://github.com/octopilot/actions/blob/main/docs/rust-images.md).

| Piece | Required value |
|---|---|
| File | `/workspace/bin/<cargo-target-name>` |
| Process command | `bin/<cargo-target-name>` (relative; launcher `chdir`s to `/workspace` first) |
| Process types | one named after the binary, plus `web` with `default = true` for the binary that should boot |
| `launch.toml` | `<buildpack-layers>/launch.toml`, command as a TOML string |
| App dir mode | `/workspace` is `0755` so the run uid can `chdir` |
| Run image | empty `CMD`. A leftover `CMD ["/bin/bash"]` becomes the launcher command and there is no default process |

`BP_DEFAULT_PROCESS` is a Paketo environment-buildpack variable. `octopilot/rust`
does not read it. `BP_RUST_BINARY_NAME` is consulted only when Cargo's JSON
message stream reports no executable. Setting either in `skaffold.yaml` does
not choose the default process.

The `op` image is a Go build, not a Rust build. Paketo `go-build` puts the
binary in a launch layer and exposes it as `/cnb/process/op`. Actions override
the entrypoint to `/bin/sh` and exec `op`. That works because the image `PATH`
starts with `/cnb/process`, so `op` is the launcher symlink. It is not
`/workspace/bin/op` and it is not on the Ubuntu `PATH`.

Charts that omit `command` use the image default process. That default is
whichever binary the buildpack marked `default = true`.

## What each published buildpack actually does

Pulled from GHCR on 2026-10-06 (the buildpack layer, not the git tree).

| Tag | Binary on disk | Launch metadata |
|---|---|---|
| `ghcr.io/octopilot/rust:0.1.0` | Renamed to `/workspace/bin/web` | Written to `<layers>/launch/launch.toml`. The lifecycle reads `<layers>/launch.toml`, so the image has no processes. |
| `ghcr.io/octopilot/rust:0.1.6` | `/workspace/bin/<name>` | `command = ["bin/<name>"]` (TOML array). Lifecycle 0.21.1 rejects it (`[]any` vs string). Binary present, no processes. |
| `ghcr.io/octopilot/rust:0.1.13` | `/workspace/bin/<name>` after a prune that keeps only `bin/` | String command, named process plus `web` default, `/workspace` chmod `0755`. |
| `ghcr.io/octopilot/rust:0.1.14` | Same as 0.1.13, plus cargo-sweep on the cache layer | Inside builder `rust-builder-c3c756a` and `:latest`. |

`0.1.12` is the prune that left `/workspace` mode `700`. Do not ship it.
`0.1.11` has named processes but not the mode fix.

## Builder images

GHCR `:latest` is still the July publish. Local `builder-jammy-base` `main`
(`c3c756a`, not pushed) is a merge of that fork's old `main` with `rust-builder`
replayed onto Paketo `main` at `7e69241` (2026-10-06). The tree pins
`octopilot/rust` 0.1.14, `octopilot/helm` 0.1.3, lifecycle 0.21.22, and the
current Paketo buildpacks (Go 4.22.0, build image `0.1.280`). The run image
stays upstream's `run-jammy-base:latest`. The February rust 0.1.0 pin is gone.

| Tag | Config digest (prefix) | `octopilot/rust` | Lifecycle | Notes |
|---|---|---|---|---|
| `latest` | `sha256:b8db544fac01` | **0.1.14** | 0.21.22 | Same image as `rust-builder-c3c756a`. Also helm 0.1.3. |
| `rust-builder-c3c756a` | `sha256:b8db544fac01` | **0.1.14** | 0.21.22 | Current consumer pin. Published from `builder-sync` at `c3c756a`. |
| `rust-builder-d5eb42a` | different | **0.1.6** | 0.21.1 | Retired. Binary at `/workspace/bin`, no launch processes. |
| local `main` `c3c756a` | not published | **0.1.14** | 0.21.22 | 75 Paketo commits past the July base, plus the octopilot additions. |

The publish workflow refuses to push unless `octopilot/rust` is `>= 0.1.13`
and `octopilot/helm` is in the order. A push of this `main` to origin also
publishes `:latest` directly (`push-image-ghcr.yml` tags `latest` on
`refs/heads/main`). It does not create an immutable `rust-builder-<sha>` tag.
Dispatch the workflow with an explicit tag first if that pin should exist
before `:latest` moves.

## Tag policy

One immutable builder tag is the pin. `latest` is only the moving alias of
the last successful publish, and it is not what `skaffold.yaml` files name.

**Current pin:** `ghcr.io/octopilot/builder-jammy-base:rust-builder-c3c756a`

Use it in:

- `octopilot-pipeline-tools/skaffold.yaml` (the `op` image)
- integration fixtures under `tests/integration/fixtures/`
- `igniteflux/skaffold.yaml` (controller and chart)
- the default builder in `actions/detect-contexts/detect.py`
- the default builder in `actions/integration-build-artifact/action.yml`

Do not use:

- `rust-builder-d5eb42a` (rust 0.1.6)
- `paketobuildpacks/builder-jammy-base` (no `octopilot/rust`, and `op`'s pack
  integration is not validated against it)
- `:latest` inside a repo pin (it moves when someone publishes)

`op` image tags are not aliases of each other. Digests on 2026-10-06:

| Tag | Manifest child (amd64 list entry) |
|---|---|
| `ghcr.io/octopilot/op:latest` | `sha256:84156f1e7f4d…` |
| `ghcr.io/octopilot/op:main` | `sha256:438591433c1d…` |
| `ghcr.io/octopilot/op:v1.1.2` | `sha256:61a4058447b8…` |

`v1.1.3` does not exist. Actions default `op_version` to **`v1.1.2`**, which
matches `actions/octopilot/action.yml` and `pipeline.yml`. Docs that still say
`v1.0.13` or `v1.0.17` are stale and have been corrected where they state the
default.

Pushing pipeline-tools `main` tags the image with `GITHUB_REF_NAME` (`main`).
It does not move `latest` or the `v1.1.2` release tag. `#40` removed the extra
alias. igniteflux currently passes `op_version: latest` because `op:main` was
left behind that change. Leave that override in place until the rebuilt
`op:main` exists, then point igniteflux at `main` or the next release tag.
Pointing it at `v1.1.2` today does not pick up the builder pin below.

pipeline-tools CI path filters did not include `skaffold.yaml`, so a builder
pin change never rebuilt `op`. The push and pull_request filters now include
`skaffold.yaml` and `base/**`.

## Repositories

### octopilot-pipeline-tools (this repo)

Builds `op` with the Go buildpack on top of `base/` (Ubuntu + Docker CLI).
The Rust buildpack in the builder does not compile `op`, but the same builder
image is what every Rust repo copies. Pinning `op` at `rust-builder-d5eb42a`
kept that tag looking supported.

Done in this change: pin moved to `rust-builder-c3c756a`; CI paths include
`skaffold.yaml` and `base/**`.

### builder-jammy-base

Local `main` already contains the rebased `rust-builder` (merge `c3c756a`).
It is ahead of `origin/main` and has not been pushed. Pushing it runs
`push-image-ghcr.yml` and moves `:latest`.

Next publish:

1. `workflow_dispatch` on `push-image-ghcr.yml` with an explicit tag
   (for example `rust-builder-c3c756a`) so there is an immutable name before
   `:latest` moves. A push of `main` itself tags only `latest`.
2. Confirm the image label `io.buildpacks.buildpack.order` contains
   `octopilot/rust` `0.1.14`, `octopilot/helm` `0.1.3`, and lifecycle 0.21.22.
3. Move every consumer pin from `rust-builder-c3c756a` to that new tag.

### rust (the buildpack)

`0.1.13` / `0.1.14` place the binary correctly for a single crate with no
`build.rs` (igniteflux is in this set). Remaining buildpack work, before
calling 0.1.14 the new floor:

- Copy only Cargo targets whose `kind` contains `bin`. `cargo build` also
  emits build-script executables. Those get a process, and the first artifact
  becomes the default `web` process.
- Honor `BP_RUST_BINARY_NAME` as the default process even when JSON discovery
  succeeds.
- Fail the build if the prune's `tar` restore does not put `bin/<name>` back
  in the app dir. `set -e` does not see a failure in that pipeline, so a
  failed restore still prints "Rust build complete".
- `scripts/package.sh --split-images` sets `CMD ["/workspace/bin/<name>"]`.
  The image entrypoint is the launcher, so Docker passes that absolute path
  as a process type. Split images need a process type, not a `CMD` path.

### actions

`detect-contexts` used to default a pack artifact with no `builder:` to
`paketobuildpacks/builder-jammy-base`. That image cannot run `octopilot/rust`.
The default is now the current pin. An explicit `builder:` in skaffold is
still passed through unchanged.

`integration-build-artifact` repeats the same default in its parse step.
Both strings have to move together when the pin moves. The unit-test mirror
is `actions/tests/unit/test_integration_build_artifact.py`.

`op_version` stays `v1.1.2` until a release is cut from an `op` image built
with the new builder pin.

### igniteflux

The controller image is Rust on a custom run image (`base/Dockerfile`:
Ubuntu Jammy, git, kustomize, empty `CMD`, explicit `PATH`). The chart is
the helm buildpack in the same builder. Both artifacts now use
`rust-builder-c3c756a`.

`op_version: latest` in `.github/workflows/ci.yml` stays until `op:main` is
rebuilt from this pin. The chart does not set a container command, so the
pod runs the image default process (`web` → `bin/igniteflux` for this crate).

## Run images

`igniteflux-base` and `op-base` are `ubuntu:jammy`, not
`paketobuildpacks/run-jammy-base`. The lifecycle still injects
`/cnb/lifecycle/launcher` at export. Consequences:

- `op:v1.0.0` runs as user `cnb`. `op:latest` has an empty user (root).
  A mode `700` `/workspace` only fails for the non-root uid. 0.1.13 makes
  the directory traversable either way; do not depend on root.
- The run image must not set `CMD`. igniteflux-base already clears it.
- `PATH` on the run image is what the launcher starts from. igniteflux-base
  sets a normal Ubuntu `PATH`. Relative `bin/<name>` does not need the
  binary directory on `PATH`.

Prefer a new run image `FROM paketobuildpacks/run-jammy-base` when the
service does not need packages that image lacks. Custom Ubuntu bases stay
valid if `CMD` is empty and `/workspace` is `0755`.

## Order of work

1. Done: pins, detect/action defaults, CI path filters, publish guard,
   doc defaults for `op_version`.
2. Done locally, not pushed: `builder-jammy-base` `main` is Paketo `7e69241`
   plus the rust 0.1.14 / helm 0.1.3 additions (merge `c3c756a`). A normal
   push works; it is not a force-push. That push also publishes `:latest`.
3. Buildpack follow-ups listed above, then release `octopilot/rust` past
   0.1.14 if the behavior changes. The rebased builder stays on 0.1.14 until
   that image exists. If the floor moves, bump the publish guard with it.
4. Publish an immutable tag from the rebased tree, confirm the order label,
   then move consumer pins off `rust-builder-c3c756a`.
5. Push pipeline-tools `main` so CI rebuilds `op:main` on the new builder.
   Compare `op:main` to the previous digest before pointing igniteflux
   `op_version` at `main`.

## Checking a builder without a full pull

```bash
# Order label. rust version must be >= 0.1.13 and helm must be listed.
TOKEN=$(curl -s "https://ghcr.io/token?service=ghcr.io&scope=repository:octopilot/builder-jammy-base:pull" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
curl -s -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
  "https://ghcr.io/v2/octopilot/builder-jammy-base/manifests/rust-builder-c3c756a" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["config"]["digest"])'
```

Fetch that config blob and read `io.buildpacks.buildpack.order`.

Local workflow check (does not push). `ubuntu-24.04` is not in act's
default platform map, so pass one. If `GITHUB_TOKEN` in the environment is
rejected by GitHub, unset it or act cannot clone even public actions.

```bash
# pipeline-tools: list jobs. A dry-run of detect also clones
# octopilot/actions, which needs a token that GitHub accepts.
act -l -W .github/workflows/ci.yml

# builder-jammy-base on main: the guard must fail closed, before login or publish.
env -u GITHUB_TOKEN act workflow_dispatch -j push \
  -W .github/workflows/push-image-ghcr.yml \
  -P ubuntu-24.04=ghcr.io/catthehacker/ubuntu:act-latest \
  --container-architecture linux/arm64
```

Checked on 2026-10-06: `act -l` lists the six pipeline-tools CI jobs. The
builder job, run as above against `main`, stopped at "Require a builder that
can launch Rust binaries" with `octopilot/rust '0.1.0' is below 0.1.13`.
The same check against branch `rust-builder`'s toml (`0.1.14`, helm present)
would allow the publish.

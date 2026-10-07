# BRRTRouter product release

Status: in progress (2026-10-07). This is the plan for an official release of a
BRRTRouter product. PriceWhisperer is the first product. Tilt stays the
local dev loop.

Related: [image-launch-and-builder-tags.md](image-launch-and-builder-tags.md)
is the CNB launch contract. This document is the product-suite contract.
The two are different image shapes, and a PriceWhisperer service must not
be forced onto the CNB layout.

## Two loops

| | Local dev | Official release |
|---|---|---|
| Who builds | Tilt on the developer machine (ms02) | GitHub Actions, `octopilot/actions` pipeline `v1.0.0` |
| How the binary is produced | Host `cargo` (`pricewhisperer build microservice`) | `cargo` inside the image build |
| How the image is packaged | Render `Dockerfile.template`, copy the host binary | A Dockerfile that compiles, then the same runtime layout |
| Where it is pushed | Dev registry `10.177.76.220:5000`, tag `dev-<ns>` | `ghcr.io/octopilot/<image>:<version>` on a `v*` tag |
| Who rolls the pods | Flux image automation in `profiles/dev` | A preprod, then prod, Flux profile that does not exist yet |
| What does not change | Helm chart, entrypoint, `/app/config` mount | Same |

Tilt never applies manifests. Flux already owns the dev cluster. The
release pipeline does not replace that. It publishes immutable images that
a GCP Flux profile can pin.

## Repos

**BRRTRouter** is the framework. Its Cargo workspace is library-first.
`skaffold.yaml` already describes three deliverables:

| Artifact | Function | Notes |
|---|---|---|
| `ghcr.io/microscaler/brrtrouter-petstore` | image | `examples/pet_store`, buildpack, `BP_RUST_PACKAGE=pet_store`, `BP_RUST_KEEP` for `doc`, `config`, `static_site` |
| `ghcr.io/microscaler/brrtrouter-lib` | lib | Verified rev. Downstream products pin it. `BP_LIB_PUBLISH=none` |
| `ghcr.io/microscaler/brrtrouter-tooling-lib` | lib | Python package. `pricewhisperer` is a shim over this |

The builder pin in that file is `rust-builder-ab65609` (rust 0.1.13). The
current pin is `rust-builder-d741287` (rust 0.1.15). Updating that pin is
part of bringing BRRTRouter onto the same pipeline, and it is not a
PriceWhisperer image.

**PriceWhisperer** is the product. `tooling/` is a thin CLI (`pricewhisperer`)
that delegates to `brrtrouter_tooling.workspace`. The microservice workspace
is `microservices/`.

Sibling checkouts live next to the product under
`~/Workspace/remote/microscaler/` (and, for BRRTRouter only, also under
`local/octopilot/`). They are how a developer compiles. They are not how a
release compiles. The ancestor file
`~/Workspace/remote/microscaler/.cargo/config.toml` is not in any repo. It
`[patch]`es git URLs back to those siblings. Cargo picks it up for every
workspace under that parent. Building with it active rewrites `Cargo.lock`
to path sources. The file itself says to move it aside before regenerating
a lockfile that will be committed.

## Runtime contract (do not change for the first release)

The chart `helm/pricewhisperer-microservice` sets no container `command`
and no `args`. The image entrypoint is the process. For a service that
entrypoint is the one in `docker/microservices/Dockerfile.template`:

```text
/app/<binary> --spec /app/doc/openapi.yaml --doc-dir /app/doc
              --static-dir /app/static_site --config /app/config/config.yaml
```

The chart mounts the Flux-rendered ConfigMap at `/app/config`. Relative
defaults in `main.rs` (`../gen/doc/openapi.yaml`, `./config/config.yaml`)
are resolved against `CARGO_MANIFEST_DIR`. They are how a developer runs
the binary from the crate directory. They are not what the cluster runs.
A buildpack image whose process is `bin/<name>` under `/workspace` would
ignore `/app/config` and would not see the mounted ConfigMap.

Workers are simpler: `docker/workers/Dockerfile` entrypoint is `/app/worker`.
Portals are nginx on port 80. `db-init` is the script in
`scripts/db-init-job.sh`. `pw-mock` is dev-only and is not a production
image.

## Deliverables Flux already expects

Dev profile `deployment-configuration/profiles/dev/pricewhisperer`. Image
names are what the HelmRelease sets. The registry prefix today is the dev
zot. The release name is the same image name on GHCR.

### Trader services (Tilt builds these)

Host musl build, then the rendered template. Each has `impl/Cargo.toml`.

| Image | Package | Binary the template copies |
|---|---|---|
| `pricewhisperer-alerts` | `pricewhisperer_alerts` | `alerts` after `copy-binary` |
| `pricewhisperer-areas` | `pricewhisperer_areas` | `areas` |
| `pricewhisperer-backtests` | `pricewhisperer_backtests` | `backtests` |
| `pricewhisperer-billing` | `pricewhisperer_billing` | `billing` |
| `pricewhisperer-brokerage` | `pricewhisperer_brokerage` | `brokerage` |
| `pricewhisperer-calendar` | `pricewhisperer_calendar` | `calendar` |
| `pricewhisperer-feedback` | `pricewhisperer_feedback` | `feedback` |
| `pricewhisperer-market` | `pricewhisperer_market` | `market` |
| `pricewhisperer-news` | `pricewhisperer_news` | `news` |
| `pricewhisperer-orders` | `pricewhisperer_orders` | `orders` |
| `pricewhisperer-portfolio` | `pricewhisperer_portfolio` | `portfolio` |
| `pricewhisperer-strategies` | `pricewhisperer_strategies` | `strategies` |

`identity` is spec-only. traderBFF serves it. No image.
`auth` is a spec. The BFF impl serves it. No image.
`disclosures` has an impl crate and no HelmRelease. Not in this release.

### BFFs (HelmRelease exists, Tilt service loop does not build them)

`traderBFF` and `platformBFF` are not keys in
`openapi/trader/bff-suite-config.yaml`, so `Tiltfile` never calls
`create_image` for them. The cluster still runs:

| Image | Package | Cargo bin |
|---|---|---|
| `pricewhisperer-trader-bff` | `pricewhisperer_traderBFF` | `traderBFF` |
| `pricewhisperer-platform-bff` | `pricewhisperer_platformBFF` | `platformBFF` |

The release has to build both. Fixing the Tilt loop is a separate, local
dev change and is not required to publish.

### Workers (Tilt, manual)

One Dockerfile, `ARG BINARY`. Host `cargo build --release` of the ingestor
crate, then copy into `build_artifacts/<arch>/`.

| Image | Package |
|---|---|
| `pricewhisperer-benzinga-news-ingestor` | `benzinga_news_ingestor` |
| `pricewhisperer-benzinga-analyst-ingestor` | `benzinga_analyst_ingestor` |
| `pricewhisperer-benzinga-ticker-ingestor` | `benzinga_ticker_ingestor` |
| `pricewhisperer-massive-ticker-ingestor` | `massive_ticker_ingestor` |
| `pricewhisperer-social-ingestor` | `social_worker` |
| `pricewhisperer-simulator-ingestor` | `simulator_worker` |

`core/workers/market-calendar.yaml` has no image field. Not a deliverable
until a Deployment names one.

### Edge and bootstrap

| Image | Tilt | Release Dockerfile that already exists |
|---|---|---|
| `pricewhisperer-website` | Host `yarn build`, `docker/portal/Dockerfile.tilt` | `docker/website/Dockerfile` (multi-stage) |
| `pricewhisperer-trader` | Host `npm` build, same tilt Dockerfile, `ARG PORTAL` | `docker/portal/Dockerfile` (multi-stage, `ARG PORTAL=trader`) |
| `pricewhisperer-platform` | Same, `ARG PORTAL=platform` | Same file, `ARG PORTAL=platform` |
| `pricewhisperer-db-init` | `docker/jobs/Dockerfile` | Same file. `COPY scripts/db-init-job.sh`. Context is the repo root |
| `pricewhisperer-pw-mock` | Host binary, fixed tag `:dev`, no image policy | None. Dev only. Do not publish for prod |
| `pricewhisperer` fte | Vite only, not in Flux | `docker/fte/Dockerfile` exists. Out of this release |

Third-party images (redis, `gnzsnz/ib-gateway`) are not ours.

### Not container images

| Thing | Where it goes |
|---|---|
| `brrtrouter` crate and macros | Git rev consumed by PriceWhisperer. BRRTRouter `-lib` artifact. Not an image |
| `brrtrouter-tooling` / `pricewhisperer` CLI | Developer and Tilt venv. Not an image |
| `pricewhisperer_migrator` | `cargo run` from Tilt (`pricewhisperer-migrate`). Schema files land in git. The cluster job is `db-init`, not this binary |
| Helm chart `helm/pricewhisperer-microservice` | Flux reads it from the GitRepository today. OCI chart publish can wait |
| Unit tests | Already `test-rust.yml` on the self-hosted runner. The pipeline Test job should take that over once the workspace builds in CI |

## Why the pipeline cannot build this set today

`detect-contexts` marks an artifact `docker` only when the context
directory contains a file named `Dockerfile`. It then always passes that
filename. Skaffold `docker.dockerfile` is ignored.

`op` docker build does not pass Skaffold `buildArgs`. One Dockerfile cannot
vary `PORTAL` or the crate name.

The service and worker files that Tilt uses are templates. They `COPY` a
binary that is not in git. A CI `docker build` of those files fails.

The Rust buildpack would also build the crate, and `BP_RUST_KEEP` can keep
`gen/doc` and `static_site`. That is the right shape for `pet_store`, whose
process is the CNB launcher and whose paths hang off `CARGO_MANIFEST_DIR`.
It is the wrong shape for PriceWhisperer until the chart stops mounting
`/app/config` and the entrypoint stops being the four-flag command. That
chart change is not part of the first release.

## What we add

### Pipeline (octopilot-pipeline-tools and octopilot/actions)

Small, and required before any PriceWhisperer `skaffold.yaml` works.

1. If an artifact has `docker.dockerfile`, it is a docker build. The path
   is relative to `context`. Absence of a file named `Dockerfile` must not
   flip the artifact to the buildpack.
2. Pass `docker.buildArgs` through to `docker build --build-arg`.
3. Context may be the repo root while the Dockerfile lives under `docker/`.
   `db-init` and the portals need that. Their `COPY` paths are from the
   repo root.

No new meta-build function. Services, workers, portals, and `db-init` are
docker artifacts. BRRTRouter's pet store and `-lib` artifacts stay
buildpack artifacts.

`actions_ref` is declared on the reusable workflow and never read. Every
composite step is `@main`. Pinning a caller to `pipeline.yml@v1.0.0` does
not pin detect, lint, test, or the wave workflow. Wiring `actions_ref`
into those `uses` lines is release hygiene. Do it in the same pipeline
change so a product can pin one ref. GitHub rejects `inputs` in a reusable
workflow `uses` ref (the wave workflow hit this). Action `uses` refs need
a form that actually evaluates, or the composites are pinned to the same
tag by a release step that rewrites them. Decide that in the implementation
review, not by guessing here.

### PriceWhisperer Dockerfiles

Keep every Tilt file. Add release files beside them.

| File | Role |
|---|---|
| `docker/microservices/Dockerfile.template` | Unchanged. Tilt renders it and copies the host binary |
| `docker/microservices/Dockerfile` | New. Multi-stage. `ARG PACKAGE` and `ARG BINARY`. Builder stage runs `cargo build --release -p ${PACKAGE} --bin ${BINARY}` in `microservices/` against the git revs in `Cargo.toml`. No sibling checkout and no path rewrite. Final stage: Alpine, `ca-certificates`, `libgcc`, the same `/app` layout and the same four-flag `ENTRYPOINT` as the template. `COPY` `gen/doc`, `gen/static_site` from the context. Do not `COPY` `impl/config` into the final image as the source of truth. The chart mounts `/app/config` |
| `docker/workers/Dockerfile` | Unchanged for Tilt (copies `build_artifacts/`) |
| `docker/workers/Dockerfile.release` | New. Multi-stage. `ARG PACKAGE`. Final stage entrypoint `/app/worker` |
| `docker/portal/Dockerfile` | Already the CI build for trader and platform. Needs `--build-arg PORTAL=` |
| `docker/website/Dockerfile` | Already the CI build. No build-arg |
| `docker/jobs/Dockerfile` | Already complete |
| `docker/portal/Dockerfile.tilt`, `docker/pw-mock/Dockerfile.tilt` | Unchanged. Not release inputs |
| `docker/fte/Dockerfile` | Leave until Flux has an fte Deployment |

One parameterized service Dockerfile, not fourteen generated
`Dockerfile.trader_*` files. `pricewhisperer docker generate-dockerfile`
can stay for the old multi-arch script. The release does not call it.

The service builder stage is large (the workspace and a Rust toolchain).
That is the cost of not shipping a host binary. Cache the cargo registry
the way the pipeline already caches pack builds, or accept a cold build
for the first cut. Do not share one image across services. Each
HelmRelease names its own image.

Jemalloc: the tooling README says CI opts in for amd64 and arm64 and skips
it on armv7 because of a musl link error. The release Dockerfiles should
match that. Target platforms for GCP are `linux/amd64` first.
`linux/arm64` is the second platform once amd64 publishes. armv7 is out.

### skaffold.yaml

New, repo root of PriceWhisperer. This is the list detect-contexts reads.
Illustrative, not the file to commit yet:

```yaml
apiVersion: skaffold/v4beta11
kind: Config
metadata:
  name: pricewhisperer
build:
  artifacts:
    - image: ghcr.io/octopilot/pricewhisperer-orders
      context: .
      docker:
        dockerfile: docker/microservices/Dockerfile
        buildArgs:
          PACKAGE: pricewhisperer_orders
          BINARY: orders
          SUITE: trader
          MODULE: orders
    - image: ghcr.io/octopilot/pricewhisperer-trader
      context: .
      docker:
        dockerfile: docker/portal/Dockerfile
        buildArgs:
          PORTAL: trader
    - image: ghcr.io/octopilot/pricewhisperer-website
      context: .
      docker:
        dockerfile: docker/website/Dockerfile
    - image: ghcr.io/octopilot/pricewhisperer-db-init
      context: .
      docker:
        dockerfile: docker/jobs/Dockerfile
    - image: ghcr.io/octopilot/pricewhisperer-benzinga-news-ingestor
      context: .
      docker:
        dockerfile: docker/workers/Dockerfile.release
        buildArgs:
          PACKAGE: benzinga_news_ingestor
```

Repeat the service block for the twelve services and both BFFs. Repeat the
worker block for the six ingestors. Image names match the HelmRelease
`image.name` values so the promoted GHCR repository is the name Flux
already uses, with a different registry.

No `-lib` artifact in the first cut. The product ships images. The
framework's lib deliverable stays in the BRRTRouter repo.

No chart artifact. Flux reads the chart from git.

### Workflow

`.github/workflows/release-images.yml` (name flexible):

```yaml
jobs:
  build:
    permissions:
      contents: read
      packages: write
      id-token: write
    uses: octopilot/actions/.github/workflows/pipeline.yml@v1.0.0
    with:
      integration: false
      op_version: v1.2.0
      runner: ubuntu-24.04
```

`integration: false` until there is a `k8s/env/ci` overlay. Do not stand
the product up in Kind inside this pipeline. Dev already has a cluster.

Push to `main` builds and pushes ephemeral ttl.sh images (the pipeline's
integration artifacts) so a change proves the Dockerfiles. A `v*` tag is
what promotes those digests to `ghcr.io/octopilot/<image>:<version>`. That
promotion already exists in the reusable workflow. Do not add a second
publish script.

The existing `test-rust.yml` stays until the pipeline Test job runs the
same `cargo test --lib --workspace` from `microservices/` and the
span-discipline check. Then delete the duplicate. `deploy-website.yml`
publishes GitHub Pages, not the cluster image. Leave it.

Runner size: this workspace is large. If `ubuntu-24.04` runs out of disk,
pass the self-hosted labels PriceWhisperer already uses
(`["self-hosted", "linux", "x64", "microscaler"]`). That is an input, not
a fork of the workflow.

### Dependency closure

Committed `Cargo.toml` is the release truth. Sibling checkouts are the
`PW_LOCAL_DEPS=1` override, not the compiled dependency lines.

| Crate | In `microservices/Cargo.toml` today | Sibling | Release rule |
|---|---|---|---|
| `brrtrouter`, `brrtrouter_macros` | git `rev` `fb070dc` | `BRRTRouter/` via `[workspace.metadata.local-deps]` | CI default. Tilt opts into the path |
| `lifeguard`, `lifeguard-derive`, `lifeguard-migrate` | git `rev` `b9cdb48` | `lifeguard/` via the same metadata table | Already a remote pin |
| `sesame-idam-client` | git `rev` `fb0364b` | `sesame-idam-client/` via the same metadata table | Already a remote pin. Consumed by traderBFF and platformBFF |
| `sesame-idam` | not a Cargo dependency | `sesame-idam/` | The service runs in cluster. PriceWhisperer vendors the client spec at `openapi/sesame-idam/`, marked `VENDORED_FROM` `aab6a18`. Re-vendor from that repo when the contract changes. Do not clone it into the image build |
| `may_postgres` | git `rev` `2ac8f6a`, one workspace dependency | `may_postgres/` via the same metadata table | Was `branch = "master"` on each impl crate |

`brrtrouter ci patch-brrtrouter` rewrites path deps to `branch = "main"`.
Do not run it in the image build. The workspace file is already on revs.

The flag that keeps a dev path out of CI:

- The compiled lines are git `rev` (or, for `may_tracing`, the tag `v0.1.1`).
- `[workspace.metadata.local-deps]` lists the sibling path for each of those
  git URLs. Cargo does not resolve metadata.
- `PW_LOCAL_DEPS=1` (Tilt sets this) writes
  `microservices/.cargo/local-deps.config`, which is gitignored. The build
  tool passes each line to `cargo --config`. A path is emitted only when
  that sibling checkout is on disk.
- CI does not set the flag. `cargo build` with the flag unset uses the revs.
- `local_deps.py --check` fails if a path that leaves the workspace appears
  in `[workspace.dependencies]` or `[patch]`. `test-rust.yml` runs that check.
  Putting `path = "../../BRRTRouter"` back on the compiled dependency is the
  breaking commit this catches.

`[patch]` in an ancestor `.cargo/config.toml` is not applied by current
Cargo. The metadata table plus `PW_LOCAL_DEPS` is the switch that is.

`Cargo.lock` stays gitignored for now. The rev pins freeze these crates.
Transitive crates of those repos still float until a lockfile is committed,
and that lockfile has to be generated with `PW_LOCAL_DEPS` unset so it does
not record path sources.

`vendor/ibapi` stays a path inside the repo (`vendor/ibapi`). It is not a
sibling checkout. The brokerage image still needs it.

`openapi/sesame-idam/*.yaml` stays in this repo. It is the contract the BFF
was written against, not a build input the Dockerfile clones. When
`sesame-idam` cuts a release that changes login or session, re-vendor those
files and bump `sesame-idam-client` together. The GCP deployment of
sesame-idam itself is that repo's release, not this pipeline.

## GCP

`infrastructure/` already describes GKE, Flux, Workload Identity, and
External Secrets. None of that is an image build. The gap is the registry
the cluster pulls.

| Environment | Registry | Tag | Who writes the tag |
|---|---|---|---|
| Dev (today) | `10.177.76.220:5000` | `dev-<ns>` | Tilt |
| Release (this plan) | `ghcr.io/octopilot` | `v*` | Pipeline promote step |
| Preprod, then prod | Same GHCR repos, or a copy into Artifact Registry if GKE should not pull GHCR | The release tag, then a promotion | Flux image policy, new profile under `deployment-configuration/profiles/` |

Do not point preprod at the dev zot. Do not retag `:latest` and let prod
float. The first GCP cut is: a profile whose `image.repository` is
`ghcr.io/octopilot` and whose tag is a release that this pipeline built.
Pull credentials for that registry are a cluster secret, not a change to
the Dockerfiles.

## Roadmap

1. **Pipeline docker contract.** detect-contexts and `op` honor
   `docker.dockerfile` and `buildArgs`. Tests cover a context of `.` with
   a Dockerfile under `docker/` and a build-arg. Pin `actions_ref` for real
   in the same change. No PriceWhisperer files yet.
2. **Sibling pins and the local-deps flag.** Done in PriceWhisperer
   `microservices/Cargo.toml`: git revs for BRRTRouter, lifeguard,
   sesame-idam-client, may_postgres, and the other floating git deps.
   Sibling paths are `[workspace.metadata.local-deps]`, applied only when
   `PW_LOCAL_DEPS=1`. Commit a `Cargo.lock` generated with that flag unset
   before the first image build, so transitive crates stop floating.
3. **One service proves the contract.** `docker/microservices/Dockerfile`
   for `orders` only, one skaffold artifact, the workflow caller. The build
   fetches the git revs. It does not mount sibling checkouts. Image starts
   with the four-flag entrypoint and reads config from `/app/config`. Tilt
   for orders is unchanged.
4. **Portals and db-init.** website, trader, platform, db-init artifacts.
   These Dockerfiles already exist. They only need the pipeline to find
   them and pass `PORTAL`.
5. **Remaining services and both BFFs.** Same Dockerfile, more artifacts.
   Add trader-bff and platform-bff to the Tilt loop only if local dev is
   still missing those images. That is independent of the release.
6. **Workers.** `Dockerfile.release`, six artifacts.
7. **Tag promotion.** Cut `v0.1.0` (or whatever the first product tag is).
   Confirm GHCR holds every image at that tag, digest-pinned from the
   ttl.sh build. No `:latest`.
8. **Preprod profile.** New Flux profile, GHCR pull, image policies on the
   release tags. Prod is the same profile shape after preprod has run.
   sesame-idam in that cluster is deployed from the sesame-idam repo, at the
   commit the vendored spec names.
9. **BRRTRouter pipeline.** Move its skaffold builder to
   `rust-builder-d741287`, keep pet_store and the two `-lib` artifacts, call
   `pipeline.yml@v1.0.0`. The PriceWhisperer `rev` for `brrtrouter` should be
   a commit that pipeline has already built.

## Out of the first release

- Replacing Tilt, or making Tilt call the release Dockerfiles.
- Building PriceWhisperer services with `octopilot/rust`.
- Publishing `pw-mock`, fte, disclosures, or the Helm chart as OCI.
- armv7.
- A Kind integration deploy of the whole product.
- Pointing the dev cluster at GHCR.

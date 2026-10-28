# Changelog

## Unreleased

### Fixed: upload accounting on Artifact Registry

op counted blob bytes only on `/v2/<repo>/blobs/uploads/` URLs. Artifact Registry continues uploads at `/artifacts-uploads/...`, so pushes there reported 0.0 MiB. Bytes are now counted for every PATCH, and for every PUT or POST that is not a manifest or a mount.

## v1.2.5

### Fixed: multi-platform images are what their index says they are

v1.2.4's image index was mixed. Its first entry came from a main-branch build that raced the release build, and every op index so far listed two amd64 images with no platform field.
- **Per-run staging tags:** each run stages its per-platform images under its own tags (`<tag>-<os>-<arch>-<run id>-<attempt>`). Two runs building the same tag at once (a main push and a release tag) no longer read each other's images.
- **Platform fields:** every index entry carries the platform its image's config states.
- **Wrong-platform guard:** an entry whose image is not the platform it was built as stops the push. Before, a builder without arm64 built amd64 and published it as arm64.
- **Aliases by digest:** version and branch aliases (`v1.2.5`, `main`) point at the exact manifest this run pushed, not at whatever the tag resolves to by then.
- **op's own image** is now built with the multi-arch `builder-jammy-base` (linux/amd64 and linux/arm64), so its arm64 image is arm64.

## v1.2.4

### Changed: the Rust layer cache is off by default when no cache image is configured

When `OP_CACHE_IMAGE` is unset or empty, op now passes `BP_RUST_CACHE=false` to the build. The octopilot/rust buildpack (from the release that includes octopilot/rust#7) then writes its `rust-toolchain` and `rust-target` layers with `cache = false`, so the lifecycle no longer exports them to the build cache.

**Why:** without a cache image, pack keeps the build cache in a local Docker volume. On ephemeral CI runners (ARC with dind) that volume is discarded with the runner. Exporting the multi-GB target dir into it took minutes per build with no output, and looked like a hang.

**Who is affected:** anyone running `op build` on a persistent machine without `OP_CACHE_IMAGE`, for example a laptop. Those builds no longer reuse the toolchain and incremental `target/` from the previous build. CI with a cache image is unchanged. CI without one, which is Octopilot's default (`build_cache: false`), simply stops writing a cache it never read back.

**Override:** set `BP_RUST_CACHE=true` to keep the local-volume cache. Either set it in op's environment (`BP_RUST_CACHE=true op build ...`), or put it in the project's buildpack env (skaffold `buildpacks.env`). An explicit value always wins over op's default. With a cache image configured, op leaves `BP_RUST_CACHE` alone.

An older rust buildpack ignores the variable, so the default only changes once the builder includes the new buildpack.

## v1.2.3

- **BRRTRouter suite pushes are split per binary:**
  - The suite is built into a registry inside op and pushed as one layer per binary.
  - Only layers the destination registry does not already have are uploaded.
  - Fan-out images mount the suite's layers instead of uploading them again.
- **Upload accounting:** every push reports the bytes it uploaded, plus blobs mounted and blobs already present.
- **Quiet-step heartbeat:** while the lifecycle prints nothing (a cache write, a registry push, a long compile), op prints a heartbeat. With a local publish, op stops the build after `OP_PACK_IDLE_TIMEOUT` (default 30m) of silence.
- **Opt-out:** set `OP_SUITE_SPLIT=false` to go back to the single-image suite push.

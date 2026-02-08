"""CLI for Skaffold/Buildpacks pipelines: build, push, watch-deployment, promote-image."""

from __future__ import annotations

import subprocess
import sys
import time
from pathlib import Path

import click

from .build_result import (
    BUILD_RESULT_FILENAME,
    get_first_tag,
    read_build_result,
    run_skaffold_build_push,
)
from .config import (
    get_config,
    get_default_repo,
    get_promote_repositories,
    get_watch_destination_repository,
)
from .pack_build import (
    parse_skaffold_artifacts,
)
from .pack_build import (
    run_pack_build_push as run_pack_build_push_impl,
)
from .registry import (
    REGISTRY_FILENAME,
    get_default_repo_from_registry,
    get_push_registries,
)
from .run_config import get_run_options_for_context, load_run_config
from .start_registry import install_cert_trust, start_registry


def _config_callback(ctx: click.Context, _param: click.Parameter, value: str | None) -> str | None:
    if value:
        path = Path(value)
        if not path.exists():
            raise click.BadParameter(f"Properties file not found: {path}")
        ctx.ensure_object(dict)
        ctx.obj["properties_path"] = path
    return value


@click.group()
@click.option(
    "--config",
    "config_path",
    type=click.Path(path_type=Path),
    default=None,
    envvar="OCTOPILOT_PIPELINE_PROPERTIES",
    help="Path to pipeline.properties (env-style key=value). Env vars override.",
    callback=_config_callback,
)
@click.pass_context
def main(ctx: click.Context, config_path: Path | None) -> None:
    """OctoPilot pipeline tools: Skaffold build, push, build_result.json, watch-deployment, promote-image."""
    ctx.ensure_object(dict)
    ctx.obj["config"] = get_config(ctx.obj.get("properties_path"))


@main.command(short_help="Run skaffold build (no push).")
@click.pass_context
def build(ctx: click.Context) -> None:
    """Run skaffold build (no push).

    Uses SKAFFOLD_DEFAULT_REPO or registry from config if set.
    """
    config = ctx.obj["config"]
    cwd = Path.cwd()
    cmd = ["skaffold", "build"]
    default_repo = get_default_repo(config)
    if default_repo:
        cmd.extend(["--default-repo", default_repo])
    for key in ("SKAFFOLD_PROFILE", "SKAFFOLD_LABEL", "SKAFFOLD_NAMESPACE"):
        if config.get(key):
            opt = key.replace("SKAFFOLD_", "").lower().replace("_", "-")
            cmd.extend([f"--{opt}", config[key]])
    proc = subprocess.run(cmd, cwd=cwd)
    if proc.returncode != 0:
        sys.exit(proc.returncode)


@main.command(
    "build-push",
    short_help="Build and push with pack --publish (use when Skaffold buildpacks fail).",
)
@click.option(
    "--repo",
    "repo",
    default="localhost:5001",
    envvar="SKAFFOLD_DEFAULT_REPO",
    help="Registry to push to (default: localhost:5001). E.g. ghcr.io/owner/repo for GitHub Container Registry.",
)
@click.option(
    "--tag",
    default="latest",
    help="Image tag for all artifacts.",
)
@click.option(
    "--skaffold-file",
    type=click.Path(path_type=Path),
    default=Path("skaffold.yaml"),
    help="Path to skaffold.yaml (default: skaffold.yaml in cwd).",
)
@click.option(
    "--output",
    type=click.Path(path_type=Path),
    default=None,
    help=f"Directory to write {BUILD_RESULT_FILENAME} (default: cwd).",
)
@click.option(
    "--pack",
    "pack_cmd",
    default="pack",
    envvar="PACK_CMD",
    help="Path to pack CLI (default: 'pack' from PATH). Use local build e.g. ../pack/out/pack or set PACK_CMD.",
)
@click.pass_context
def build_push(
    ctx: click.Context,
    repo: str,
    tag: str,
    skaffold_file: Path,
    output: Path | None,
    pack_cmd: str,
) -> None:
    """Build and push with pack --publish (use when Skaffold buildpacks fail on Mac or Linux).

    Reads buildpacks artifacts from skaffold.yaml and runs pack build for each with
    --publish, then writes build_result.json. Works around Mac containerd digest
    error and Linux /layers permission denied. Default registry is localhost:5001
    (override with --repo or SKAFFOLD_DEFAULT_REPO).
    """
    cwd = Path.cwd()
    skaffold_path = cwd / skaffold_file if not skaffold_file.is_absolute() else skaffold_file
    try:
        path = run_pack_build_push_impl(
            default_repo=repo,
            cwd=cwd,
            tag=tag,
            skaffold_path=skaffold_path,
            output_dir=output,
            pack_cmd=pack_cmd,
        )
        click.echo(f"Wrote {path}")
    except SystemExit as e:
        sys.exit(e.code)


def _run_resolve_default_repo(cwd: Path, config: dict, run_config: dict) -> str:
    """Default repo for op run: .run.yaml > SKAFFOLD_DEFAULT_REPO > .registry local > localhost:5001."""
    repo = run_config.get("default_repo") if isinstance(run_config.get("default_repo"), str) else None
    if repo:
        return repo.strip().rstrip("/")
    repo = get_default_repo(config)
    if repo:
        return repo.strip().rstrip("/")
    repo = get_default_repo_from_registry(repo_root=cwd, destination="local")
    if repo:
        return repo.strip().rstrip("/")
    return "localhost:5001"


def _run_docker_run(
    image: str,
    ports: list[str],
    env: dict[str, str],
    volumes: list[str],
) -> None:
    """Run docker run with the given image, ports, env, and volumes."""
    cmd = ["docker", "run", "--rm", "-it"]
    for p in ports:
        cmd.extend(["-p", p])
    for k, v in env.items():
        cmd.extend(["-e", f"{k}={v}"])
    for v in volumes:
        cmd.extend(["-v", v])
    cmd.append(image)
    proc = subprocess.run(cmd)
    if proc.returncode != 0:
        sys.exit(proc.returncode)


@main.command(
    "run",
    short_help="Run a built image for a Skaffold context (ports/env/volumes from .run.yaml).",
)
@click.option(
    "--skaffold-file",
    type=click.Path(path_type=Path),
    default=Path("skaffold.yaml"),
    help="Path to skaffold.yaml (default: skaffold.yaml in cwd).",
)
@click.argument("args", nargs=-1)
@click.pass_context
def run(
    ctx: click.Context,
    skaffold_file: Path,
    args: tuple[str, ...],
) -> None:
    """Run a built image for a Skaffold context (local dev only).

    For local development only: runs a single container with \"docker run\" using
    preconfigured ports/env/volumes from .run.yaml. Not for production. Does not
    replace docker-compose, Kubernetes (e.g. kind), or full deploy workflows.

    Use \"op run context list\" to list runnable contexts from skaffold.yaml.
    Use \"op run <context>\" to run that context (e.g. op run api, op run frontend).
    Ports, env vars, and volumes come from .run.yaml in the repo root; if missing,
    defaults are applied (e.g. -p 8080:8080 -e PORT=8080). Build images first with
    \"op build-push\" or \"skaffold build\".
    """
    if not args:
        click.echo(ctx.get_help())
        sys.exit(0)
    cwd = Path.cwd()
    skaffold_path = cwd / skaffold_file if not skaffold_file.is_absolute() else skaffold_file
    if not skaffold_path.exists():
        click.echo(f"Skaffold file not found: {skaffold_path}", err=True)
        sys.exit(1)
    try:
        artifacts = parse_skaffold_artifacts(skaffold_path)
    except Exception as e:
        click.echo(f"Failed to read skaffold.yaml: {e}", err=True)
        sys.exit(1)
    if not artifacts:
        click.echo("No artifacts in skaffold.yaml.", err=True)
        sys.exit(1)
    run_cfg = load_run_config(cwd)
    config = ctx.obj["config"]
    default_repo = _run_resolve_default_repo(cwd, config, run_cfg)
    default_tag = run_cfg.get("tag") if isinstance(run_cfg.get("tag"), str) else "latest"

    if len(args) == 2 and args[0] == "context" and args[1] == "list":
        click.echo("Contexts (use: op run <context>):")
        for art in artifacts:
            click.echo(f"  {art['context']}")
        return

    if len(args) == 1:
        context_name = args[0]
        match = [a for a in artifacts if a["context"] == context_name]
        if not match:
            click.echo(
                f'Unknown context: {context_name}. Use "op run context list" to list contexts.',
                err=True,
            )
            sys.exit(1)
        art = match[0]
        image_name = art["image"]
        full_image = f"{default_repo}/{image_name}:{default_tag}"
        opts = get_run_options_for_context(context_name, cwd, config=run_cfg)
        _run_docker_run(
            full_image,
            ports=opts["ports"],
            env=opts["env"],
            volumes=opts["volumes"],
        )
        return

    click.echo("Usage: op run context list  |  op run <context>", err=True)
    sys.exit(1)


@main.command(
    short_help="Build with Skaffold, push to registry, write build_result.json.",
)
@click.option(
    "--default-repo",
    envvar="SKAFFOLD_DEFAULT_REPO",
    help="Registry/repo for push (overrides .registry and env).",
)
@click.option(
    "--destination",
    type=click.Choice(["local", "ci", "all", "auto"]),
    default="auto",
    help="Which registries from .registry: local, ci, all, or auto (ci in GITHUB_ACTIONS else local).",
)
@click.option(
    "--registry-file",
    type=click.Path(path_type=Path),
    default=None,
    help=f"Path to .registry file (default: {REGISTRY_FILENAME} in cwd).",
)
@click.option(
    "--push-all",
    is_flag=True,
    help="After push to first registry, crane copy to remaining CI destinations.",
)
@click.option("--profile", default="push", help="Skaffold profile (e.g. push).")
@click.option(
    "--output",
    type=click.Path(path_type=Path),
    default=None,
    help=f"Write {BUILD_RESULT_FILENAME} here (default: cwd).",
)
@click.option("--no-file-output", is_flag=True, help="Parse skaffold stdout instead of --file-output.")
@click.option("--image-pattern", default=None, help="Regex with 'image' and 'tag' groups to parse output.")
@click.pass_context
def push(
    ctx: click.Context,
    default_repo: str | None,
    destination: str,
    registry_file: Path | None,
    push_all: bool,
    profile: str | None,
    output: Path | None,
    no_file_output: bool,
    image_pattern: str | None,
) -> None:
    """Run skaffold build (with profile), push to registry, write build_result.json.

    Registry can come from --default-repo, SKAFFOLD_DEFAULT_REPO, or .registry file.
    Use --destination to choose local vs CI registries; --push-all to crane copy
    to remaining CI destinations after the first push.
    """
    config = ctx.obj["config"]
    cwd = Path.cwd()
    if output is not None:
        cwd = output
    repo_root = cwd if registry_file is None else registry_file.parent
    reg_path = (cwd / REGISTRY_FILENAME) if registry_file is None else registry_file
    if not default_repo:
        default_repo = get_default_repo(config)
    if not default_repo and reg_path.exists():
        default_repo = get_default_repo_from_registry(repo_root=repo_root, destination=destination)
    if not default_repo:
        click.echo(
            "::error ::No push registry. Set --default-repo, SKAFFOLD_DEFAULT_REPO "
            "(or equivalent env), or add a .registry file.",
            err=True,
        )
        sys.exit(1)
    try:
        path = run_skaffold_build_push(
            default_repo=default_repo,
            profile=profile,
            cwd=cwd,
            use_file_output=not no_file_output,
            image_pattern=image_pattern,
            push=True,
        )
        click.echo(f"Wrote {path}")
    except SystemExit as e:
        sys.exit(e.code)
    # Optional: push to remaining CI registries via crane copy
    if push_all and reg_path.exists():
        registries = get_push_registries(repo_root=repo_root, destination="ci")
        if len(registries) <= 1:
            return
        data = read_build_result(cwd=cwd)
        tag_str = get_first_tag(data)
        # Primary ref we just pushed: default_repo/image:tag
        if "/" not in tag_str or tag_str.startswith(default_repo):
            primary_ref = f"{default_repo}/{tag_str}" if "/" not in tag_str else tag_str
        else:
            primary_ref = f"{default_repo}/{tag_str}"
        image_tag_part = tag_str.split("/", 1)[-1]  # image:tag or path/image:tag
        for other in registries:
            if other.rstrip("/") == default_repo.rstrip("/"):
                continue
            dest_ref = f"{other}/{image_tag_part}"
            click.echo(f"Copying to {dest_ref} ...")
            proc = subprocess.run(["crane", "copy", primary_ref, dest_ref])
            if proc.returncode != 0:
                click.echo(f"::error ::crane copy to {other} failed", err=True)
                sys.exit(proc.returncode)


@main.command(
    "watch-deployment",
    short_help="Wait for Flux to update deployment, then kubectl rollout status.",
)
@click.option("--component", "component_name", required=True, help="Deployment/HelmRelease name.")
@click.option(
    "--environment",
    type=click.Choice(["dev", "pp", "prod"]),
    required=True,
    help="Environment (sets destination repo).",
)
@click.option("--timeout", default="30m", help="kubectl rollout status timeout (e.g. 15m).")
@click.option("--namespace", default="default", help="Kubernetes namespace.")
@click.option(
    "--build-result",
    type=click.Path(path_type=Path),
    default=Path(BUILD_RESULT_FILENAME),
    help="Path to build_result.json.",
)
@click.pass_context
def watch_deployment(
    ctx: click.Context,
    component_name: str,
    environment: str,
    timeout: str,
    namespace: str,
    build_result: Path,
) -> None:
    """Wait for Flux to update deployment to image from build_result.json, then rollout status.

    Reconciles the HelmRelease, polls until the deployment uses the image from
    build_result.json, then runs kubectl rollout status with the given timeout.
    Destination repository is resolved from config (e.g. GOOGLE_GKE_IMAGE_* or
    WATCH_DESTINATION_REPOSITORY by environment).
    """
    config = ctx.obj["config"]
    dest_repo = get_watch_destination_repository(config, environment)
    if not dest_repo:
        click.echo(
            "::error ::Could not resolve destination repository. Set env "
            "(e.g. GOOGLE_GKE_IMAGE_* or WATCH_DESTINATION_REPOSITORY).",
            err=True,
        )
        sys.exit(1)
    data = read_build_result(build_result.parent if build_result.is_file() else Path.cwd())
    tag = get_first_tag(data)
    # tag may be "image-name:sha-timestamp"; full image = dest_repo/tag
    full_image = f"{dest_repo}/{tag}" if "/" not in tag else tag
    repo_base = full_image.rsplit("/", 1)[0] if "/" in full_image else dest_repo
    image_tag_only = tag.split(":")[-1] if ":" in tag else tag
    click.echo(f"Waiting for deployment {component_name} to use image {repo_base}/{tag} ...")
    while True:
        subprocess.run(
            ["flux", "reconcile", "helmrelease", component_name, "-n", namespace],
            capture_output=True,
        )
        jsonpath = "{.spec.template.spec.containers[0].image}"
        proc = subprocess.run(
            ["kubectl", "-n", namespace, "get", "deployment", component_name, "-o", jsonpath],
            capture_output=True,
            text=True,
        )
        current = (proc.stdout or "").strip()
        if current and (image_tag_only in current or tag in current):
            break
        time.sleep(10)
    click.echo(f"Image matched. Waiting for rollout (timeout {timeout}) ...")
    proc = subprocess.run(
        [
            "kubectl",
            "-n",
            namespace,
            "rollout",
            "status",
            f"deployment/{component_name}",
            "--timeout",
            timeout,
        ],
    )
    if proc.returncode != 0:
        click.echo("::error ::Flux: deployment rollout failed.", err=True)
        sys.exit(proc.returncode)


@main.command(
    "start-registry",
    short_help="Start local registry with TLS; replace existing, copy certs, optionally trust.",
)
@click.option(
    "--image",
    default="ghcr.io/octopilot/registry-tls:latest",
    envvar="REGISTRY_TLS_IMAGE",
    help="Docker image for the registry (default: ghcr.io/octopilot/registry-tls:latest).",
)
@click.option(
    "--certs-dir",
    type=click.Path(path_type=Path),
    default=None,
    help="Directory to copy certs to (default: ~/.config/registry-tls/certs).",
)
@click.option(
    "--trust-cert",
    is_flag=True,
    default=False,
    help="Install the self-signed cert for system trust (may prompt for sudo/password).",
)
@click.option(
    "--user-keychain",
    is_flag=True,
    default=False,
    help="[macOS only] Use login keychain instead of System keychain (no sudo).",
)
@click.pass_context
def start_registry_cmd(
    ctx: click.Context,
    image: str,
    certs_dir: Path | None,
    trust_cert: bool,
    user_keychain: bool,
) -> None:
    """Start local registry with TLS on port 5001.

    Replaces any existing registry container, starts the image, copies certs out of the
    container, and optionally installs the cert so the system (and Docker) trusts HTTPS
    to localhost:5001. On macOS, trusting the cert may ask for your password (sudo to add
    to System keychain); use --user-keychain to avoid sudo. On Linux, trusting runs sudo
    to copy the cert into the system CA store. If you skip trust, add localhost:5001 to
    Docker's insecure-registries instead.
    """
    try:
        crt = start_registry(
            image=image,
            certs_out_dir=certs_dir,
            trust_cert=trust_cert,
            use_system_keychain_macos=not user_keychain,
        )
        click.echo(f"Certs copied to {crt.parent}")
        if (
            not trust_cert
            and sys.stdin.isatty()
            and click.confirm(
                "Install cert for system trust? This may ask for your password (sudo).",
                default=False,
            )
        ):
            install_cert_trust(crt, use_system_keychain_macos=not user_keychain)
            click.echo("Cert installed for system trust. You may need to restart Docker for it to take effect.")
        elif not trust_cert:
            click.echo(
                "To trust the cert later, run: op start-registry --trust-cert. "
                'Or add "insecure-registries": ["localhost:5001"] to Docker settings.',
            )
        else:
            click.echo("Cert installed for system trust. You may need to restart Docker for it to take effect.")
    except RuntimeError as e:
        click.echo(f"::error ::{e}", err=True)
        sys.exit(1)
    except FileNotFoundError as e:
        click.echo(f"::error ::{e}", err=True)
        sys.exit(1)


@main.command(
    "promote-image",
    short_help="Copy image from source to destination registry (crane copy).",
)
@click.option("--source", type=click.Choice(["dev", "pp", "prod"]), required=True)
@click.option("--destination", type=click.Choice(["pp", "prod"]), required=True)
@click.option(
    "--build-result",
    type=click.Path(path_type=Path),
    default=Path(BUILD_RESULT_FILENAME),
    help="Path to build_result.json.",
)
@click.pass_context
def promote_image(
    ctx: click.Context,
    source: str,
    destination: str,
    build_result: Path,
) -> None:
    """Copy image from source to destination registry (crane copy).

    Reads the image tag from build_result.json and uses crane copy to promote
    from the source environment registry (e.g. dev) to the destination (e.g. pp
    or prod). Repositories come from config (e.g. GOOGLE_GKE_IMAGE_* or
    PROMOTE_SOURCE/DESTINATION_REPOSITORY).
    """
    config = ctx.obj["config"]
    src_repo, dest_repo = get_promote_repositories(config, source, destination)
    if not src_repo or not dest_repo:
        click.echo(
            "::error ::Set env (e.g. GOOGLE_GKE_IMAGE_* or PROMOTE_SOURCE/DESTINATION_REPOSITORY).",
            err=True,
        )
        sys.exit(1)
    data = read_build_result(build_result.parent if build_result.is_file() else Path.cwd())
    tag = get_first_tag(data)
    src_ref = f"{src_repo}/{tag}"
    dest_ref = f"{dest_repo}/{tag}"
    click.echo(f"Promoting {src_ref} -> {dest_ref}")
    proc = subprocess.run(["crane", "copy", src_ref, dest_ref])
    if proc.returncode != 0:
        sys.exit(proc.returncode)


if __name__ == "__main__":
    main()

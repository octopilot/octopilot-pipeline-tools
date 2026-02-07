"""CLI: port of pipeline.sh for Skaffold/Buildpacks (build, push, watch-deployment, promote-image)."""

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
    write_build_result,
)
from .config import (
    get_config,
    get_default_repo,
    get_promote_repositories,
    get_watch_destination_repository,
)
from .registry import (
    get_default_repo_from_registry,
    get_push_registries,
    load_registry_file,
    REGISTRY_FILENAME,
)


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


@main.command()
@click.pass_context
def build(ctx: click.Context) -> None:
    """Run skaffold build (no push)."""
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


@main.command()
@click.option("--default-repo", envvar="SKAFFOLD_DEFAULT_REPO", help="Registry/repo for push (overrides .registry and env).")
@click.option(
    "--destination",
    type=click.Choice(["local", "ci", "all", "auto"]),
    default="auto",
    help="Which registries from .registry: local, ci, all, or auto (ci in GITHUB_ACTIONS else local).",
)
@click.option("--registry-file", type=click.Path(path_type=Path), default=None, help=f"Path to .registry file (default: {REGISTRY_FILENAME} in cwd).")
@click.option("--push-all", is_flag=True, help="After push to first registry, crane copy to remaining CI destinations.")
@click.option("--profile", default="push", help="Skaffold profile (e.g. push).")
@click.option("--output", type=click.Path(path_type=Path), default=None, help=f"Write {BUILD_RESULT_FILENAME} here (default: cwd).")
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
    Registry can come from --default-repo, env (SKAFFOLD_DEFAULT_REPO / GOOGLE_GKE_IMAGE_REPOSITORY), or .registry file.
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
            "::error ::No push registry. Set --default-repo, SKAFFOLD_DEFAULT_REPO, GOOGLE_GKE_IMAGE_REPOSITORY, or add a .registry file.",
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


@main.command("watch-deployment")
@click.option("--component", "component_name", required=True, help="Deployment/HelmRelease name.")
@click.option("--environment", type=click.Choice(["dev", "pp", "prod"]), required=True, help="Environment (sets destination repo).")
@click.option("--timeout", default="30m", help="kubectl rollout status timeout (e.g. 15m).")
@click.option("--namespace", default="sam", help="Kubernetes namespace.")
@click.option("--build-result", type=click.Path(path_type=Path), default=Path(BUILD_RESULT_FILENAME), help="Path to build_result.json.")
@click.pass_context
def watch_deployment(
    ctx: click.Context,
    component_name: str,
    environment: str,
    timeout: str,
    namespace: str,
    build_result: Path,
) -> None:
    """Wait for Flux to update deployment to image from build_result.json, then rollout status."""
    config = ctx.obj["config"]
    dest_repo = get_watch_destination_repository(config, environment)
    if not dest_repo:
        click.echo("::error ::Could not resolve destination repository. Set GOOGLE_GKE_IMAGE_* or WATCH_DESTINATION_REPOSITORY.", err=True)
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
        proc = subprocess.run(
            ["kubectl", "-n", namespace, "get", "deployment", component_name, "-o", "jsonpath={.spec.template.spec.containers[0].image}"],
            capture_output=True,
            text=True,
        )
        current = (proc.stdout or "").strip()
        if current and (image_tag_only in current or tag in current):
            break
        time.sleep(10)
    click.echo(f"Image matched. Waiting for rollout (timeout {timeout}) ...")
    proc = subprocess.run(
        ["kubectl", "-n", namespace, "rollout", "status", f"deployment/{component_name}", "--timeout", timeout],
    )
    if proc.returncode != 0:
        click.echo("::error ::Flux: deployment rollout failed.", err=True)
        sys.exit(proc.returncode)


@main.command("promote-image")
@click.option("--source", type=click.Choice(["dev", "pp", "prod"]), required=True)
@click.option("--destination", type=click.Choice(["pp", "prod"]), required=True)
@click.option("--build-result", type=click.Path(path_type=Path), default=Path(BUILD_RESULT_FILENAME), help="Path to build_result.json.")
@click.pass_context
def promote_image(
    ctx: click.Context,
    source: str,
    destination: str,
    build_result: Path,
) -> None:
    """Copy image from source to destination registry (crane copy)."""
    config = ctx.obj["config"]
    src_repo, dest_repo = get_promote_repositories(config, source, destination)
    if not src_repo or not dest_repo:
        click.echo("::error ::Set GOOGLE_GKE_IMAGE_* or PROMOTE_SOURCE/DESTINATION_REPOSITORY.", err=True)
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

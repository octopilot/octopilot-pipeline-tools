"""Build and push using pack CLI with --publish (workaround when Skaffold buildpacks fail)."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path  # noqa: TC003

import yaml

from .build_result import write_build_result


def parse_skaffold_artifacts(skaffold_path: Path) -> list[dict]:
    """
    Read skaffold.yaml and return all artifacts: list of {image, context}.
    context defaults to "." if not set.
    """
    if not skaffold_path.exists():
        raise FileNotFoundError(f"Skaffold file not found: {skaffold_path}")
    data = yaml.safe_load(skaffold_path.read_text()) or {}
    build = data.get("build") or {}
    artifacts = build.get("artifacts") or []
    result: list[dict] = []
    for art in artifacts:
        if not isinstance(art, dict):
            continue
        image = art.get("image")
        context = art.get("context") or "."
        if image:
            result.append({"image": image, "context": context})
    return result


def parse_skaffold_buildpacks_artifacts(skaffold_path: Path) -> list[dict]:
    """
    Read skaffold.yaml and return buildpacks artifacts: list of {image, context, builder}.
    Skips artifacts that do not have buildpacks.builder.
    """
    if not skaffold_path.exists():
        raise FileNotFoundError(f"Skaffold file not found: {skaffold_path}")
    data = yaml.safe_load(skaffold_path.read_text()) or {}
    build = data.get("build") or {}
    artifacts = build.get("artifacts") or []
    result: list[dict] = []
    for art in artifacts:
        if not isinstance(art, dict):
            continue
        buildpacks = art.get("buildpacks") or {}
        builder = buildpacks.get("builder") if isinstance(buildpacks, dict) else None
        if not builder:
            continue
        image = art.get("image")
        context = art.get("context") or "."
        if image:
            result.append({"image": image, "context": context, "builder": builder})
    return result


def run_pack_build_push(
    *,
    default_repo: str,
    cwd: Path,
    tag: str = "latest",
    skaffold_path: Path | None = None,
    pack_cmd: str = "pack",
    output_dir: Path | None = None,
) -> Path:
    """
    For each buildpacks artifact in skaffold.yaml, run pack build with --publish,
    then write build_result.json. Use when Skaffold fails (Mac containerd digest,
    Linux /layers permission denied).
    """
    cwd = cwd.resolve()
    skaffold_path = (skaffold_path or cwd / "skaffold.yaml").resolve()
    artifacts = parse_skaffold_buildpacks_artifacts(skaffold_path)
    if not artifacts:
        sys.stderr.write("No buildpacks artifacts in skaffold.yaml. Each artifact must have buildpacks.builder.\n")
        raise SystemExit(1)
    default_repo = default_repo.rstrip("/")
    builds: list[dict] = []
    for art in artifacts:
        image_name = art["image"]
        context = art["context"]
        builder = art["builder"]
        full_image = f"{default_repo}/{image_name}:{tag}"
        cmd = [
            pack_cmd,
            "build",
            full_image,
            "--path",
            str(cwd / context),
            "--builder",
            builder,
            "--publish",
        ]
        proc = subprocess.run(cmd, cwd=cwd)
        if proc.returncode != 0:
            raise SystemExit(proc.returncode)
        builds.append({"tag": full_image})
    write_cwd = (output_dir or cwd).resolve()
    out_path = write_build_result(builds, cwd=write_cwd)
    return out_path

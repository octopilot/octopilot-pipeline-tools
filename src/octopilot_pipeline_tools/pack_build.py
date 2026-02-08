"""Build and push using pack CLI with --publish (workaround when Skaffold buildpacks fail)."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path  # noqa: TC003
from threading import Thread

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


def parse_skaffold_docker_artifacts(skaffold_path: Path) -> list[dict]:
    """
    Read skaffold.yaml and return docker artifacts: list of {image, context, dockerfile}.
    Skips artifacts that do not have docker (e.g. buildpacks-only).
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
        docker_cfg = art.get("docker")
        if not isinstance(docker_cfg, dict):
            continue
        image = art.get("image")
        context = art.get("context") or "."
        dockerfile = (docker_cfg.get("dockerfile") or "Dockerfile") if docker_cfg else "Dockerfile"
        if image:
            result.append({"image": image, "context": context, "dockerfile": dockerfile})
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
    For each buildpacks artifact run pack build --publish; for each docker artifact
    run docker build and docker push. Writes build_result.json with all built images.
    Use when Skaffold fails (Mac containerd digest, Linux /layers permission denied).
    """
    cwd = cwd.resolve()
    skaffold_path = (skaffold_path or cwd / "skaffold.yaml").resolve()
    bp_artifacts = parse_skaffold_buildpacks_artifacts(skaffold_path)
    docker_artifacts = parse_skaffold_docker_artifacts(skaffold_path)
    if not bp_artifacts and not docker_artifacts:
        msg = (
            "No buildpacks or docker artifacts in skaffold.yaml. "
            "Add artifacts with buildpacks.builder and/or docker.dockerfile.\n"
        )
        sys.stderr.write(msg)
        sys.stderr.flush()
        raise SystemExit(1)
    default_repo = default_repo.rstrip("/")
    # Lifecycle runs in a container: from there "localhost" is the container, not the host.
    # Use host.docker.internal so the lifecycle can reach the registry (Mac/Windows Docker).
    effective_repo = default_repo
    insecure_registries: list[str] = []
    if default_repo in ("localhost:5001", "127.0.0.1:5001"):
        effective_repo = "host.docker.internal:5001"
        # Lifecycle container doesn't have the host's CA store; skip TLS verify for this registry.
        insecure_registries.append("host.docker.internal:5001")
    full_repo = effective_repo.rstrip("/")
    # Result file and downstream tools use the user-facing ref (localhost:5001), not effective_repo.
    display_repo = default_repo.rstrip("/")
    builds: list[dict] = []
    for art in bp_artifacts:
        image_name = art["image"]
        context = art["context"]
        builder = art["builder"]
        full_image = f"{full_repo}/{image_name}:{tag}"
        cmd = [
            pack_cmd,
            "build",
            full_image,
            "--path",
            str(cwd / context),
            "--builder",
            builder,
            "--publish",
            "--verbose",
        ]
        for reg in insecure_registries:
            cmd.extend(["--insecure-registry", reg])
        proc = subprocess.Popen(
            cmd,
            cwd=cwd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )
        assert proc.stdout is not None and proc.stderr is not None

        def stream_fd(pipe, out_stream):
            for line in iter(pipe.readline, ""):
                out_stream.write(line.replace("host.docker.internal:5001", display_repo))
                out_stream.flush()
            pipe.close()

        t_out = Thread(target=stream_fd, args=(proc.stdout, sys.stdout))
        t_err = Thread(target=stream_fd, args=(proc.stderr, sys.stderr))
        t_out.daemon = True
        t_err.daemon = True
        t_out.start()
        t_err.start()
        proc.wait()
        t_out.join(timeout=5)
        t_err.join(timeout=5)
        if proc.returncode != 0:
            raise SystemExit(proc.returncode)
        builds.append({"tag": f"{display_repo}/{image_name}:{tag}"})
    for art in docker_artifacts:
        image_name = art["image"]
        context = art["context"]
        dockerfile = art["dockerfile"]
        full_image = f"{display_repo}/{image_name}:{tag}"
        context_dir = cwd / context
        dockerfile_path = context_dir / dockerfile
        if not dockerfile_path.exists():
            sys.stderr.write(f"Dockerfile not found: {dockerfile_path}\n")
            sys.stderr.flush()
            raise SystemExit(1)
        proc = subprocess.run(
            ["docker", "build", "-t", full_image, "-f", str(dockerfile_path), str(context_dir)],
            cwd=cwd,
        )
        if proc.returncode != 0:
            raise SystemExit(proc.returncode)
        proc = subprocess.run(["docker", "push", full_image], cwd=cwd)
        if proc.returncode != 0:
            raise SystemExit(proc.returncode)
        builds.append({"tag": full_image})
    write_cwd = (output_dir or cwd).resolve()
    out_path = write_build_result(builds, cwd=write_cwd)
    return out_path

"""Read/write build_result.json (image tag for downstream steps)."""

from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path

BUILD_RESULT_FILENAME = "build_result.json"


def build_result_path(cwd: Path | None = None) -> Path:
    return (cwd or Path.cwd()) / BUILD_RESULT_FILENAME


def read_build_result(cwd: Path | None = None) -> dict:
    """Read build_result.json; raise if missing or invalid."""
    path = build_result_path(cwd)
    if not path.exists():
        raise FileNotFoundError(f"{path} not found. Run 'push' first.")
    data = json.loads(path.read_text())
    if "builds" not in data or not data["builds"]:
        raise ValueError(f"{path}: expected 'builds' list with at least one entry")
    return data


def get_first_tag(build_result: dict) -> str:
    """First image tag from build_result (e.g. 'image-name:tag')."""
    first = build_result["builds"][0]
    if isinstance(first, dict) and "tag" in first:
        return first["tag"]
    if isinstance(first, str):
        return first
    raise ValueError("builds[0] must have 'tag' or be a string")


def find_tag_for_image(build_result: dict, image_name: str) -> str | None:
    """
    Return the full tag from build_result for the given image name, or None.
    Tags are like 'localhost:5001/sample-react-node-api:latest'; image_name is 'sample-react-node-api'.
    """
    for b in build_result.get("builds") or []:
        tag = b.get("tag") if isinstance(b, dict) else (b if isinstance(b, str) else None)
        if not tag:
            continue
        # Last segment is "image_name:tag"
        suffix = tag.split("/")[-1]
        if suffix.startswith(image_name + ":"):
            return tag
    return None


def write_build_result(builds: list[dict], cwd: Path | None = None) -> Path:
    """Write build_result.json. Each build: { 'tag': 'image:tag' } or { 'imageName', 'tag' }."""
    path = build_result_path(cwd)
    path.write_text(json.dumps({"builds": builds}, indent=2))
    return path


def parse_skaffold_output_for_tag(
    stdout: str,
    *,
    image_pattern: str | None = None,
) -> list[dict]:
    """
    Parse skaffold build output to extract image tags.
    If image_pattern is given, use regex with group 'image' and 'tag'; else use Skaffold --file-output.
    Fallback: look for last "Tagged ... as <repo>/<image>:<tag>" or "Built ... -> <image>:<tag>".
    """
    if image_pattern:
        rx = re.compile(image_pattern)
        builds = []
        for line in stdout.splitlines():
            m = rx.search(line)
            if m:
                g = m.groupdict()
                image = g.get("image", "").strip()
                tag = g.get("tag", "").strip()
                if image and tag:
                    builds.append({"tag": f"{image}:{tag}"})
        if builds:
            return builds
    # Fallback: common Skaffold/pack output patterns
    for pattern in [
        r"Tagged .+ as (?P<ref>[^\s]+)",
        r"Built .+ -> (?P<ref>[^\s]+)",
        r"(?P<ref>[a-zA-Z0-9][a-zA-Z0-9._/-]+:[a-zA-Z0-9][a-zA-Z0-9._-]+)",
    ]:
        rx = re.compile(pattern)
        for line in reversed(stdout.splitlines()):
            m = rx.search(line)
            if m:
                ref = m.group("ref" if "ref" in rx.groupindex else 0).strip()
                if "/" in ref or ":" in ref:
                    builds = [{"tag": ref}]
                    return builds
    return []


def run_skaffold_build_push(
    *,
    default_repo: str,
    profile: str | None = "push",
    cwd: Path | None = None,
    skaffold_cmd: str = "skaffold",
    skaffold_file: Path | None = None,
    image_pattern: str | None = None,
    use_file_output: bool = True,
    push: bool = False,
) -> Path:
    """
    Run skaffold build (with optional profile) and write build_result.json.
    When push=True, pass --push so images go to the registry (avoids daemon export issues).
    Prefer skaffold build --file-output build_result.json when available.
    Skaffold builds all artifacts (docker + buildpacks) in one invocation.
    """
    cwd = cwd or Path.cwd()
    if use_file_output:
        out_path = cwd / BUILD_RESULT_FILENAME
        cmd = [skaffold_cmd, "build"]
        if skaffold_file is not None:
            cmd.extend(["-f", str(skaffold_file)])
        cmd.extend(
            [
                "--file-output",
                str(out_path),
                "--default-repo",
                default_repo,
            ]
        )
        if push:
            cmd.append("--push")
        if profile:
            cmd.extend(["--profile", profile])
        proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
        if proc.returncode != 0:
            sys.stderr.write(proc.stderr or "")
            sys.stderr.write(proc.stdout or "")
            raise SystemExit(proc.returncode)
        # Skaffold --file-output format may differ; normalize to { "builds": [ { "tag": "..." } ] }
        data = json.loads(out_path.read_text())
        if data.get("builds"):
            normalized = []
            for b in data["builds"]:
                if isinstance(b, dict):
                    if "tag" in b and ":" in str(b["tag"]):
                        normalized.append({"tag": b["tag"]})
                    elif "imageName" in b and "tag" in b:
                        normalized.append({"tag": f"{b['imageName']}:{b['tag']}"})
                    else:
                        img, t = b.get("imageName", "app"), b.get("tag", "latest")
                        normalized.append({"tag": f"{default_repo}/{img}:{t}"})
                else:
                    normalized.append({"tag": str(b)})
            out_path.write_text(json.dumps({"builds": normalized}, indent=2))
        elif "image" in data or "tag" in data:
            img, t = data.get("image", "app"), data.get("tag", "latest")
            out_path.write_text(json.dumps({"builds": [{"tag": f"{default_repo}/{img}:{t}"}]}, indent=2))
        return out_path
    # No --file-output: run skaffold build and parse stdout
    cmd = [skaffold_cmd, "build"]
    if skaffold_file is not None:
        cmd.extend(["-f", str(skaffold_file)])
    cmd.extend(["--default-repo", default_repo])
    if push:
        cmd.append("--push")
    if profile:
        cmd.extend(["--profile", profile])
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True)
    stdout = proc.stdout or ""
    stderr = proc.stderr or ""
    if proc.returncode != 0:
        sys.stderr.write(stderr)
        sys.stderr.write(stdout)
        raise SystemExit(proc.returncode)
    builds = parse_skaffold_output_for_tag(stdout + "\n" + stderr, image_pattern=image_pattern)
    if not builds:
        sys.stderr.write("Could not parse image tag from skaffold output. Use --file-output or set image-pattern.\n")
        raise SystemExit(1)
    write_build_result(builds, cwd=cwd)
    return build_result_path(cwd)

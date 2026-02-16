"""Resolve build tag from CI (GITHUB_REF) or git for release-style image tagging."""

from __future__ import annotations

import os
import subprocess
from pathlib import Path


def resolve_build_tag(cwd: Path | None = None) -> tuple[str | None, bool]:
    """
    Resolve the image tag to use for this build, and whether to also tag as 'latest'.

    - In GitHub Actions: if GITHUB_REF is refs/tags/v* (e.g. refs/tags/v1.2.3), return
      (version without leading 'v', True) so images get e.g. 1.2.3 and latest.
    - Local/git: if HEAD has an exact tag (git describe --exact-match --tags HEAD),
      return (tag with optional 'v' stripped, True).
    - Otherwise: return (None, False) — use Skaffold default (hash) and do not add latest.

    Returns:
        (tag_string, add_latest): tag_string is the tag to pass to Skaffold (e.g. "1.2.3"),
        or None to use Skaffold default. add_latest is True only when we have a version tag
        (release build), so we also push the same images as :latest.
    """
    cwd = cwd or Path.cwd()

    # GitHub Actions: refs/tags/v1.2.3 → use 1.2.3 (or v1.2.3); add latest
    github_ref = os.environ.get("GITHUB_REF")
    if github_ref:
        if github_ref.startswith("refs/tags/"):
            raw = github_ref.removeprefix("refs/tags/")
            tag = raw[1:] if raw.startswith("v") else raw
            if tag:
                return (tag, True)
        # GITHUB_REF set but not a tag (e.g. refs/heads/main): do not fall back to git
        return (None, False)

    # Local: exact git tag on HEAD
    try:
        proc = subprocess.run(
            ["git", "describe", "--exact-match", "--tags", "HEAD"],
            cwd=cwd,
            capture_output=True,
            text=True,
            timeout=5,
        )
        if proc.returncode == 0 and proc.stdout:
            raw = proc.stdout.strip()
            tag = raw[1:] if raw.startswith("v") else raw
            if tag:
                return (tag, True)
    except (FileNotFoundError, subprocess.TimeoutExpired):
        pass

    return (None, False)

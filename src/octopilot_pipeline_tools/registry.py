"""
.registry file: push destinations for Skaffold (local + CI / multiple).

Format (YAML in repo root). Values support env interpolation:
  - ${VAR} or $VAR  → replaced by environment variable VAR
  - ${VAR:-default} → VAR if set, else "default"
  - $$ → literal $

Examples (GitHub Actions sets GITHUB_REPOSITORY_OWNER, GITHUB_REPOSITORY, etc.):
  local: localhost:5000
  ci:
    - ghcr.io/${GITHUB_REPOSITORY_OWNER}
    - ghcr.io/${GITHUB_REPOSITORY_OWNER:-my-org}
"""

from __future__ import annotations

import os
import re
from pathlib import Path
from typing import Any

REGISTRY_FILENAME = ".registry"

# ${VAR} or ${VAR:-default}
_RE_INTERPOLATE = re.compile(r"\$\{([^}:]+)(?::-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)|\$\$")


def _interpolate(s: str, env: dict[str, str] | None = None) -> str:
    """Replace ${VAR}, ${VAR:-default}, $VAR with env values; $$ → $."""
    env = env if env is not None else os.environ

    def repl(m: re.Match[str]) -> str:
        name, default, simple = m.group(1), m.group(2), m.group(3)
        if name is not None:
            return env.get(name, default or "")
        if simple is not None:
            return env.get(simple, "")
        return "$"

    return _RE_INTERPOLATE.sub(repl, s)


def _normalize_entry(entry: Any, env: dict[str, str] | None = None) -> str:
    if isinstance(entry, str):
        s = entry.strip().rstrip("/")
        return _interpolate(s, env)
    if isinstance(entry, dict) and "url" in entry:
        s = str(entry["url"]).strip().rstrip("/")
        return _interpolate(s, env)
    raise ValueError(f"Invalid registry entry: {entry!r}")


def load_registry_file(
    repo_root: Path | None = None,
    env: dict[str, str] | None = None,
) -> dict[str, Any]:
    """
    Load .registry YAML from repo root. Returns {
        "local": str | None,
        "ci": list[str],
    }. Missing file or empty => empty dict; invalid => raise.
    All string values are interpolated with env (default os.environ): ${VAR}, ${VAR:-default}, $VAR.
    """
    repo_root = repo_root or Path.cwd()
    path = repo_root / REGISTRY_FILENAME
    if not path.exists():
        return {"local": None, "ci": []}
    try:
        import yaml
    except ImportError:
        raise RuntimeError("PyYAML is required to read .registry. pip install pyyaml") from None
    raw = yaml.safe_load(path.read_text())
    if raw is None:
        return {"local": None, "ci": []}
    if not isinstance(raw, dict):
        raise ValueError(".registry must be a YAML object")
    env = env if env is not None else os.environ
    local = raw.get("local")
    if local is not None:
        local = _normalize_entry(local, env)
    ci_raw = raw.get("ci") or raw.get("destinations") or []
    if not isinstance(ci_raw, list):
        raise ValueError(".registry 'ci' must be a list")
    ci = [_normalize_entry(e, env) for e in ci_raw]
    return {"local": local, "ci": ci}


def get_push_registries(
    *,
    repo_root: Path | None = None,
    destination: str = "auto",
    in_ci: bool | None = None,
) -> list[str]:
    """
    Return list of registry URLs to push to.

    - destination: "local" | "ci" | "all" | "auto"
      - local: use .registry local (single)
      - ci: use .registry ci (one or more)
      - all: local + ci (deduplicated)
      - auto: in CI => ci, else local
    - in_ci: if None, inferred from GITHUB_ACTIONS env.
    """
    data = load_registry_file(repo_root)
    if in_ci is None:
        import os

        in_ci = os.environ.get("GITHUB_ACTIONS") == "true"
    if destination == "auto":
        destination = "ci" if in_ci else "local"
    if destination == "local":
        return [data["local"]] if data["local"] else []
    if destination == "ci":
        return list(data["ci"])
    if destination == "all":
        seen: set[str] = set()
        out: list[str] = []
        for url in ([data["local"]] if data["local"] else []) + data["ci"]:
            if url and url not in seen:
                seen.add(url)
                out.append(url)
        return out
    raise ValueError(f"destination must be local|ci|all|auto, got {destination!r}")


def get_default_repo_from_registry(
    repo_root: Path | None = None,
    destination: str = "auto",
    in_ci: bool | None = None,
) -> str | None:
    """First push registry from .registry (for --default-repo when not overridden)."""
    registries = get_push_registries(repo_root=repo_root, destination=destination, in_ci=in_ci)
    return registries[0] if registries else None

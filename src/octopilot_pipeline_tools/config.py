"""Load pipeline config from environment and optional properties file."""

from __future__ import annotations

import os
from pathlib import Path


def load_properties_file(path: Path) -> dict[str, str]:
    """Load key=value from a .properties-like file (skip comments and empty lines)."""
    result: dict[str, str] = {}
    if not path.exists():
        return result
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" in line:
            key, _, value = line.partition("=")
            result[key.strip()] = value.strip()
    return result


def get_config(properties_path: Path | None = None) -> dict[str, str]:
    """Merge env and optional properties file; env takes precedence."""
    config: dict[str, str] = {}
    if properties_path:
        config.update(load_properties_file(properties_path))
    for key, value in os.environ.items():
        if value is not None and value != "":
            config[key] = value
    return config


def get_default_repo(config: dict[str, str]) -> str | None:
    """SKAFFOLD_DEFAULT_REPO or GOOGLE_GKE_IMAGE_REPOSITORY (SAM convention)."""
    return (
        config.get("SKAFFOLD_DEFAULT_REPO")
        or config.get("GOOGLE_GKE_IMAGE_REPOSITORY")
    )


def get_watch_destination_repository(config: dict[str, str], environment: str) -> str | None:
    """Repository for watch-deployment by environment (dev, pp, prod)."""
    if environment == "dev":
        return config.get("GOOGLE_GKE_IMAGE_REPOSITORY") or config.get("WATCH_DESTINATION_REPOSITORY")
    if environment == "pp":
        return config.get("GOOGLE_GKE_IMAGE_PP_REPOSITORY") or config.get("WATCH_DESTINATION_REPOSITORY")
    if environment == "prod":
        return config.get("GOOGLE_GKE_IMAGE_PROD_REPOSITORY") or config.get("WATCH_DESTINATION_REPOSITORY")
    return config.get("WATCH_DESTINATION_REPOSITORY")


def get_promote_repositories(
    config: dict[str, str], source: str, destination: str
) -> tuple[str | None, str | None]:
    """(source_repo, dest_repo) for promote-image."""
    repo_map = {
        "dev": config.get("GOOGLE_GKE_IMAGE_REPOSITORY"),
        "pp": config.get("GOOGLE_GKE_IMAGE_PP_REPOSITORY"),
        "prod": config.get("GOOGLE_GKE_IMAGE_PROD_REPOSITORY"),
    }
    return (repo_map.get(source) or config.get("PROMOTE_SOURCE_REPOSITORY"),
            repo_map.get(destination) or config.get("PROMOTE_DESTINATION_REPOSITORY"))

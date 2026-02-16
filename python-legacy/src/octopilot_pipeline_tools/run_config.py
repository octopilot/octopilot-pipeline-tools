"""Load .github/octopilot.yaml for preconfigured ports, env, and volumes for `op run`."""

from __future__ import annotations

from pathlib import Path  # noqa: TC003
from typing import Any

from .infer_run_options import infer_run_options

RUN_CONFIG_FILENAME = ".github/octopilot.yaml"

_DEFAULT_PORTS = ["8080:8080"]
_DEFAULT_ENV = {"PORT": "8080"}


def load_run_config(cwd: Path) -> dict[str, Any]:
    """
    Load .github/octopilot.yaml from cwd. Returns a dict with:
      - default_repo: str | None
      - tag: str (default "latest")
      - contexts: dict[context_name, { ports: list[str], env: dict, volumes: list[str] }]
    Missing file or empty => empty dict. Invalid YAML => raise.
    """
    path = cwd / RUN_CONFIG_FILENAME
    if not path.exists():
        return {}
    try:
        import yaml
    except ImportError:
        raise RuntimeError("PyYAML required to read .github/octopilot.yaml. pip install pyyaml") from None
    raw = yaml.safe_load(path.read_text())
    if raw is None:
        return {}
    if not isinstance(raw, dict):
        raise ValueError(f"{RUN_CONFIG_FILENAME} must be a YAML object")
    return raw


def get_run_options_for_context(
    context_name: str,
    cwd: Path,
    *,
    config: dict[str, Any] | None = None,
    context_dir: Path | None = None,
) -> dict[str, Any]:
    """
    Return run options for a context: ports, env, volumes.
    When context_dir is provided, infers container_port and env from Procfile/project.toml/Dockerfile,
    then overrides from .github/octopilot.yaml. If octopilot does not set ports, returns
    ports=None and container_port=<inferred> so the caller can resolve host port at run time
    (e.g. find_free_port).
    """
    if config is None:
        config = load_run_config(cwd)
    contexts = config.get("contexts") or {}
    if not isinstance(contexts, dict):
        contexts = {}
    ctx_opts = contexts.get(context_name)
    if not isinstance(ctx_opts, dict):
        ctx_opts = {}

    if context_dir is not None:
        inferred = infer_run_options(context_dir)
        container_port = inferred["container_port"]
        env = {str(k): str(v) for k, v in inferred["env"].items()}
        # Octopilot overrides
        if ctx_opts.get("env") and isinstance(ctx_opts["env"], dict):
            env.update({str(k): str(v) for k, v in ctx_opts["env"].items()})
        octopilot_ports = ctx_opts.get("ports")
        if isinstance(octopilot_ports, list) and len(octopilot_ports) > 0:
            ports = [str(p) for p in octopilot_ports]
            return {
                "ports": ports,
                "env": env,
                "volumes": _volumes_from_ctx(ctx_opts),
                "container_port": container_port,
            }
        # No override: caller must resolve host port at run time
        volumes = _volumes_from_ctx(ctx_opts)
        return {"ports": None, "env": env, "volumes": volumes, "container_port": container_port}

    # Legacy path: no context_dir, use only octopilot + defaults
    ports = ctx_opts.get("ports")
    ports = _DEFAULT_PORTS.copy() if not isinstance(ports, list) else [str(p) for p in ports]
    env = ctx_opts.get("env")
    env = _DEFAULT_ENV.copy() if not isinstance(env, dict) else {str(k): str(v) for k, v in env.items()}
    volumes = _volumes_from_ctx(ctx_opts)
    return {"ports": ports, "env": env, "volumes": volumes, "container_port": 8080}


def _volumes_from_ctx(ctx_opts: dict) -> list[str]:
    volumes = ctx_opts.get("volumes")
    return [] if not isinstance(volumes, list) else [str(v) for v in volumes]


def get_default_repo_and_tag_for_run(cwd: Path, config: dict[str, Any] | None = None) -> tuple[str, str]:
    """Return (default_repo, tag) for op run. Uses .github/octopilot.yaml, else localhost:5001 and latest."""
    if config is None:
        config = load_run_config(cwd)
    default_repo = config.get("default_repo")
    if not default_repo or not isinstance(default_repo, str):
        default_repo = "localhost:5001"
    tag = config.get("tag")
    if not tag or not isinstance(tag, str):
        tag = "latest"
    return default_repo.strip().rstrip("/"), tag

"""Infer container port and env from Procfile / project.toml / Dockerfile in context dir."""

from __future__ import annotations

import re
from pathlib import Path  # noqa: TC003

_DEFAULT_CONTAINER_PORT = 8080
_DEFAULT_ENV = {"PORT": "8080"}


def infer_run_options(context_dir: Path) -> dict:
    """
    Infer container_port and env from context_dir (artifact's build context).
    Returns {"container_port": int, "env": dict}. Host port is not set (resolved at run time).
    """
    context_dir = context_dir.resolve()
    if not context_dir.is_dir():
        return {
            "container_port": _DEFAULT_CONTAINER_PORT,
            "env": dict(_DEFAULT_ENV),
        }

    # 1) Procfile: web process, look for PORT or --port / -p
    procfile = context_dir / "Procfile"
    if procfile.exists():
        port, env = _infer_from_procfile(procfile)
        if port is not None:
            env = dict(env or {}, PORT=str(port))
            return {"container_port": port, "env": env}
        return {"container_port": _DEFAULT_CONTAINER_PORT, "env": dict(_DEFAULT_ENV)}

    # 2) project.toml present -> default 8080
    if (context_dir / "project.toml").exists():
        return {"container_port": _DEFAULT_CONTAINER_PORT, "env": dict(_DEFAULT_ENV)}

    # 3) Dockerfile: optional EXPOSE
    dockerfile = context_dir / "Dockerfile"
    if dockerfile.exists():
        port = _infer_from_dockerfile(dockerfile)
        env = {"PORT": str(port)}
        return {"container_port": port, "env": env}

    # 4) nginx.conf: listen N;
    nginx = context_dir / "nginx.conf"
    if nginx.exists():
        port = _infer_from_nginx(nginx)
        if port is not None:
            return {"container_port": port, "env": {"PORT": str(port)}}

    return {"container_port": _DEFAULT_CONTAINER_PORT, "env": dict(_DEFAULT_ENV)}


def _infer_from_procfile(procfile: Path) -> tuple[int | None, dict | None]:
    """Parse Procfile for web process; look for ${PORT:-N} or --port N / -p N. Returns (port, env) or (None, None)."""
    text = procfile.read_text()
    web_line: str | None = None
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if ":" in line:
            name, rest = line.split(":", 1)
            if name.strip().lower() == "web":
                web_line = rest.strip()
                break
    if not web_line:
        # First process line if no web
        for line in text.splitlines():
            line = line.strip()
            if line and not line.startswith("#") and ":" in line:
                _, rest = line.split(":", 1)
                web_line = rest.strip()
                break
    if not web_line:
        return None, None

    # ${PORT:-N} or ${PORT:- N}
    m = re.search(r"\$\{PORT:-\s*(\d+)\}", web_line)
    if m:
        return int(m.group(1)), None
    # --port N or -p N
    m = re.search(r"(?:--port|-p)\s+(\d+)", web_line)
    if m:
        return int(m.group(1)), None
    return None, None


def _infer_from_dockerfile(dockerfile: Path) -> int:
    """First EXPOSE N in Dockerfile; else default 8080."""
    text = dockerfile.read_text()
    m = re.search(r"EXPOSE\s+(\d+)", text, re.IGNORECASE)
    if m:
        return int(m.group(1))
    return _DEFAULT_CONTAINER_PORT


def _infer_from_nginx(nginx_path: Path) -> int | None:
    """listen N; in nginx.conf."""
    text = nginx_path.read_text()
    m = re.search(r"listen\s+(\d+)\s*;", text)
    if m:
        return int(m.group(1))
    return None

"""
Start local registry-tls container, copy certs out, optionally trust them system-wide.

- Replaces any existing registry container.
- Copies certs from container to a local directory.
- On confirmation, installs cert for system trust (macOS Keychain or Linux ca-certificates);
  may prompt for sudo/password based on platform.
"""

from __future__ import annotations

import platform
import subprocess
import sys
import time
from pathlib import Path

REGISTRY_CONTAINER_NAME = "registry"
REGISTRY_IMAGE_DEFAULT = "ghcr.io/octopilot/registry-tls:latest"
CERT_SOURCE_PATH = "/etc/envoy/certs"
CERT_NAMES = ("tls.crt", "tls.key")


def _run(cmd: list[str], check: bool = True, capture: bool = False) -> subprocess.CompletedProcess:
    result = subprocess.run(
        cmd,
        capture_output=capture,
        text=capture,
    )
    if check and result.returncode != 0:
        raise RuntimeError(f"Command failed: {' '.join(cmd)}")
    return result


def _docker_ps_filter(name: str) -> list[str]:
    """Return list of container IDs with the given name (running or stopped)."""
    out = _run(
        ["docker", "ps", "-a", "--filter", f"name=^{name}$", "--format", "{{.ID}}"],
        capture=True,
    )
    return [s.strip() for s in (out.stdout or "").strip().splitlines() if s.strip()]


def _replace_registry_container(
    image: str,
    port: str = "5001:5001",
    volume: str = "registry-data:/var/lib/registry",
) -> str:
    """
    Stop/remove any existing registry container, run the given image, return container ID.
    """
    for cid in _docker_ps_filter(REGISTRY_CONTAINER_NAME):
        _run(["docker", "stop", cid], check=False)
        _run(["docker", "rm", cid], check=False)

    _run(
        [
            "docker",
            "run",
            "-d",
            "-p",
            port,
            "-v",
            volume,
            "--restart",
            "unless-stopped",
            "--name",
            REGISTRY_CONTAINER_NAME,
            image,
        ]
    )
    # Resolve ID
    ids = _docker_ps_filter(REGISTRY_CONTAINER_NAME)
    if not ids:
        raise RuntimeError("Container started but not found")
    return ids[0]


def _wait_for_certs(container_id: str, timeout_sec: int = 15) -> None:
    """Wait until tls.crt exists in the container (generated at startup)."""
    deadline = time.monotonic() + timeout_sec
    while time.monotonic() < deadline:
        r = subprocess.run(
            ["docker", "exec", container_id, "test", "-f", f"{CERT_SOURCE_PATH}/tls.crt"],
            capture_output=True,
        )
        if r.returncode == 0:
            return
        time.sleep(0.5)
    raise RuntimeError("Timed out waiting for certs inside container")


def _copy_certs_from_container(container_id: str, dest_dir: Path) -> Path:
    """Copy certs from container to dest_dir. Returns path to tls.crt."""
    dest_dir.mkdir(parents=True, exist_ok=True)
    _run(["docker", "cp", f"{container_id}:{CERT_SOURCE_PATH}/.", str(dest_dir)])
    crt = dest_dir / "tls.crt"
    if not crt.exists():
        raise RuntimeError(f"Expected {crt} after docker cp")
    return crt


def _is_macos() -> bool:
    return platform.system() == "Darwin"


def _is_linux() -> bool:
    return platform.system() == "Linux"


def _install_trust_macos(cert_path: Path, use_system_keychain: bool = True) -> None:
    """Add cert as trusted root on macOS. May prompt for password if use_system_keychain."""
    cert_path = cert_path.resolve()
    if not cert_path.exists():
        raise FileNotFoundError(cert_path)
    keychain = "/Library/Keychains/System.keychain" if use_system_keychain else None
    if keychain:
        _run(["sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", keychain, str(cert_path)])
    else:
        login_keychain = Path.home() / "Library/Keychains/login.keychain-db"
        if not login_keychain.exists():
            login_keychain = Path.home() / "Library/Keychains/login.keychain"
        _run(["security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", str(login_keychain), str(cert_path)])


def _install_trust_linux(cert_path: Path) -> None:
    """Copy cert to /usr/local/share/ca-certificates and run update-ca-certificates. Requires sudo."""
    cert_path = cert_path.resolve()
    if not cert_path.exists():
        raise FileNotFoundError(cert_path)
    dest_name = "registry-tls-localhost.crt"
    dest = f"/usr/local/share/ca-certificates/{dest_name}"
    _run(["sudo", "cp", str(cert_path), dest])
    _run(["sudo", "update-ca-certificates"])


def install_cert_trust(
    cert_path: Path,
    use_system_keychain_macos: bool = True,
) -> None:
    """
    Install an existing cert (e.g. from a previous start_registry) for system trust.
    May prompt for sudo/password. Supports macOS and Linux only.
    """
    cert_path = cert_path.resolve()
    if not cert_path.exists():
        raise FileNotFoundError(cert_path)
    if _is_macos():
        _install_trust_macos(cert_path, use_system_keychain=use_system_keychain_macos)
    elif _is_linux():
        _install_trust_linux(cert_path)
    else:
        raise RuntimeError("System trust is supported only on macOS and Linux")


def start_registry(
    image: str = REGISTRY_IMAGE_DEFAULT,
    certs_out_dir: Path | None = None,
    trust_cert: bool = False,
    use_system_keychain_macos: bool = True,
) -> Path:
    """
    Start (or replace) the registry container, copy certs out, optionally trust them.

    - image: Docker image (default ghcr.io/octopilot/registry-tls:latest).
    - certs_out_dir: Where to copy tls.crt/tls.key (default: ~/.config/registry-tls/certs).
    - trust_cert: If True, install cert for system trust (may prompt for sudo/password).
    - use_system_keychain_macos: If True (default), use System keychain on macOS (requires sudo);
      if False, use login keychain (no sudo).

    Returns path to the copied tls.crt.
    """
    if certs_out_dir is None:
        certs_out_dir = Path.home() / ".config" / "registry-tls" / "certs"

    _replace_registry_container(image)
    ids = _docker_ps_filter(REGISTRY_CONTAINER_NAME)
    if not ids:
        raise RuntimeError("Container not found after start")
    cid = ids[0]

    _wait_for_certs(cid)
    crt_path = _copy_certs_from_container(cid, certs_out_dir)

    if trust_cert:
        if _is_macos():
            _install_trust_macos(crt_path, use_system_keychain=use_system_keychain_macos)
        elif _is_linux():
            _install_trust_linux(crt_path)
        else:
            sys.stderr.write("System trust is supported only on macOS and Linux. Certs were copied.\n")

    return crt_path

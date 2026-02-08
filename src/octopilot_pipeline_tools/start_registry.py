"""
Start local registry-tls container, copy certs out, optionally trust them system-wide.

- Replaces any existing registry container.
- Copies certs from container to a local directory.
- On confirmation, installs cert for system trust (macOS Keychain or Linux ca-certificates)
  or Colima VM trust (--trust-cert-colima); may prompt for sudo/password.

**TLS only — do not expose HTTP (5000).** This image is built for HTTPS with a self-signed
cert. Exposing port 5000 (plain HTTP) would bypass TLS and make the cert setup pointless.
Clients (Docker daemon, pack lifecycle) must trust the cert (e.g. op start-registry
--trust-cert-colima on Colima) and use https://localhost:5001. Do not add a second -p
5000:5000 or similar in this module.
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
    Exposes only 5001 (TLS). Do not add HTTP (5000): use cert trust (e.g. --trust-cert-colima) instead.
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


# Host:port entries for the local registry in Colima; cert is installed for each
# so both "docker pull localhost:5001/..." and "docker pull host.docker.internal:5001/..." work.
COLIMA_REGISTRY_HOST_PORTS = ("localhost:5001", "host.docker.internal:5001")


def install_cert_trust_colima(
    cert_path: Path,
    registry_host_port: str | None = None,
    restart_colima: bool = True,
) -> None:
    """
    Install the registry's self-signed cert into the Colima VM so Docker (and pack
    lifecycle containers) trust HTTPS to the registry.

    - Writes the cert to /etc/docker/certs.d/<host:port>/ca.crt for both
      localhost:5001 and host.docker.internal:5001 (so pull works for either).
    - Optionally restarts Colima so the Docker daemon picks up the new CA.

    Requires colima to be on PATH and the VM to be running. May prompt for sudo
    when Colima runs the remote commands.
    """
    cert_path = cert_path.resolve()
    if not cert_path.exists():
        raise FileNotFoundError(cert_path)
    # Check colima is available and running
    r = subprocess.run(
        ["colima", "status"],
        capture_output=True,
        text=True,
    )
    if r.returncode != 0:
        raise RuntimeError("Colima is not running or not on PATH. Start it with: colima start")
    cert_content = cert_path.read_text()
    # Docker reads /etc/docker/certs.d/<host:port>/*.crt; install for both so
    # "docker pull localhost:5001/..." and "docker pull host.docker.internal:5001/..." work
    ports = (registry_host_port,) if registry_host_port else COLIMA_REGISTRY_HOST_PORTS
    dirs = " ".join(f"/etc/docker/certs.d/{p}" for p in ports)
    # Write cert once to /tmp then copy to each dir (single stdin pipe)
    script = f"mkdir -p {dirs} && cat > /tmp/registry-ca.crt && " + " && ".join(
        f"cp /tmp/registry-ca.crt /etc/docker/certs.d/{p}/ca.crt" for p in ports
    )
    proc = subprocess.run(
        ["colima", "ssh", "--", "sudo", "sh", "-c", script],
        input=cert_content,
        text=True,
        capture_output=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"Failed to install cert in Colima VM: {proc.stderr or proc.stdout or 'unknown'}")
    if restart_colima:
        sys.stderr.write("Restarting Colima so Docker picks up the new CA...\n")
        subprocess.run(["colima", "restart"], check=True)
    else:
        sys.stderr.write("Cert installed in Colima VM. Restart Colima (colima restart) for Docker to use it.\n")


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

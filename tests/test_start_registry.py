"""Tests for start_registry module."""

from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from octopilot_pipeline_tools.start_registry import (
    _docker_ps_filter,
    install_cert_trust,
    install_cert_trust_colima,
    start_registry,
)


@patch("octopilot_pipeline_tools.start_registry.subprocess.run")
def test_docker_ps_filter_empty(mock_run: MagicMock) -> None:
    mock_run.return_value = MagicMock(stdout="", returncode=0)
    assert _docker_ps_filter("registry") == []
    mock_run.assert_called_once()


@patch("octopilot_pipeline_tools.start_registry.subprocess.run")
def test_docker_ps_filter_one_id(mock_run: MagicMock) -> None:
    mock_run.return_value = MagicMock(stdout="abc123\n", returncode=0)
    assert _docker_ps_filter("registry") == ["abc123"]


@patch("octopilot_pipeline_tools.start_registry._install_trust_macos")
@patch("octopilot_pipeline_tools.start_registry.platform")
def test_install_cert_trust_macos(mock_platform: MagicMock, mock_install: MagicMock, tmp_path: Path) -> None:
    mock_platform.system.return_value = "Darwin"
    cert = tmp_path / "tls.crt"
    cert.write_text("PEM")
    install_cert_trust(cert, use_system_keychain_macos=True)
    mock_install.assert_called_once_with(cert.resolve(), use_system_keychain=True)


@patch("octopilot_pipeline_tools.start_registry._install_trust_linux")
@patch("octopilot_pipeline_tools.start_registry.platform")
def test_install_cert_trust_linux(mock_platform: MagicMock, mock_install: MagicMock, tmp_path: Path) -> None:
    mock_platform.system.return_value = "Linux"
    cert = tmp_path / "tls.crt"
    cert.write_text("PEM")
    install_cert_trust(cert, use_system_keychain_macos=False)
    mock_install.assert_called_once()


def test_install_cert_trust_missing_file() -> None:
    with pytest.raises(FileNotFoundError):
        install_cert_trust(Path("/nonexistent/tls.crt"))


@patch("octopilot_pipeline_tools.start_registry.platform")
def test_install_cert_trust_unsupported_platform(mock_platform: MagicMock, tmp_path: Path) -> None:
    mock_platform.system.return_value = "Windows"
    cert = tmp_path / "tls.crt"
    cert.write_text("PEM")
    with pytest.raises(RuntimeError, match="only on macOS and Linux"):
        install_cert_trust(cert)


@patch("octopilot_pipeline_tools.start_registry._copy_certs_from_container")
@patch("octopilot_pipeline_tools.start_registry._wait_for_certs")
@patch("octopilot_pipeline_tools.start_registry._docker_ps_filter")
@patch("octopilot_pipeline_tools.start_registry._replace_registry_container")
def test_start_registry_copies_certs(
    mock_replace: MagicMock,
    mock_ps: MagicMock,
    mock_wait: MagicMock,
    mock_copy: MagicMock,
    tmp_path: Path,
) -> None:
    mock_replace.return_value = "cid123"
    mock_ps.return_value = ["cid123"]
    mock_copy.return_value = tmp_path / "tls.crt"
    (tmp_path / "tls.crt").write_text("PEM")
    result = start_registry(
        image="myreg:latest",
        certs_out_dir=tmp_path,
        trust_cert=False,
    )
    mock_replace.assert_called_once_with("myreg:latest")
    mock_wait.assert_called_once_with("cid123")
    mock_copy.assert_called_once_with("cid123", tmp_path)
    assert result == tmp_path / "tls.crt"


@patch("octopilot_pipeline_tools.start_registry._install_trust_macos")
@patch("octopilot_pipeline_tools.start_registry.platform")
@patch("octopilot_pipeline_tools.start_registry._copy_certs_from_container")
@patch("octopilot_pipeline_tools.start_registry._wait_for_certs")
@patch("octopilot_pipeline_tools.start_registry._docker_ps_filter")
@patch("octopilot_pipeline_tools.start_registry._replace_registry_container")
def test_start_registry_with_trust_cert_macos(
    mock_replace: MagicMock,
    mock_ps: MagicMock,
    mock_wait: MagicMock,
    mock_copy: MagicMock,
    mock_platform: MagicMock,
    mock_install: MagicMock,
    tmp_path: Path,
) -> None:
    mock_replace.return_value = "cid123"
    mock_ps.return_value = ["cid123"]
    mock_copy.return_value = tmp_path / "tls.crt"
    (tmp_path / "tls.crt").write_text("PEM")
    mock_platform.system.return_value = "Darwin"
    result = start_registry(
        image="myreg:latest",
        certs_out_dir=tmp_path,
        trust_cert=True,
        use_system_keychain_macos=True,
    )
    mock_install.assert_called_once_with(tmp_path / "tls.crt", use_system_keychain=True)
    assert result == tmp_path / "tls.crt"


@patch("octopilot_pipeline_tools.start_registry._install_trust_linux")
@patch("octopilot_pipeline_tools.start_registry.platform")
@patch("octopilot_pipeline_tools.start_registry._copy_certs_from_container")
@patch("octopilot_pipeline_tools.start_registry._wait_for_certs")
@patch("octopilot_pipeline_tools.start_registry._docker_ps_filter")
@patch("octopilot_pipeline_tools.start_registry._replace_registry_container")
def test_start_registry_with_trust_cert_linux(
    mock_replace: MagicMock,
    mock_ps: MagicMock,
    mock_wait: MagicMock,
    mock_copy: MagicMock,
    mock_platform: MagicMock,
    mock_install: MagicMock,
    tmp_path: Path,
) -> None:
    mock_replace.return_value = "cid123"
    mock_ps.return_value = ["cid123"]
    mock_copy.return_value = tmp_path / "tls.crt"
    (tmp_path / "tls.crt").write_text("PEM")
    mock_platform.system.return_value = "Linux"
    result = start_registry(
        image="myreg:latest",
        certs_out_dir=tmp_path,
        trust_cert=True,
    )
    mock_install.assert_called_once()
    assert result == tmp_path / "tls.crt"


@patch("octopilot_pipeline_tools.start_registry.subprocess.run")
def test_install_cert_trust_colima_success(mock_run: MagicMock, tmp_path: Path) -> None:
    cert = tmp_path / "tls.crt"
    cert.write_text("-----BEGIN CERTIFICATE-----\nPEM\n-----END CERTIFICATE-----")
    mock_run.side_effect = [
        MagicMock(returncode=0, stdout="", stderr=""),  # colima status
        MagicMock(returncode=0, stdout="", stderr=""),  # colima ssh (install cert)
        MagicMock(returncode=0, stdout="", stderr=""),  # colima restart
    ]
    install_cert_trust_colima(cert, restart_colima=True)
    assert mock_run.call_count == 3
    assert mock_run.call_args_list[1][0][0][:3] == ["colima", "ssh", "--"]
    ssh_args = " ".join(mock_run.call_args_list[1][0][0])
    assert "localhost:5001" in ssh_args
    assert "host.docker.internal:5001" in ssh_args
    assert "registry.local:5001" in ssh_args
    assert mock_run.call_args_list[2][0][0] == ["colima", "restart"]


@patch("octopilot_pipeline_tools.start_registry.subprocess.run")
def test_install_cert_trust_colima_no_restart(mock_run: MagicMock, tmp_path: Path) -> None:
    cert = tmp_path / "tls.crt"
    cert.write_text("PEM")
    mock_run.side_effect = [
        MagicMock(returncode=0),
        MagicMock(returncode=0),
    ]
    install_cert_trust_colima(cert, restart_colima=False)
    assert mock_run.call_count == 2
    assert mock_run.call_args_list[1][0][0][:3] == ["colima", "ssh", "--"]


def test_install_cert_trust_colima_missing_file() -> None:
    with pytest.raises(FileNotFoundError):
        install_cert_trust_colima(Path("/nonexistent/tls.crt"))


@patch("octopilot_pipeline_tools.start_registry.subprocess.run")
def test_install_cert_trust_colima_not_running(mock_run: MagicMock, tmp_path: Path) -> None:
    cert = tmp_path / "tls.crt"
    cert.write_text("PEM")
    mock_run.return_value = MagicMock(returncode=1, stderr="colima is not running")
    with pytest.raises(RuntimeError, match="Colima is not running"):
        install_cert_trust_colima(cert)

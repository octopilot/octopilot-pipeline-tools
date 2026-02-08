from pathlib import Path
from unittest.mock import MagicMock, patch

from click.testing import CliRunner

from octopilot_pipeline_tools.cli import main

runner = CliRunner()


def test_build_help() -> None:
    r = runner.invoke(main, ["build", "--help"])
    assert r.exit_code == 0
    assert "skaffold build" in r.output or "build" in r.output


def test_push_requires_default_repo() -> None:
    r = runner.invoke(main, ["push"])
    assert r.exit_code != 0
    assert "default-repo" in r.output.lower() or "SKAFFOLD_DEFAULT_REPO" in r.output


def test_push_help() -> None:
    r = runner.invoke(main, ["push", "--help"])
    assert r.exit_code == 0
    assert "default-repo" in r.output


def test_watch_deployment_requires_component_and_env() -> None:
    r = runner.invoke(main, ["watch-deployment", "--component", "x", "--environment", "dev"])
    assert "component" in r.output or r.exit_code != 0 or "build_result" in r.output.lower()


def test_promote_image_help() -> None:
    r = runner.invoke(main, ["promote-image", "--help"])
    assert r.exit_code == 0
    assert "source" in r.output and "destination" in r.output


def test_build_push_help() -> None:
    r = runner.invoke(main, ["build-push", "--help"])
    assert r.exit_code == 0
    assert "--repo" in r.output and "pack" in r.output.lower()


@patch("octopilot_pipeline_tools.cli.run_pack_build_push_impl")
def test_build_push_uses_local_registry_by_default(mock_pack_build: MagicMock) -> None:
    mock_pack_build.return_value = Path("build_result.json")
    r = runner.invoke(main, ["build-push"])
    assert r.exit_code == 0
    mock_pack_build.assert_called_once()
    call_kw = mock_pack_build.call_args[1]
    assert call_kw["default_repo"] == "localhost:5001"
    assert call_kw["pack_cmd"] == "pack"


@patch("octopilot_pipeline_tools.cli.run_pack_build_push_impl")
def test_build_push_passes_pack_cmd(mock_pack_build: MagicMock) -> None:
    mock_pack_build.return_value = Path("build_result.json")
    r = runner.invoke(main, ["build-push", "--pack", "/opt/pack/out/pack"])
    assert r.exit_code == 0
    mock_pack_build.assert_called_once()
    call_kw = mock_pack_build.call_args[1]
    assert call_kw["pack_cmd"] == "/opt/pack/out/pack"


@patch("octopilot_pipeline_tools.cli.run_pack_build_push_impl")
def test_build_push_success(mock_pack_build: MagicMock) -> None:
    mock_pack_build.return_value = Path("build_result.json")
    r = runner.invoke(main, ["build-push", "--repo", "localhost:5001"])
    assert r.exit_code == 0
    mock_pack_build.assert_called_once()
    assert "Wrote" in r.output


@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_build_invokes_skaffold(mock_run: MagicMock) -> None:
    mock_run.return_value = MagicMock(returncode=0)
    r = runner.invoke(main, ["build"])
    assert r.exit_code == 0
    mock_run.assert_called_once()
    call_args = mock_run.call_args[0][0]
    assert "skaffold" in call_args[0]
    assert "build" in call_args


@patch("octopilot_pipeline_tools.cli.subprocess.run")
@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_push_success(mock_build_push: MagicMock, mock_subprocess: MagicMock) -> None:
    mock_build_push.return_value = Path("build_result.json")
    mock_subprocess.return_value = MagicMock(returncode=0)
    r = runner.invoke(main, ["push", "--default-repo", "reg.io/repo"])
    assert r.exit_code == 0
    mock_build_push.assert_called_once()
    assert "Wrote" in r.output


@patch("octopilot_pipeline_tools.cli.get_watch_destination_repository")
@patch("octopilot_pipeline_tools.cli.read_build_result")
@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_watch_deployment_success(
    mock_run: MagicMock,
    mock_read_build: MagicMock,
    mock_get_dest: MagicMock,
) -> None:
    mock_get_dest.return_value = "reg.io/repo"
    mock_read_build.return_value = {"builds": [{"tag": "app:tag123"}]}
    # First kubectl get deployment returns image with tag123, then rollout status succeeds
    mock_run.side_effect = [
        MagicMock(returncode=0),
        MagicMock(returncode=0, stdout="reg.io/repo/app:tag123"),
        MagicMock(returncode=0),
    ]
    r = runner.invoke(
        main,
        [
            "watch-deployment",
            "--component",
            "myapp",
            "--environment",
            "dev",
            "--namespace",
            "default",
        ],
    )
    assert r.exit_code == 0


@patch("octopilot_pipeline_tools.cli.get_promote_repositories")
@patch("octopilot_pipeline_tools.cli.read_build_result")
@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_promote_image_success(
    mock_run: MagicMock,
    mock_read_build: MagicMock,
    mock_get_promote: MagicMock,
) -> None:
    mock_get_promote.return_value = ("reg.io/dev", "reg.io/prod")
    mock_read_build.return_value = {"builds": [{"tag": "app:tag123"}]}
    mock_run.return_value = MagicMock(returncode=0)
    r = runner.invoke(
        main,
        ["promote-image", "--source", "dev", "--destination", "prod"],
    )
    assert r.exit_code == 0
    mock_run.assert_called_once()
    call_args = mock_run.call_args[0][0]
    assert "crane" in call_args
    assert "copy" in call_args


def test_run_help() -> None:
    r = runner.invoke(main, ["run"])
    assert r.exit_code == 0
    assert "context list" in r.output or "run" in r.output.lower()


def test_run_context_list(tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: app-api
      context: api
    - image: app-frontend
      context: frontend
""")
    r = runner.invoke(
        main,
        ["run", "context", "list", "--skaffold-file", str(skaffold)],
        obj={"config": {}},
    )
    assert r.exit_code == 0
    assert "api" in r.output and "frontend" in r.output


@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_run_context_invokes_docker(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(returncode=0)
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: myapp-api
      context: api
""")
    r = runner.invoke(
        main,
        ["run", "api", "--skaffold-file", str(skaffold)],
        obj={"config": {}},
    )
    assert r.exit_code == 0
    mock_run.assert_called_once()
    call_args = mock_run.call_args[0][0]
    assert call_args[0] == "docker"
    assert "run" in call_args
    assert "localhost:5001/myapp-api:latest" in call_args
    assert "-p" in call_args
    assert "-e" in call_args


def test_start_registry_help() -> None:
    r = runner.invoke(main, ["start-registry", "--help"])
    assert r.exit_code == 0
    assert "start-registry" in r.output
    assert "trust-cert" in r.output
    assert "5001" in r.output


@patch("octopilot_pipeline_tools.cli.start_registry")
def test_start_registry_invokes_module(mock_start_registry: MagicMock, tmp_path: Path) -> None:
    mock_start_registry.return_value = tmp_path / "tls.crt"
    r = runner.invoke(
        main,
        ["start-registry", "--certs-dir", str(tmp_path), "--image", "myreg:latest"],
        obj={"config": {}},
    )
    assert r.exit_code == 0
    mock_start_registry.assert_called_once()
    call_kw = mock_start_registry.call_args[1]
    assert call_kw["image"] == "myreg:latest"
    assert call_kw["certs_out_dir"] == tmp_path
    assert call_kw["trust_cert"] is False

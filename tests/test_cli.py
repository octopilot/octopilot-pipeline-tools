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

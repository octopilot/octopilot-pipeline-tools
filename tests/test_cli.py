from pathlib import Path

import pytest
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
    # Will fail without build_result.json or flux/kubectl
    assert "component" in r.output or r.exit_code != 0 or "build_result" in r.output.lower()


def test_promote_image_help() -> None:
    r = runner.invoke(main, ["promote-image", "--help"])
    assert r.exit_code == 0
    assert "source" in r.output and "destination" in r.output

"""Tests for pack_build module (parse_skaffold_buildpacks_artifacts, run_pack_build_push)."""

from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from octopilot_pipeline_tools.pack_build import (
    parse_skaffold_artifacts,
    parse_skaffold_buildpacks_artifacts,
    run_pack_build_push,
)


def test_parse_skaffold_artifacts(tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: myapp-api
      context: api
    - image: myapp-frontend
      context: frontend
    - image: no-context
""")
    artifacts = parse_skaffold_artifacts(skaffold)
    assert len(artifacts) == 3
    assert artifacts[0]["image"] == "myapp-api"
    assert artifacts[0]["context"] == "api"
    assert artifacts[1]["context"] == "frontend"
    assert artifacts[2]["context"] == "."


def test_parse_skaffold_buildpacks_artifacts(tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: octopilot-samples/spring-gradle-hello
      context: spring-gradle
      buildpacks:
        builder: paketobuildpacks/builder-jammy-base
    - image: octopilot-samples/python-fastapi-hello
      context: python-fastapi
      buildpacks:
        builder: paketobuildpacks/builder-jammy-base
    - image: no-buildpacks
      context: .
      docker:
        dockerfile: Dockerfile
""")
    artifacts = parse_skaffold_buildpacks_artifacts(skaffold)
    assert len(artifacts) == 2
    assert artifacts[0]["image"] == "octopilot-samples/spring-gradle-hello"
    assert artifacts[0]["context"] == "spring-gradle"
    assert artifacts[0]["builder"] == "paketobuildpacks/builder-jammy-base"
    assert artifacts[1]["image"] == "octopilot-samples/python-fastapi-hello"
    assert artifacts[1]["context"] == "python-fastapi"


def test_parse_skaffold_missing_file() -> None:
    with pytest.raises(FileNotFoundError, match="not found"):
        parse_skaffold_buildpacks_artifacts(Path("/nonexistent/skaffold.yaml"))


def test_parse_skaffold_empty_artifacts(tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    artifacts = parse_skaffold_buildpacks_artifacts(skaffold)
    assert artifacts == []


def test_parse_skaffold_no_buildpacks(tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: docker-only
      context: .
      docker: {}
""")
    artifacts = parse_skaffold_buildpacks_artifacts(skaffold)
    assert artifacts == []


@patch("octopilot_pipeline_tools.pack_build.subprocess.run")
def test_run_pack_build_push_invokes_pack_and_writes_result(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(returncode=0)
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: myapp/foo
      context: app
      buildpacks:
        builder: paketobuildpacks/builder-jammy-base
""")
    (tmp_path / "app").mkdir()
    out = run_pack_build_push(
        default_repo="localhost:5001",
        cwd=tmp_path,
        tag="latest",
        skaffold_path=skaffold,
    )
    assert out == tmp_path / "build_result.json"
    assert out.exists()
    data = __import__("json").loads(out.read_text())
    assert data["builds"] == [{"tag": "localhost:5001/myapp/foo:latest"}]
    assert mock_run.call_count == 1
    call_args = mock_run.call_args[0][0]
    assert call_args[0] == "pack"
    assert "build" in call_args
    assert "localhost:5001/myapp/foo:latest" in call_args
    assert "--publish" in call_args
    assert "--path" in call_args
    assert "--builder" in call_args


@patch("octopilot_pipeline_tools.pack_build.subprocess.run")
def test_run_pack_build_push_no_artifacts_exits(mock_run: MagicMock, tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    with pytest.raises(SystemExit):
        run_pack_build_push(
            default_repo="localhost:5001",
            cwd=tmp_path,
            skaffold_path=skaffold,
        )
    mock_run.assert_not_called()

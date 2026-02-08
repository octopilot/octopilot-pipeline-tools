"""Tests for pack_build module (parse_skaffold_buildpacks_artifacts, run_pack_build_push)."""

from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from octopilot_pipeline_tools.pack_build import (
    parse_skaffold_artifacts,
    parse_skaffold_buildpacks_artifacts,
    parse_skaffold_docker_artifacts,
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


def test_parse_skaffold_docker_artifacts(tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: sample-react-node-frontend
      context: frontend
      docker:
        dockerfile: Dockerfile
    - image: sample-react-node-api
      context: api
      buildpacks:
        builder: paketobuildpacks/builder-jammy-base
    - image: docker-only
      context: .
      docker: {}
""")
    artifacts = parse_skaffold_docker_artifacts(skaffold)
    assert len(artifacts) == 2
    assert artifacts[0]["image"] == "sample-react-node-frontend"
    assert artifacts[0]["context"] == "frontend"
    assert artifacts[0]["dockerfile"] == "Dockerfile"
    assert artifacts[1]["image"] == "docker-only"
    assert artifacts[1]["dockerfile"] == "Dockerfile"


def _mock_popen_success(returncode=0):
    mock_stdout = MagicMock()
    mock_stdout.readline.side_effect = [""]
    mock_stderr = MagicMock()
    mock_stderr.readline.side_effect = [""]
    proc = MagicMock()
    proc.stdout = mock_stdout
    proc.stderr = mock_stderr
    proc.returncode = returncode
    proc.wait = MagicMock()
    return proc


@patch("octopilot_pipeline_tools.pack_build.subprocess.Popen")
def test_run_pack_build_push_invokes_pack_and_writes_result(mock_popen: MagicMock, tmp_path: Path) -> None:
    mock_popen.return_value = _mock_popen_success()
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
    # build_result uses display_repo (localhost:5001); pack invoked with host.docker.internal:5001
    assert data["builds"] == [{"tag": "localhost:5001/myapp/foo:latest"}]
    assert mock_popen.call_count == 1
    call_kw = mock_popen.call_args[1]
    call_args = mock_popen.call_args[0][0]
    assert call_args[0] == "pack"
    assert "build" in call_args
    assert "host.docker.internal:5001/myapp/foo:latest" in call_args
    assert "--publish" in call_args
    assert "--path" in call_args
    assert "--builder" in call_args
    assert "--insecure-registry" in call_args
    assert "host.docker.internal:5001" in call_args
    assert call_kw.get("text") is True
    assert call_kw.get("bufsize") == 1


@patch("octopilot_pipeline_tools.pack_build.subprocess.Popen")
def test_run_pack_build_push_remote_repo_no_insecure_registry(mock_popen: MagicMock, tmp_path: Path) -> None:
    mock_popen.return_value = _mock_popen_success()
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: myapp
      context: app
      buildpacks:
        builder: paketobuildpacks/builder-jammy-base
""")
    (tmp_path / "app").mkdir()
    run_pack_build_push(
        default_repo="ghcr.io/org/repo",
        cwd=tmp_path,
        skaffold_path=skaffold,
    )
    call_args = mock_popen.call_args[0][0]
    assert "ghcr.io/org/repo/myapp:latest" in call_args
    assert "--insecure-registry" not in call_args


@patch("octopilot_pipeline_tools.pack_build.subprocess.run")
@patch("octopilot_pipeline_tools.pack_build.subprocess.Popen")
def test_run_pack_build_push_builds_both_buildpacks_and_docker(
    mock_popen: MagicMock,
    mock_run: MagicMock,
    tmp_path: Path,
) -> None:
    mock_popen.return_value = _mock_popen_success()
    mock_run.return_value = MagicMock(returncode=0)
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: myapp-api
      context: api
      buildpacks:
        builder: paketobuildpacks/builder-jammy-base
    - image: myapp-frontend
      context: frontend
      docker:
        dockerfile: Dockerfile
""")
    (tmp_path / "api").mkdir()
    (tmp_path / "frontend").mkdir()
    (tmp_path / "frontend" / "Dockerfile").write_text("FROM scratch\n")
    out = run_pack_build_push(
        default_repo="localhost:5001",
        cwd=tmp_path,
        tag="latest",
        skaffold_path=skaffold,
    )
    data = __import__("json").loads(out.read_text())
    tags = [b["tag"] for b in data["builds"]]
    assert "localhost:5001/myapp-api:latest" in tags
    assert "localhost:5001/myapp-frontend:latest" in tags
    assert mock_popen.call_count == 1
    assert mock_run.call_count == 2
    run_calls = [mock_run.call_args_list[i][0][0] for i in range(2)]
    assert any("docker" in c and "build" in c for c in run_calls)
    assert any("docker" in c and "push" in c for c in run_calls)


@patch("octopilot_pipeline_tools.pack_build.subprocess.Popen")
def test_run_pack_build_push_no_artifacts_exits(mock_popen: MagicMock, tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    with pytest.raises(SystemExit):
        run_pack_build_push(
            default_repo="localhost:5001",
            cwd=tmp_path,
            skaffold_path=skaffold,
        )
    mock_popen.assert_not_called()

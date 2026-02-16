from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from octopilot_pipeline_tools.build_result import (
    build_result_path,
    find_tag_for_image,
    get_first_tag,
    parse_skaffold_output_for_tag,
    read_build_result,
    run_skaffold_build_push,
    write_build_result,
)


def test_write_and_read_build_result(tmp_path: Path) -> None:
    builds = [{"tag": "myimage:abc1234-20250101120000"}]
    path = write_build_result(builds, cwd=tmp_path)
    assert path == tmp_path / "build_result.json"
    data = read_build_result(cwd=tmp_path)
    assert data["builds"] == builds
    assert get_first_tag(data) == "myimage:abc1234-20250101120000"


def test_read_build_result_two_images(tmp_path: Path) -> None:
    """When build_result.json has two builds (e.g. frontend + api), both are returned."""
    two = (
        '{"builds": [{"imageName": "sample-static-go-frontend", '
        '"tag": "host.docker.internal:5001/sample-static-go-frontend:849657d"}, '
        '{"imageName": "sample-static-go-api", '
        '"tag": "host.docker.internal:5001/sample-static-go-api:849657d"}]}'
    )
    (tmp_path / "build_result.json").write_text(two)
    data = read_build_result(cwd=tmp_path)
    assert len(data["builds"]) == 2
    tags = [b.get("tag") or b for b in data["builds"] if isinstance(b, dict)]
    assert "849657d" in tags[0] and "sample-static-go-frontend" in tags[0]
    assert "849657d" in tags[1] and "sample-static-go-api" in tags[1]


def test_read_build_result_missing(tmp_path: Path) -> None:
    with pytest.raises(FileNotFoundError, match="not found"):
        read_build_result(cwd=tmp_path)


def test_get_first_tag_formats() -> None:
    assert get_first_tag({"builds": [{"tag": "img:tag"}]}) == "img:tag"
    assert get_first_tag({"builds": ["img:tag"]}) == "img:tag"
    with pytest.raises(ValueError):
        get_first_tag({"builds": [{}]})


def test_parse_skaffold_output_for_tag_with_pattern() -> None:
    out = "Tagged myapp:abc1234-20250101120000\n"
    builds = parse_skaffold_output_for_tag(
        out,
        image_pattern=r"(?P<image>[a-z-]+):(?P<tag>[a-z0-9-]+)",
    )
    assert len(builds) == 1
    assert builds[0]["tag"] == "myapp:abc1234-20250101120000"


def test_parse_skaffold_output_tagged_line() -> None:
    out = "Tagged buildpacksio/lifecycle as gcr.io/repo/myapp:sha123-20250101120000\n"
    builds = parse_skaffold_output_for_tag(out)
    assert len(builds) >= 1
    assert ":" in builds[0]["tag"]


def test_parse_skaffold_output_built_line() -> None:
    out = "Built some context -> ghcr.io/org/app:v1\n"
    builds = parse_skaffold_output_for_tag(out)
    assert len(builds) == 1
    assert builds[0]["tag"] == "ghcr.io/org/app:v1"


def test_parse_skaffold_output_generic_ref() -> None:
    out = "log line\nmyimage:tag123\n"
    builds = parse_skaffold_output_for_tag(out)
    assert len(builds) == 1
    assert builds[0]["tag"] == "myimage:tag123"


def test_parse_skaffold_output_empty_returns_empty() -> None:
    assert parse_skaffold_output_for_tag("") == []
    assert parse_skaffold_output_for_tag("no match here\n") == []


def test_read_build_result_no_builds_key(tmp_path: Path) -> None:
    (tmp_path / "build_result.json").write_text("{}")
    with pytest.raises(ValueError, match="expected 'builds'"):
        read_build_result(cwd=tmp_path)


def test_read_build_result_empty_builds(tmp_path: Path) -> None:
    (tmp_path / "build_result.json").write_text('{"builds": []}')
    with pytest.raises(ValueError, match="expected 'builds'"):
        read_build_result(cwd=tmp_path)


def test_build_result_path(tmp_path: Path) -> None:
    p = build_result_path()
    assert p.name == "build_result.json"
    p2 = build_result_path(cwd=tmp_path)
    assert p2 == tmp_path / "build_result.json"


def test_find_tag_for_image() -> None:
    data = {
        "builds": [
            {"tag": "localhost:5001/sample-react-node-api:latest"},
            {"tag": "localhost:5001/sample-react-node-frontend:latest"},
        ]
    }
    assert find_tag_for_image(data, "sample-react-node-api") == "localhost:5001/sample-react-node-api:latest"
    assert find_tag_for_image(data, "sample-react-node-frontend") == "localhost:5001/sample-react-node-frontend:latest"
    assert find_tag_for_image(data, "other-image") is None
    assert find_tag_for_image({"builds": []}, "any") is None


@patch("subprocess.run")
def test_run_skaffold_build_push_file_output_success(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(returncode=0)
    # Pre-create file (mock skaffold doesn't write it); imageName+tag branch
    (tmp_path / "build_result.json").write_text('{"builds": [{"imageName": "app", "tag": "abc123"}]}')
    out = run_skaffold_build_push(
        default_repo="ghcr.io/org",
        profile="push",
        cwd=tmp_path,
        use_file_output=True,
        skaffold_cmd="echo",
    )
    assert out == tmp_path / "build_result.json"
    data = read_build_result(cwd=tmp_path)
    assert data["builds"][0]["tag"] == "app:abc123"


@patch("subprocess.run")
def test_run_skaffold_build_push_file_output_image_tag_keys(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(returncode=0)
    # Top-level image/tag (no "builds" key) -> single build with default_repo/image:tag
    (tmp_path / "build_result.json").write_text('{"image": "myapp", "tag": "v1"}')
    run_skaffold_build_push(
        default_repo="reg.io/r",
        cwd=tmp_path,
        use_file_output=True,
        skaffold_cmd="echo",
    )
    data = read_build_result(cwd=tmp_path)
    assert data["builds"][0]["tag"] == "reg.io/r/myapp:v1"


@patch("subprocess.run")
def test_run_skaffold_build_push_file_output_two_images(mock_run: MagicMock, tmp_path: Path) -> None:
    """When Skaffold --file-output writes two images (e.g. frontend + api), build_result has two entries."""
    mock_run.return_value = MagicMock(returncode=0)
    repo = "host.docker.internal:5001"
    two_builds_json = (
        '{"builds": ['
        f'{{"imageName": "sample-static-go-frontend", "tag": "{repo}/sample-static-go-frontend:849657d"}}, '
        f'{{"imageName": "sample-static-go-api", "tag": "{repo}/sample-static-go-api:849657d"}}'
        "]}"
    )
    (tmp_path / "build_result.json").write_text(two_builds_json)
    out = run_skaffold_build_push(
        default_repo=repo,
        profile=None,
        cwd=tmp_path,
        use_file_output=True,
        skaffold_cmd="echo",
    )
    assert out == tmp_path / "build_result.json"
    data = read_build_result(cwd=tmp_path)
    assert len(data["builds"]) == 2
    assert any("sample-static-go-frontend" in str(b.get("tag", "")) for b in data["builds"])
    assert any("sample-static-go-api" in str(b.get("tag", "")) for b in data["builds"])


@patch("subprocess.run")
def test_run_skaffold_build_push_stdout_parse_success(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(
        returncode=0,
        stdout="Tagged buildpacksio/lifecycle as reg.io/app:tag1",
        stderr="",
    )
    run_skaffold_build_push(
        default_repo="reg.io",
        cwd=tmp_path,
        use_file_output=False,
        skaffold_cmd="echo",
    )
    data = read_build_result(cwd=tmp_path)
    assert data["builds"][0]["tag"] == "reg.io/app:tag1"


@patch("subprocess.run")
def test_run_skaffold_build_push_stdout_parse_fails_exits(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(returncode=0, stdout="no tag here", stderr="")
    with pytest.raises(SystemExit):
        run_skaffold_build_push(
            default_repo="reg.io",
            cwd=tmp_path,
            use_file_output=False,
            skaffold_cmd="echo",
        )


@patch("subprocess.run")
def test_run_skaffold_build_push_skaffold_fails_exits(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(returncode=1, stdout="", stderr="error")
    with pytest.raises(SystemExit):
        run_skaffold_build_push(
            default_repo="reg.io",
            cwd=tmp_path,
            use_file_output=True,
            skaffold_cmd="echo",
        )

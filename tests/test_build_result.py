import json
from pathlib import Path

import pytest

from octopilot_pipeline_tools.build_result import (
    build_result_path,
    get_first_tag,
    parse_skaffold_output_for_tag,
    read_build_result,
    write_build_result,
)


def test_write_and_read_build_result(tmp_path: Path) -> None:
    builds = [{"tag": "myimage:abc1234-20250101120000"}]
    path = write_build_result(builds, cwd=tmp_path)
    assert path == tmp_path / "build_result.json"
    data = read_build_result(cwd=tmp_path)
    assert data["builds"] == builds
    assert get_first_tag(data) == "myimage:abc1234-20250101120000"


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

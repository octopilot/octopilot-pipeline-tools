"""Tests for tag_resolution.resolve_build_tag."""

from __future__ import annotations

import os
from pathlib import Path
from unittest.mock import MagicMock, patch

from octopilot_pipeline_tools.tag_resolution import resolve_build_tag


def test_resolve_build_tag_github_ref_tag_with_v() -> None:
    """GITHUB_REF=refs/tags/v1.2.3 returns (1.2.3, True)."""
    with patch.dict(os.environ, {"GITHUB_REF": "refs/tags/v1.2.3"}, clear=False):
        tag, add_latest = resolve_build_tag()
    assert tag == "1.2.3"
    assert add_latest is True


def test_resolve_build_tag_github_ref_tag_without_v() -> None:
    """GITHUB_REF=refs/tags/1.2.3 returns (1.2.3, True)."""
    with patch.dict(os.environ, {"GITHUB_REF": "refs/tags/1.2.3"}, clear=False):
        tag, add_latest = resolve_build_tag()
    assert tag == "1.2.3"
    assert add_latest is True


def test_resolve_build_tag_github_ref_not_tag() -> None:
    """GITHUB_REF=refs/heads/main does not yield a version tag."""
    with patch.dict(os.environ, {"GITHUB_REF": "refs/heads/main"}, clear=False):
        tag, add_latest = resolve_build_tag()
    assert tag is None
    assert add_latest is False


def test_resolve_build_tag_no_github_ref_git_describe_fails() -> None:
    """Without GITHUB_REF and git describe failing returns (None, False)."""
    with (
        patch("octopilot_pipeline_tools.tag_resolution.os.environ.get", return_value=None),
        patch("octopilot_pipeline_tools.tag_resolution.subprocess.run") as mock_run,
    ):
        mock_run.return_value = MagicMock(returncode=128, stdout="", stderr="")
        tag, add_latest = resolve_build_tag()
    assert tag is None
    assert add_latest is False


def test_resolve_build_tag_no_github_ref_git_describe_succeeds() -> None:
    """Without GITHUB_REF, git describe --exact-match returns tag → (version, True)."""
    with (
        patch("octopilot_pipeline_tools.tag_resolution.os.environ.get", return_value=None),
        patch("octopilot_pipeline_tools.tag_resolution.subprocess.run") as mock_run,
    ):
        mock_run.return_value = MagicMock(returncode=0, stdout="v2.0.0\n", stderr="")
        tag, add_latest = resolve_build_tag()
    assert tag == "2.0.0"
    assert add_latest is True


def test_resolve_build_tag_cwd_passed_to_git() -> None:
    """resolve_build_tag(cwd=path) runs git in that directory."""
    cwd = Path("/some/repo")
    with (
        patch("octopilot_pipeline_tools.tag_resolution.os.environ.get", return_value=None),
        patch("octopilot_pipeline_tools.tag_resolution.subprocess.run") as mock_run,
    ):
        mock_run.return_value = MagicMock(returncode=128, stdout="", stderr="")
        resolve_build_tag(cwd=cwd)
    mock_run.assert_called_once()
    assert mock_run.call_args[1]["cwd"] == cwd

"""Tests for run_config module (.github/octopilot.yaml loading)."""

from pathlib import Path

from octopilot_pipeline_tools.run_config import (
    RUN_CONFIG_FILENAME,
    get_default_repo_and_tag_for_run,
    get_run_options_for_context,
    load_run_config,
)


def _octopilot_path(tmp_path: Path) -> Path:
    """Path to .github/octopilot.yaml under tmp_path."""
    p = tmp_path / RUN_CONFIG_FILENAME
    p.parent.mkdir(parents=True, exist_ok=True)
    return p


def test_load_run_config_missing(tmp_path: Path) -> None:
    assert load_run_config(tmp_path) == {}


def test_load_run_config_empty_file(tmp_path: Path) -> None:
    _octopilot_path(tmp_path).write_text("")
    assert load_run_config(tmp_path) == {}


def test_load_run_config_with_contexts(tmp_path: Path) -> None:
    _octopilot_path(tmp_path).write_text("""
default_repo: localhost:5001
tag: latest
contexts:
  api:
    ports: ["8081:8080"]
    env:
      PORT: "8080"
  frontend:
    ports: ["8080:8080"]
    env:
      PORT: "8080"
""")
    cfg = load_run_config(tmp_path)
    assert cfg.get("default_repo") == "localhost:5001"
    assert cfg.get("tag") == "latest"
    assert "api" in cfg.get("contexts", {})
    assert cfg["contexts"]["api"]["ports"] == ["8081:8080"]


def test_get_run_options_for_context_from_file(tmp_path: Path) -> None:
    _octopilot_path(tmp_path).write_text("""
contexts:
  api:
    ports: ["8081:8080"]
    env:
      PORT: "8080"
      FOO: "bar"
""")
    opts = get_run_options_for_context("api", tmp_path)
    assert opts["ports"] == ["8081:8080"]
    assert opts["env"]["PORT"] == "8080"
    assert opts["env"]["FOO"] == "bar"
    assert opts["volumes"] == []


def test_get_run_options_for_context_defaults(tmp_path: Path) -> None:
    opts = get_run_options_for_context("unknown", tmp_path)
    assert opts["ports"] == ["8080:8080"]
    assert opts["env"] == {"PORT": "8080"}
    assert opts["volumes"] == []


def test_get_default_repo_and_tag_for_run(tmp_path: Path) -> None:
    repo, tag = get_default_repo_and_tag_for_run(tmp_path)
    assert repo == "localhost:5001"
    assert tag == "latest"
    _octopilot_path(tmp_path).write_text("default_repo: ghcr.io/org\ntag: v1\n")
    repo, tag = get_default_repo_and_tag_for_run(tmp_path)
    assert repo == "ghcr.io/org"
    assert tag == "v1"

"""Tests for infer_run_options (Procfile / project.toml / Dockerfile)."""

from pathlib import Path

from octopilot_pipeline_tools.infer_run_options import infer_run_options


def test_infer_run_options_no_dir_returns_defaults(tmp_path: Path) -> None:
    # Non-existent or file: default 8080
    opts = infer_run_options(tmp_path / "missing")
    assert opts["container_port"] == 8080
    assert opts["env"]["PORT"] == "8080"


def test_infer_run_options_procfile_port_syntax(tmp_path: Path) -> None:
    (tmp_path / "Procfile").write_text("web: node server.js --port ${PORT:-3000}\n")
    opts = infer_run_options(tmp_path)
    assert opts["container_port"] == 3000
    assert opts["env"]["PORT"] == "3000"


def test_infer_run_options_procfile_web_first(tmp_path: Path) -> None:
    (tmp_path / "Procfile").write_text("worker: python worker.py\nweb: uwsgi --http :${PORT:-8080}\n")
    opts = infer_run_options(tmp_path)
    assert opts["container_port"] == 8080
    assert opts["env"]["PORT"] == "8080"


def test_infer_run_options_project_toml_default(tmp_path: Path) -> None:
    (tmp_path / "project.toml").write_text('[project]\nname = "app"\n')
    opts = infer_run_options(tmp_path)
    assert opts["container_port"] == 8080
    assert opts["env"]["PORT"] == "8080"


def test_infer_run_options_dockerfile_expose(tmp_path: Path) -> None:
    (tmp_path / "Dockerfile").write_text("FROM nginx\nEXPOSE 9000\n")
    opts = infer_run_options(tmp_path)
    assert opts["container_port"] == 9000
    assert opts["env"]["PORT"] == "9000"


def test_infer_run_options_dockerfile_no_expose_default(tmp_path: Path) -> None:
    (tmp_path / "Dockerfile").write_text('FROM node:20\nCOPY . .\nCMD ["node", "app"]\n')
    opts = infer_run_options(tmp_path)
    assert opts["container_port"] == 8080
    assert opts["env"]["PORT"] == "8080"


def test_infer_run_options_nginx_listen(tmp_path: Path) -> None:
    (tmp_path / "nginx.conf").write_text("server { listen 3000; }\n")
    opts = infer_run_options(tmp_path)
    assert opts["container_port"] == 3000
    assert opts["env"]["PORT"] == "3000"


def test_infer_run_options_empty_dir_default(tmp_path: Path) -> None:
    opts = infer_run_options(tmp_path)
    assert opts["container_port"] == 8080
    assert opts["env"]["PORT"] == "8080"

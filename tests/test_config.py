import os
from pathlib import Path

from octopilot_pipeline_tools.config import (
    get_config,
    get_default_repo,
    get_promote_repositories,
    get_watch_destination_repository,
    load_properties_file,
)


def test_load_properties_file_empty(tmp_path: Path) -> None:
    assert load_properties_file(tmp_path / "missing") == {}


def test_load_properties_file(tmp_path: Path) -> None:
    p = tmp_path / "p.properties"
    p.write_text("# comment\nKEY1=value1\n\nKEY2=value2\n")
    assert load_properties_file(p) == {"KEY1": "value1", "KEY2": "value2"}


def test_get_config_env_overrides_properties(tmp_path: Path) -> None:
    (tmp_path / "p.properties").write_text("FOO=from_file\n")
    os.environ["FOO"] = "from_env"
    try:
        config = get_config(tmp_path / "p.properties")
        assert config["FOO"] == "from_env"
    finally:
        os.environ.pop("FOO", None)


def test_get_default_repo() -> None:
    assert get_default_repo({}) is None
    assert get_default_repo({"SKAFFOLD_DEFAULT_REPO": "localhost:5000"}) == "localhost:5000"
    assert get_default_repo({"GOOGLE_GKE_IMAGE_REPOSITORY": "gcr.io/proj/repo"}) == "gcr.io/proj/repo"
    assert get_default_repo({"SKAFFOLD_DEFAULT_REPO": "a", "GOOGLE_GKE_IMAGE_REPOSITORY": "b"}) == "a"


def test_get_watch_destination_repository() -> None:
    config = {
        "GOOGLE_GKE_IMAGE_REPOSITORY": "dev.repo",
        "GOOGLE_GKE_IMAGE_PP_REPOSITORY": "pp.repo",
        "GOOGLE_GKE_IMAGE_PROD_REPOSITORY": "prod.repo",
    }
    assert get_watch_destination_repository(config, "dev") == "dev.repo"
    assert get_watch_destination_repository(config, "pp") == "pp.repo"
    assert get_watch_destination_repository(config, "prod") == "prod.repo"


def test_get_watch_destination_repository_fallback_and_unknown_env() -> None:
    # Unknown environment returns WATCH_DESTINATION_REPOSITORY
    assert get_watch_destination_repository({"WATCH_DESTINATION_REPOSITORY": "custom"}, "staging") == "custom"
    # dev falls back to WATCH_DESTINATION_REPOSITORY if GOOGLE_GKE_* not set
    assert get_watch_destination_repository({"WATCH_DESTINATION_REPOSITORY": "fallback"}, "dev") == "fallback"


def test_get_promote_repositories() -> None:
    config = {
        "GOOGLE_GKE_IMAGE_REPOSITORY": "dev.repo",
        "GOOGLE_GKE_IMAGE_PP_REPOSITORY": "pp.repo",
        "GOOGLE_GKE_IMAGE_PROD_REPOSITORY": "prod.repo",
    }
    src, dest = get_promote_repositories(config, "dev", "pp")
    assert src == "dev.repo"
    assert dest == "pp.repo"
    # Fallback to PROMOTE_* when env not in repo_map
    config2 = {"PROMOTE_SOURCE_REPOSITORY": "src.io", "PROMOTE_DESTINATION_REPOSITORY": "dest.io"}
    src2, dest2 = get_promote_repositories(config2, "dev", "pp")
    assert src2 == "src.io"
    assert dest2 == "dest.io"

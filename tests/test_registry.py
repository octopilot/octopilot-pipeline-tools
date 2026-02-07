from pathlib import Path

import pytest

from octopilot_pipeline_tools.registry import (
    REGISTRY_FILENAME,
    get_default_repo_from_registry,
    get_push_registries,
    load_registry_file,
)


def test_load_registry_file_missing(tmp_path: Path) -> None:
    assert load_registry_file(tmp_path) == {"local": None, "ci": []}


def test_load_registry_file_empty(tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text("")
    # empty YAML -> None
    assert load_registry_file(tmp_path) == {"local": None, "ci": []}


def test_load_registry_file_local_and_ci(tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text(
        "local: localhost:5000\nci:\n  - ghcr.io/my-org\n  - europe-west1-docker.pkg.dev/proj/repo\n"
    )
    data = load_registry_file(tmp_path)
    assert data["local"] == "localhost:5000"
    assert data["ci"] == ["ghcr.io/my-org", "europe-west1-docker.pkg.dev/proj/repo"]


def test_load_registry_file_ci_with_url_objects(tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text("ci:\n  - url: ghcr.io/octopilot\n    name: ghcr\n  - docker.io/user\n")
    data = load_registry_file(tmp_path)
    assert data["ci"] == ["ghcr.io/octopilot", "docker.io/user"]


def test_get_push_registries_auto_local(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text("local: localhost:5000\nci:\n  - ghcr.io/org\n")
    monkeypatch.delenv("GITHUB_ACTIONS", raising=False)
    assert get_push_registries(repo_root=tmp_path, destination="auto", in_ci=False) == ["localhost:5000"]
    assert get_push_registries(repo_root=tmp_path, destination="auto", in_ci=True) == ["ghcr.io/org"]


def test_get_push_registries_destination(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text("local: localhost:5000\nci:\n  - ghcr.io/org\n  - gcr.io/p\n")
    assert get_push_registries(repo_root=tmp_path, destination="local") == ["localhost:5000"]
    assert get_push_registries(repo_root=tmp_path, destination="ci") == ["ghcr.io/org", "gcr.io/p"]
    all_reg = get_push_registries(repo_root=tmp_path, destination="all")
    assert "localhost:5000" in all_reg
    assert "ghcr.io/org" in all_reg


def test_get_default_repo_from_registry(tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text("local: localhost:5000\nci:\n  - ghcr.io/org\n")
    assert get_default_repo_from_registry(repo_root=tmp_path, destination="local") == "localhost:5000"
    assert get_default_repo_from_registry(repo_root=tmp_path, destination="ci") == "ghcr.io/org"
    assert get_default_repo_from_registry(repo_root=tmp_path / "nonexistent") is None


def test_get_push_registries_empty_when_no_file(tmp_path: Path) -> None:
    assert get_push_registries(repo_root=tmp_path, destination="local") == []
    assert get_default_repo_from_registry(repo_root=tmp_path) is None


def test_registry_interpolation(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text(
        "local: localhost:${PORT:-5000}\n"
        "ci:\n"
        "  - ghcr.io/${GITHUB_REPOSITORY_OWNER}\n"
        "  - ghcr.io/${MISSING:-fallback}\n"
    )
    monkeypatch.setenv("PORT", "9999")
    monkeypatch.setenv("GITHUB_REPOSITORY_OWNER", "octopilot")
    data = load_registry_file(tmp_path)
    assert data["local"] == "localhost:9999"
    assert data["ci"] == ["ghcr.io/octopilot", "ghcr.io/fallback"]


def test_registry_interpolation_dollar_dollar(tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text('local: "prefix$$suffix"\nci: []\n')
    data = load_registry_file(tmp_path)
    assert data["local"] == "prefix$suffix"


def test_load_registry_file_invalid_not_dict(tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text("local: localhost\n")  # YAML list or scalar
    (tmp_path / REGISTRY_FILENAME).write_text('["list"]')
    with pytest.raises(ValueError, match="must be a YAML object"):
        load_registry_file(tmp_path)


def test_load_registry_file_ci_not_list(tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text("local: localhost:5000\nci: not-a-list\n")
    with pytest.raises(ValueError, match="'ci' must be a list"):
        load_registry_file(tmp_path)


def test_get_push_registries_invalid_destination(tmp_path: Path) -> None:
    (tmp_path / REGISTRY_FILENAME).write_text("local: localhost:5000\nci: []\n")
    with pytest.raises(ValueError, match="destination must be local"):
        get_push_registries(repo_root=tmp_path, destination="invalid")

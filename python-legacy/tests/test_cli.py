import os
from pathlib import Path
from unittest.mock import MagicMock, patch

from click.testing import CliRunner

from octopilot_pipeline_tools.cli import main
from octopilot_pipeline_tools.run_config import RUN_CONFIG_FILENAME

runner = CliRunner()


def _write_octopilot(tmp_path: Path, content: str) -> None:
    p = tmp_path / RUN_CONFIG_FILENAME
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(content)


def test_build_help() -> None:
    """op build --help shows group with status subcommand."""
    r = runner.invoke(main, ["build", "--help"])
    assert r.exit_code == 0
    assert "build" in r.output
    assert "status" in r.output
    assert "Commands:" in r.output


def test_build_status_help() -> None:
    """op build status --help shows status-specific options."""
    r = runner.invoke(main, ["build", "status", "--help"])
    assert r.exit_code == 0
    assert "status" in r.output
    assert "--repo" in r.output
    assert "build_result.json" in r.output or "cache" in r.output.lower()


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
    assert "component" in r.output or r.exit_code != 0 or "build_result" in r.output.lower()


def test_promote_image_help() -> None:
    r = runner.invoke(main, ["promote-image", "--help"])
    assert r.exit_code == 0
    assert "source" in r.output and "destination" in r.output


def test_build_push_help() -> None:
    r = runner.invoke(main, ["build-push", "--help"])
    assert r.exit_code == 0
    assert "--repo" in r.output and "skaffold" in r.output.lower()


@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_build_push_uses_skaffold(mock_skaffold: MagicMock, tmp_path: Path) -> None:
    """build-push runs Skaffold only; no fallback."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    mock_skaffold.return_value = tmp_path / "build_result.json"
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build-push"])
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    mock_skaffold.assert_called_once()
    call_kw = mock_skaffold.call_args[1]
    assert call_kw["default_repo"] == "localhost:5001"
    assert call_kw["push"] is True
    assert call_kw["cache_artifacts"] is False


@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push", side_effect=SystemExit(2))
def test_build_push_fails_when_skaffold_fails(_mock_skaffold: MagicMock, tmp_path: Path) -> None:
    """When Skaffold fails, build-push exits with same code (no fallback)."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build-push"])
    finally:
        os.chdir(old)
    assert r.exit_code == 2


@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_build_push_uses_default_repo_from_run_yaml(mock_skaffold: MagicMock, tmp_path: Path) -> None:
    """op build-push uses default_repo from .github/octopilot.yaml when --repo not passed."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    _write_octopilot(tmp_path, "default_repo: myreg:5000\n")
    mock_skaffold.return_value = tmp_path / "build_result.json"
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build-push"])
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    call_kw = mock_skaffold.call_args[1]
    assert call_kw["default_repo"] == "myreg:5000"
    assert call_kw["cache_artifacts"] is False


@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_build_push_repo_option_overrides_run_yaml(mock_skaffold: MagicMock, tmp_path: Path) -> None:
    """op build-push --repo overrides .github/octopilot.yaml default_repo."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    _write_octopilot(tmp_path, "default_repo: registry.local:5001\n")
    mock_skaffold.return_value = tmp_path / "build_result.json"
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build-push", "--repo", "ghcr.io/owner/repo"])
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    call_kw = mock_skaffold.call_args[1]
    assert call_kw["default_repo"] == "ghcr.io/owner/repo"


def test_build_push_exits_when_skaffold_yaml_missing(tmp_path: Path) -> None:
    """op build-push exits with error when skaffold.yaml is not found."""
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build-push"])
    finally:
        os.chdir(old)
    assert r.exit_code == 1
    assert "Skaffold file not found" in r.output or "not found" in r.output


@patch("octopilot_pipeline_tools.cli.resolve_build_tag", return_value=(None, False))
@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_build_invokes_skaffold(mock_run: MagicMock, _mock_resolve_tag: MagicMock) -> None:
    """op build (no subcommand) runs full Skaffold build with cache disabled and default-repo set."""
    mock_run.return_value = MagicMock(returncode=0)
    r = runner.invoke(main, ["build"])
    assert r.exit_code == 0
    mock_run.assert_called_once()
    call_args = mock_run.call_args[0][0]
    assert "skaffold" in call_args[0]
    assert "build" in call_args
    assert "--cache-artifacts=false" in call_args
    assert "--default-repo" in call_args
    # Without .github/octopilot.yaml, default repo is localhost:5001
    idx = call_args.index("--default-repo")
    assert call_args[idx + 1] == "localhost:5001"
    assert "--tag" not in call_args


@patch("octopilot_pipeline_tools.cli.resolve_build_tag", return_value=(None, False))
@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_build_uses_default_repo_from_run_yaml(
    mock_run: MagicMock, _mock_resolve_tag: MagicMock, tmp_path: Path
) -> None:
    """op build uses default_repo from .github/octopilot.yaml when present."""
    mock_run.return_value = MagicMock(returncode=0)
    _write_octopilot(tmp_path, "default_repo: myreg.local:5000\n")
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build"], obj={"config": {}})
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    call_args = mock_run.call_args[0][0]
    idx = call_args.index("--default-repo")
    assert call_args[idx + 1] == "myreg.local:5000"


@patch("octopilot_pipeline_tools.cli.resolve_build_tag", return_value=("1.2.3", False))
@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_build_with_version_tag_passes_tag_to_skaffold(mock_run: MagicMock, _mock_resolve_tag: MagicMock) -> None:
    """When on a version tag, op build passes --tag to Skaffold."""
    mock_run.return_value = MagicMock(returncode=0)
    r = runner.invoke(main, ["build"])
    assert r.exit_code == 0
    call_args = mock_run.call_args[0][0]
    assert "--tag" in call_args
    idx = call_args.index("--tag")
    assert call_args[idx + 1] == "1.2.3"


@patch("octopilot_pipeline_tools.cli.resolve_build_tag", return_value=("1.2.3", True))
@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_build_push_with_version_tag_passes_tag_and_add_latest(
    mock_skaffold: MagicMock, _mock_resolve_tag: MagicMock, tmp_path: Path
) -> None:
    """When on a version tag, op build-push passes tag and add_latest to run_skaffold_build_push."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    mock_skaffold.return_value = tmp_path / "build_result.json"
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build-push"])
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    call_kw = mock_skaffold.call_args[1]
    assert call_kw["tag"] == "1.2.3"
    assert call_kw["add_latest"] is True


@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_build_status_uses_cache(mock_skaffold: MagicMock, tmp_path: Path) -> None:
    """build-status (alias) runs Skaffold with cache enabled (cache_artifacts=True)."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    mock_skaffold.return_value = tmp_path / "build_result.json"
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build-status"])
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    mock_skaffold.assert_called_once()
    call_kw = mock_skaffold.call_args[1]
    assert call_kw["cache_artifacts"] is True
    assert call_kw["push"] is True
    assert call_kw["default_repo"] == "localhost:5001"


@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_build_status_subcommand_same_as_alias(mock_skaffold: MagicMock, tmp_path: Path) -> None:
    """op build status (subcommand) has same behaviour as op build-status (alias)."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    mock_skaffold.return_value = tmp_path / "build_result.json"
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build", "status"])
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    mock_skaffold.assert_called_once()
    call_kw = mock_skaffold.call_args[1]
    assert call_kw["cache_artifacts"] is True
    assert call_kw["push"] is True
    assert call_kw["default_repo"] == "localhost:5001"
    assert "Wrote" in r.output


@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_build_status_uses_default_repo_from_run_yaml(mock_skaffold: MagicMock, tmp_path: Path) -> None:
    """op build status uses default_repo from .github/octopilot.yaml when --repo not passed."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    _write_octopilot(tmp_path, "default_repo: registry.local:5001\n")
    mock_skaffold.return_value = tmp_path / "build_result.json"
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build", "status"])
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    call_kw = mock_skaffold.call_args[1]
    assert call_kw["default_repo"] == "registry.local:5001"


@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_build_status_repo_option_overrides_run_yaml(mock_skaffold: MagicMock, tmp_path: Path) -> None:
    """op build status --repo overrides .github/octopilot.yaml default_repo."""
    (tmp_path / "skaffold.yaml").write_text("apiVersion: skaffold/v2beta29\nkind: Config\nbuild:\n  artifacts: []\n")
    _write_octopilot(tmp_path, "default_repo: registry.local:5001\n")
    mock_skaffold.return_value = tmp_path / "build_result.json"
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build", "status", "--repo", "ghcr.io/org/repo"])
    finally:
        os.chdir(old)
    assert r.exit_code == 0
    call_kw = mock_skaffold.call_args[1]
    assert call_kw["default_repo"] == "ghcr.io/org/repo"


def test_build_status_exits_when_skaffold_yaml_missing(tmp_path: Path) -> None:
    """op build status exits with error when skaffold.yaml is not found."""
    # No skaffold.yaml in tmp_path
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build", "status"])
    finally:
        os.chdir(old)
    assert r.exit_code == 1
    assert "Skaffold file not found" in r.output or "not found" in r.output


def test_build_status_alias_exits_when_skaffold_yaml_missing(tmp_path: Path) -> None:
    """op build-status (alias) exits with error when skaffold.yaml is not found."""
    old = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(main, ["build-status"])
    finally:
        os.chdir(old)
    assert r.exit_code == 1
    assert "Skaffold file not found" in r.output or "not found" in r.output


@patch("octopilot_pipeline_tools.cli.subprocess.run")
@patch("octopilot_pipeline_tools.cli.run_skaffold_build_push")
def test_push_success(mock_build_push: MagicMock, mock_subprocess: MagicMock) -> None:
    mock_build_push.return_value = Path("build_result.json")
    mock_subprocess.return_value = MagicMock(returncode=0)
    r = runner.invoke(main, ["push", "--default-repo", "reg.io/repo"])
    assert r.exit_code == 0
    mock_build_push.assert_called_once()
    assert "Wrote" in r.output


@patch("octopilot_pipeline_tools.cli.get_watch_destination_repository")
@patch("octopilot_pipeline_tools.cli.read_build_result")
@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_watch_deployment_success(
    mock_run: MagicMock,
    mock_read_build: MagicMock,
    mock_get_dest: MagicMock,
) -> None:
    mock_get_dest.return_value = "reg.io/repo"
    mock_read_build.return_value = {"builds": [{"tag": "app:tag123"}]}
    # First kubectl get deployment returns image with tag123, then rollout status succeeds
    mock_run.side_effect = [
        MagicMock(returncode=0),
        MagicMock(returncode=0, stdout="reg.io/repo/app:tag123"),
        MagicMock(returncode=0),
    ]
    r = runner.invoke(
        main,
        [
            "watch-deployment",
            "--component",
            "myapp",
            "--environment",
            "dev",
            "--namespace",
            "default",
        ],
    )
    assert r.exit_code == 0


@patch("octopilot_pipeline_tools.cli.get_promote_repositories")
@patch("octopilot_pipeline_tools.cli.read_build_result")
@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_promote_image_success(
    mock_run: MagicMock,
    mock_read_build: MagicMock,
    mock_get_promote: MagicMock,
) -> None:
    mock_get_promote.return_value = ("reg.io/dev", "reg.io/prod")
    mock_read_build.return_value = {"builds": [{"tag": "app:tag123"}]}
    mock_run.return_value = MagicMock(returncode=0)
    r = runner.invoke(
        main,
        ["promote-image", "--source", "dev", "--destination", "prod"],
    )
    assert r.exit_code == 0
    mock_run.assert_called_once()
    call_args = mock_run.call_args[0][0]
    assert "crane" in call_args
    assert "copy" in call_args


def test_run_help() -> None:
    r = runner.invoke(main, ["run"])
    assert r.exit_code == 0
    assert "context list" in r.output or "run" in r.output.lower()


def test_run_context_list(tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: app-api
      context: api
    - image: app-frontend
      context: frontend
""")
    r = runner.invoke(
        main,
        ["run", "context", "list", "--skaffold-file", str(skaffold)],
        obj={"config": {}},
    )
    assert r.exit_code == 0
    assert "api" in r.output and "frontend" in r.output


@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_run_context_invokes_docker(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(returncode=0)
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: myapp-api
      context: api
""")
    r = runner.invoke(
        main,
        ["run", "api", "--skaffold-file", str(skaffold)],
        obj={"config": {}},
    )
    assert r.exit_code == 0
    mock_run.assert_called_once()
    call_args = mock_run.call_args[0][0]
    assert call_args[0] == "docker"
    assert "run" in call_args
    assert "localhost:5001/myapp-api:latest" in call_args
    assert "-p" in call_args
    assert "-e" in call_args


@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_run_uses_build_result_when_present(mock_run: MagicMock, tmp_path: Path) -> None:
    mock_run.return_value = MagicMock(returncode=0)
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: myapp-api
      context: api
""")
    (tmp_path / "build_result.json").write_text('{"builds": [{"tag": "localhost:5001/myapp-api:latest"}]}')
    _write_octopilot(tmp_path, 'contexts:\n  api:\n    ports: ["8080:8080"]\n')
    old_cwd = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(
            main,
            ["run", "api", "--skaffold-file", "skaffold.yaml"],
            obj={"config": {}},
        )
    finally:
        os.chdir(old_cwd)
    assert r.exit_code == 0
    call_args = mock_run.call_args[0][0]
    assert "localhost:5001/myapp-api:latest" in call_args
    # Octopilot ports override: must see -p 8080:8080
    idx = call_args.index("-p")
    assert idx + 1 < len(call_args)
    assert call_args[idx + 1] == "8080:8080"


@patch("octopilot_pipeline_tools.cli.subprocess.run")
def test_run_explicit_octopilot_ports_used_as_is(mock_run: MagicMock, tmp_path: Path) -> None:
    """When octopilot sets ports for context, op run uses them (no free-port scan)."""
    mock_run.return_value = MagicMock(returncode=0)
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: myapp-api
      context: api
""")
    (tmp_path / "api").mkdir()
    _write_octopilot(tmp_path, 'contexts:\n  api:\n    ports: ["3001:8080"]\n')
    old_cwd = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(
            main,
            ["run", "api", "--skaffold-file", "skaffold.yaml"],
            obj={"config": {}},
        )
    finally:
        os.chdir(old_cwd)
    assert r.exit_code == 0
    call_args = mock_run.call_args[0][0]
    idx = call_args.index("-p")
    assert call_args[idx + 1] == "3001:8080"


def test_run_context_not_in_build_result_exits_with_message(tmp_path: Path) -> None:
    skaffold = tmp_path / "skaffold.yaml"
    skaffold.write_text("""
apiVersion: skaffold/v2beta29
kind: Config
build:
  artifacts:
    - image: sample-react-node-api
      context: api
    - image: sample-react-node-frontend
      context: frontend
""")
    (tmp_path / "build_result.json").write_text('{"builds": [{"tag": "localhost:5001/sample-react-node-api:latest"}]}')
    _write_octopilot(
        tmp_path,
        'contexts:\n  api:\n    ports: ["8081:8080"]\n  frontend:\n    ports: ["8080:8080"]\n',
    )
    old_cwd = os.getcwd()
    try:
        os.chdir(tmp_path)
        r = runner.invoke(
            main,
            ["run", "frontend", "--skaffold-file", "skaffold.yaml"],
            obj={"config": {}},
        )
    finally:
        os.chdir(old_cwd)
    assert r.exit_code == 1
    assert "frontend" in r.output or "sample-react-node-frontend" in r.output
    assert "build_result.json" in r.output
    assert "op build-push" in r.output or "skaffold build" in r.output


def test_start_registry_help() -> None:
    r = runner.invoke(main, ["start-registry", "--help"])
    assert r.exit_code == 0
    assert "start-registry" in r.output
    assert "trust-cert" in r.output
    assert "5001" in r.output


@patch("octopilot_pipeline_tools.cli.etc_hosts_has_registry_local", return_value=True)
@patch("octopilot_pipeline_tools.cli.start_registry")
def test_start_registry_invokes_module(
    mock_start_registry: MagicMock,
    mock_etc_hosts: MagicMock,
    tmp_path: Path,
) -> None:
    mock_start_registry.return_value = tmp_path / "tls.crt"
    r = runner.invoke(
        main,
        [
            "start-registry",
            "--certs-dir",
            str(tmp_path),
            "--image",
            "myreg:latest",
            "--no-trust-cert-colima",
        ],
        obj={"config": {}},
    )
    assert r.exit_code == 0
    mock_start_registry.assert_called_once()
    call_kw = mock_start_registry.call_args[1]
    assert call_kw["image"] == "myreg:latest"
    assert call_kw["certs_out_dir"] == tmp_path
    assert call_kw["trust_cert"] is False


@patch("octopilot_pipeline_tools.cli.etc_hosts_has_registry_local", return_value=True)
@patch("octopilot_pipeline_tools.cli.install_cert_trust")
@patch("octopilot_pipeline_tools.cli.is_colima_running")
@patch("octopilot_pipeline_tools.cli.start_registry")
def test_start_registry_docker_desktop_fallback(
    mock_start_registry: MagicMock,
    mock_is_colima: MagicMock,
    mock_install_trust: MagicMock,
    mock_etc_hosts: MagicMock,
    tmp_path: Path,
) -> None:
    """When trust_cert_colima is default (True) but Colima is not running, use system trust (Docker Desktop)."""
    mock_start_registry.return_value = tmp_path / "tls.crt"
    (tmp_path / "tls.crt").write_text("PEM")
    mock_is_colima.return_value = False
    r = runner.invoke(
        main,
        ["start-registry", "--certs-dir", str(tmp_path), "--image", "myreg:latest"],
        obj={"config": {}},
    )
    assert r.exit_code == 0
    mock_is_colima.assert_called_once()
    mock_install_trust.assert_called_once()
    assert "Docker Desktop" in r.output or "system trust" in r.output


@patch("octopilot_pipeline_tools.cli.etc_hosts_has_registry_local", return_value=True)
@patch("octopilot_pipeline_tools.cli.install_cert_trust")
@patch("octopilot_pipeline_tools.cli.is_cert_already_trusted_system", return_value=True)
@patch("octopilot_pipeline_tools.cli.is_colima_running")
@patch("octopilot_pipeline_tools.cli.start_registry")
def test_start_registry_idempotent_already_trusted(
    mock_start_registry: MagicMock,
    mock_is_colima: MagicMock,
    mock_already_trusted: MagicMock,
    mock_install_trust: MagicMock,
    mock_etc_hosts: MagicMock,
    tmp_path: Path,
) -> None:
    """When cert is already trusted (Docker Desktop path), skip install and do not prompt."""
    mock_start_registry.return_value = tmp_path / "tls.crt"
    (tmp_path / "tls.crt").write_text("PEM")
    mock_is_colima.return_value = False
    r = runner.invoke(
        main,
        ["start-registry", "--certs-dir", str(tmp_path), "--image", "myreg:latest"],
        obj={"config": {}},
    )
    assert r.exit_code == 0
    mock_already_trusted.assert_called_once()
    mock_install_trust.assert_not_called()
    assert "already trusted" in r.output or "idempotent" in r.output


@patch("octopilot_pipeline_tools.cli.etc_hosts_has_registry_local", return_value=False)
def test_start_registry_exits_early_without_registry_local_in_hosts(
    mock_etc_hosts: MagicMock,
) -> None:
    """If /etc/hosts does not contain registry.local, exit with error before starting anything."""
    r = runner.invoke(main, ["start-registry", "--no-trust-cert-colima"], obj={"config": {}})
    assert r.exit_code == 1
    assert "registry.local" in r.output
    assert "/etc/hosts" in r.output
    mock_etc_hosts.assert_called_once()

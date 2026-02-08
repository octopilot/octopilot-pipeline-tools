# OctoPilot pipeline-tools – just recipes
# Run: just (list) | just build | just install | just lint | just test | just check

# Default: list recipes
default:
    @just --list

# Build: install package and dev deps in editable mode (same as install)
build:
    pip install -e ".[dev]"

# Install package and dev deps (editable)
install:
    pip install -e ".[dev]"

# Lint only (no fix)
lint:
    ruff check src tests && ruff format src tests --check

# Fix lint and format
fix:
    ruff check src tests --fix
    ruff format src tests

# Run tests with coverage (fail under 80%)
test:
    pytest tests/ -v --tb=short --cov=src/octopilot_pipeline_tools --cov-fail-under=80 --cov-report=term-missing

# Run pre-commit on all files (same as CI gate)
pre-commit:
    pre-commit run --all-files

# Install pre-commit git hooks (run once after clone)
install-hooks:
    pre-commit install

# Full check: lint + test (CI equivalent)
check: lint test

# Run op CLI (e.g. just op build-push or just op build-push --repo localhost:5001)
op *ARGS:
    op {{ARGS}}

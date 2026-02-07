# Contributing

## Just (optional)

A [justfile](https://just.systems/) provides short commands: `just` (list), `just install`, `just lint`, `just fix`, `just test`, `just check`, `just pre-commit`, `just install-hooks`, `just op <args>`.

## Pre-commit (recommended)

Pre-commit blocks commits that would fail CI (ruff lint/format and pytest). Install once:

```bash
pip install -e ".[dev]"
pre-commit install
```

Then every `git commit` runs ruff (check + format) and pytest. To run manually:

```bash
pre-commit run --all-files
```

## Lint and tests (manual)

**Ruff (strict linting, line-length 120):**
```bash
pip install -e ".[dev]"
ruff check src tests && ruff format src tests --check
ruff check src tests --fix && ruff format src tests   # fix and format
```

**Tests and coverage (minimum 80%):**
```bash
pip install -e ".[dev]"
pytest tests/ -v --cov=src/octopilot_pipeline_tools --cov-report=term-missing --cov-branch
```

CI runs these via [.github/workflows/ci.yml](.github/workflows/ci.yml).

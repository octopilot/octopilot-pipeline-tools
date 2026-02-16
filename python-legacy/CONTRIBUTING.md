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

## Optional: strip Cursor Co-authored-by from commits

If you use Cursor IDE and don't want `Co-authored-by: Cursor <cursoragent@cursor.com>` added to your commit messages, use the prepare-commit-msg hook:

```bash
# From this repo (octopilot-pipeline-tools)
echo '#!/bin/sh' > .git/hooks/prepare-commit-msg
echo 'exec python3 '"$(pwd)"'/scripts/strip_cursor_coauthor.py "$@"' >> .git/hooks/prepare-commit-msg
chmod +x .git/hooks/prepare-commit-msg
```

To apply for all your repos, set a global hooks directory and put the script there (e.g. as `prepare-commit-msg` with `#!/usr/bin/env python3` and the script body, then `chmod +x`).

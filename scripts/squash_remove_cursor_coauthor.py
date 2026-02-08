#!/usr/bin/env python3
"""
THROWAWAY: Squash all commits in OctoPilot repos to one commit and remove
"Co-authored-by: Cursor <cursoragent@cursor.com>" from the commit message.

DESTRUCTIVE: Rewrites history. Run once from octopilot workspace root, then
force-push (--push or manually). Delete this script after cleanup.

Usage (from octopilot workspace root, or pass repo paths):
  python3 scripts/squash_remove_cursor_coauthor.py [--push] [REPO_DIR ...]

If no REPO_DIR given, finds octopilot-pipeline-tools and all sample-* dirs
under the current directory that are git repos.

With --push, runs git push --force after rewriting (use with care).
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

CURSOR_COAUTHOR_PATTERN = re.compile(
    r"^\s*Co-authored-by:\s*Cursor\s*<cursoragent@cursor\.com>\s*$",
    re.IGNORECASE | re.MULTILINE,
)


def strip_cursor_coauthor(msg: str) -> str:
    out = CURSOR_COAUTHOR_PATTERN.sub("", msg)
    out = re.sub(r"\n{3,}", "\n\n", out).rstrip()
    return out


def run(cwd: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        list(args),
        cwd=cwd,
        capture_output=True,
        text=True,
        check=check,
    )


def get_root_commit(repo: Path) -> str:
    r = run(repo, "git", "rev-list", "--max-parents=0", "HEAD")
    return r.stdout.strip().splitlines()[0]


def get_commit_count(repo: Path) -> int:
    r = run(repo, "git", "rev-list", "--count", "HEAD")
    return int(r.stdout.strip())


def get_head_message(repo: Path) -> str:
    r = run(repo, "git", "log", "-1", "--format=%B", "HEAD")
    return r.stdout


def _out(msg: str) -> None:
    sys.stdout.write(msg + "\n")


def squash_and_clean(repo: Path, push: bool) -> None:
    if not (repo / ".git").exists():
        _out(f"Skip (not a git repo): {repo}")
        return
    try:
        root = get_root_commit(repo)
    except (IndexError, subprocess.CalledProcessError):
        _out(f"Skip (no commits?): {repo}")
        return
    n = get_commit_count(repo)
    name = repo.name
    _out(f"  {name}: {n} commit(s), root={root[:8]}")

    if n == 1:
        msg = get_head_message(repo)
        clean_msg = strip_cursor_coauthor(msg)
        if clean_msg == msg:
            _out("    -> Single commit, no Cursor line to remove")
            return
        run(repo, "git", "commit", "--amend", "-m", clean_msg)
        _out("    -> Amended single commit (removed Cursor co-author)")
    else:
        msg = get_head_message(repo)
        clean_msg = strip_cursor_coauthor(msg)
        if not clean_msg.strip():
            clean_msg = "Initial commit"
        run(repo, "git", "reset", "--soft", root)
        run(repo, "git", "commit", "--amend", "-m", clean_msg)
        _out("    -> Squashed to one commit (no Cursor co-author)")

    if push:
        run(repo, "git", "push", "--force")
        _out("    -> Pushed (--force)")
    else:
        _out(f"    -> Run `git push --force` in {repo} to update remote")


def main() -> int:
    ap = argparse.ArgumentParser(description="Squash to one commit and remove Cursor co-author.")
    ap.add_argument("--push", action="store_true", help="Run git push --force after rewrite")
    ap.add_argument(
        "repos", nargs="*", help="Repo directories (default: auto-detect octopilot-pipeline-tools and sample-*)"
    )
    args = ap.parse_args()

    if args.repos:
        repos = [Path(p).resolve() for p in args.repos]
    else:
        cwd = Path.cwd().resolve()
        search_dirs = [cwd] if (cwd / ".git").exists() else []
        search_dirs += [p for p in cwd.iterdir() if p.is_dir()]
        repos = []
        for d in search_dirs:
            if not d.is_dir() or not (d / ".git").exists():
                continue
            if d.name == "octopilot-pipeline-tools" or d.name.startswith("sample-"):
                repos.append(d)
        repos = sorted(set(repos))
        if not repos:
            sys.stderr.write("No repos found. Run from octopilot workspace root or pass repo paths.\n")
            return 1

    _out(f"Repos: {len(repos)}")
    for repo in repos:
        squash_and_clean(repo, args.push)
    return 0


if __name__ == "__main__":
    sys.exit(main())

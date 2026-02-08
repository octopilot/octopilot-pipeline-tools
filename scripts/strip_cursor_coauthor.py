#!/usr/bin/env python3
"""
Strip "Co-authored-by: Cursor <cursoragent@cursor.com>" from a git commit message file.

Use as a prepare-commit-msg hook so Cursor IDE does not add that trailer:

  echo '#!/usr/bin/env python3' > .git/hooks/prepare-commit-msg
  echo 'exec python3 /path/to/octopilot-pipeline-tools/scripts/strip_cursor_coauthor.py "$@"' \\
    >> .git/hooks/prepare-commit-msg
  chmod +x .git/hooks/prepare-commit-msg

Or use globally: git config --global core.hooksPath /path/to/your/hooks
and put this script there as prepare-commit-msg (shebang + chmod +x).
"""

from __future__ import annotations

import re
import sys


def main() -> None:
    if len(sys.argv) < 2:
        sys.exit(0)
    path = sys.argv[1]
    try:
        with open(path) as f:
            text = f.read()
    except OSError:
        sys.exit(0)
    # Remove Cursor co-authored-by (exact or with trailing/leading whitespace)
    pattern = re.compile(
        r"^\s*Co-authored-by:\s*Cursor\s*<cursoragent@cursor\.com>\s*$",
        re.IGNORECASE | re.MULTILINE,
    )
    new_text = pattern.sub("", text)
    new_text = re.sub(r"\n{3,}", "\n\n", new_text).rstrip()
    if new_text != text:
        with open(path, "w") as f:
            f.write(new_text + "\n" if new_text else "\n")


if __name__ == "__main__":
    main()

#!/usr/bin/env bash
# Build .deb and .rpm for octopilot-pipeline-tools using fpm.
# Requires: fpm (gem install fpm), python3, pip.
# Usage: ./build-deb-rpm.sh [version]
#   version defaults to value from pyproject.toml.

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  VERSION=$(grep -E '^version\s*=' pyproject.toml | sed -E "s/.*=[[:space:]]*[\"']([^\"']+)[\"'].*/\1/")
fi

echo "Building octopilot-pipeline-tools ${VERSION} (deb + rpm)"

# Build a wheel so we have a single artifact; then use fpm with dir type to install it.
BUILD_DIR=$(mktemp -d)
trap "rm -rf '$BUILD_DIR'" EXIT

python3 -m pip install --target "$BUILD_DIR/usr/lib/python3/dist-packages" --no-compile --no-deps .
# Ensure scripts on PATH
mkdir -p "$BUILD_DIR/usr/bin"
# Pip puts scripts in site-packages/bin or we need to create wrappers. For system packages,
# we install into dist-packages and the console_scripts are in .../bin. So we need
# to copy the script entries. Easiest: use a venv in BUILD_DIR, install there, then
# copy usr/lib/python3/dist-packages and usr/bin from venv/bin.
rm -rf "$BUILD_DIR/usr"
python3 -m venv "$BUILD_DIR/venv"
"$BUILD_DIR/venv/bin/pip" install .

# Layout for deb/rpm: /usr/lib/python3/dist-packages + /usr/bin
PREFIX="$BUILD_DIR/usr"
mkdir -p "$PREFIX/bin"
PYVER=$(python3 -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
SITELIB="$PREFIX/lib/python${PYVER}/dist-packages"
mkdir -p "$SITELIB"
cp -a "$BUILD_DIR/venv/lib/python${PYVER}/site-packages"/* "$SITELIB"

for name in op octopipeline; do
  cat > "$PREFIX/bin/$name" << 'WRAP'
#!/usr/bin/env python3
import sys
from octopilot_pipeline_tools.cli import main
if __name__ == "__main__":
    sys.exit(main())
WRAP
  chmod +x "$PREFIX/bin/$name"
done

# Build .deb and .rpm with fpm
mkdir -p "$REPO_ROOT/dist"
fpm -s dir -t deb -n octopilot-pipeline-tools -v "$VERSION" \
  --description "CLI for Skaffold/Buildpacks pipelines: build, push, build_result.json (op)" \
  --url "https://github.com/octopilot/octopilot-pipeline-tools" \
  --license "Apache-2.0" \
  -C "$BUILD_DIR" \
  usr

fpm -s dir -t rpm -n octopilot-pipeline-tools -v "$VERSION" \
  --description "CLI for Skaffold/Buildpacks pipelines: build, push, build_result.json (op)" \
  --url "https://github.com/octopilot/octopilot-pipeline-tools" \
  --license "Apache-2.0" \
  -C "$BUILD_DIR" \
  usr

mkdir -p "$REPO_ROOT/dist"
mv "$REPO_ROOT"/octopilot-pipeline-tools_*.deb "$REPO_ROOT/dist/" 2>/dev/null || true
mv "$REPO_ROOT"/octopilot-pipeline-tools-*.rpm "$REPO_ROOT/dist/" 2>/dev/null || true

echo "Built: $REPO_ROOT/dist/"
ls -la "$REPO_ROOT/dist/"

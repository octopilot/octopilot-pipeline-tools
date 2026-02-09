# Packaging OctoPilot Pipeline Tools for delivery

**Build packaging in CI (no release):** The workflow [Build packaging](.github/workflows/build-packaging.yml) runs on push to `main` when `packaging/`, `pyproject.toml`, or `src/` change, and can be run manually from the Actions tab. It builds deb, rpm, Chocolatey nupkg, and a versioned Homebrew formula template, and uploads them as workflow artifacts.

This directory contains assets and scripts to package **octopilot-pipeline-tools** (the `op` / `octopipeline` CLI) for:

| Format       | Directory        | Use case                    |
|-------------|------------------|-----------------------------|
| **Homebrew** | `homebrew/`      | macOS / Linux (brew)        |
| **Chocolatey** | `chocolatey/` | Windows                     |
| **deb**     | `deb-rpm/`      | Debian / Ubuntu             |
| **rpm**     | `deb-rpm/`      | RHEL / Fedora / openSUSE   |

Before packaging, ensure the project is published to **PyPI** (or that the Homebrew formula points at a GitHub release tarball). Version in each asset should match the release (e.g. `v0.1.0`).

---

## 1. Homebrew

**Files:** `homebrew/octopilot-pipeline-tools.rb`

- Copy the Formula into a Homebrew tap repo (e.g. `homebrew-octopilot` or your org’s tap).
- Put it at `Formula/o/octopilot-pipeline-tools.rb`.
- For each release:
  1. Update `url` and `version` in the Formula (e.g. `v0.1.0`).
  2. Set `sha256`:
     `curl -sL "https://github.com/octopilot/octopilot-pipeline-tools/archive/refs/tags/v0.1.0.tar.gz" | shasum -a 256`
  3. Optionally run `brew update-python-resources octopilot-pipeline-tools` to refresh dependency resources.

**Install (after tap is set up):**

```bash
brew tap octopilot/octopilot   # or your tap URL
brew install octopilot-pipeline-tools
op --help
```

---

## 2. Chocolatey (Windows)

**Files:** `chocolatey/octopilot-pipeline-tools.nuspec`, `chocolatey/tools/chocolateyinstall.ps1`, `chocolatey/tools/chocolateyuninstall.ps1`

- The package depends on the **python** Chocolatey package (Python 3.10+).
- Install runs `pip install octopilot-pipeline-tools==<version>` (or latest if that fails).

**Build the nupkg (from repo root):**

```powershell
cd packaging\chocolatey
choco pack octopilot-pipeline-tools.nuspec
```

Update the `<version>` in the `.nuspec` to match the release. Publish to the Chocolatey community feed or your internal feed.

**Install:**

```powershell
choco install octopilot-pipeline-tools -y
op --help
```

---

## 3. deb (Debian / Ubuntu)

**Script:** `deb-rpm/build-deb-rpm.sh`

**Requirements:** `fpm` (e.g. `gem install fpm`), `python3`, `pip`.

**Build (from repo root):**

```bash
./packaging/deb-rpm/build-deb-rpm.sh
# Or with explicit version:
./packaging/deb-rpm/build-deb-rpm.sh 0.1.0
```

Output: `dist/octopilot-pipeline-tools_<version>_<arch>.deb`

**Install:**

```bash
sudo dpkg -i dist/octopilot-pipeline-tools_*.deb
# or
sudo apt install ./dist/octopilot-pipeline-tools_*.deb
op --help
```

---

## 4. rpm (RHEL / Fedora / openSUSE)

**Script:** Same `packaging/deb-rpm/build-deb-rpm.sh` (builds both deb and rpm).

**Requirements:** Same as deb (fpm, python3, pip).

**Build:** Same as deb (script produces both packages).

Output: `dist/octopilot-pipeline-tools-<version>-1.<arch>.rpm`

**Install:**

```bash
sudo rpm -ivh dist/octopilot-pipeline-tools-*.rpm
# or
sudo dnf install dist/octopilot-pipeline-tools-*.rpm
op --help
```

---

## Version and release checklist

1. Bump version in `pyproject.toml`, commit, then create and push a **tag** `v<version>` (e.g. `v0.1.0`).
2. The **Release** workflow (`.github/workflows/release.yml`) runs on tag push and will:
   - Build **deb** and **rpm** (Ubuntu, fpm).
   - Build **Chocolatey** `.nupkg` (Windows, choco pack) with the tag version.
   - Build and push the **Docker** image to ghcr.io (multi-arch) with that tag and `latest`.
   - Create a **GitHub Release** and attach: `.deb`, `.rpm`, `.nupkg`, and a ready-to-use **Homebrew formula** (with version and sha256 filled in).
3. **PyPI** (optional): To make `pip install octopilot-pipeline-tools==<version>` work, run `python -m build && twine upload dist/*` after the tag exists (or add a PyPI publish step to the workflow).
4. Copy the Formula from the release assets into your Homebrew tap; publish the Chocolatey package to your feed if not using the nupkg directly.

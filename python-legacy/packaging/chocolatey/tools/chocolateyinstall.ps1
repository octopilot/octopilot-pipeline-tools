# Install OctoPilot Pipeline Tools (op) via pip. Requires Python 3.10+ (installed as Chocolatey dependency).
$ErrorActionPreference = 'Stop'
$packageName = $env:chocoPackageName
$version = $env:chocoPackageVersion

$pipArgs = @('install', "--upgrade", "${packageName}==${version}")
if ($env:chocoInstallArguments) {
  $pipArgs += $env:chocoInstallArguments -split '\s+'
}

# Prefer py -3 or python3 so we use the Python installed by Chocolatey.
$py = $null
foreach ($exe in @('py', 'python3', 'python')) {
  try {
    $null = & $exe -3 -c "import sys; sys.exit(0)" 2>$null
    if ($LASTEXITCODE -eq 0) { $py = $exe; break }
  } catch {}
  try {
    $null = & $exe -c "import sys; sys.exit(0)" 2>$null
    if ($LASTEXITCODE -eq 0) { $py = $exe; break }
  } catch {}
}

if (-not $py) {
  throw "Python not found. Install the chocolatey python package first."
}

& $py -m pip @pipArgs
if ($LASTEXITCODE -ne 0) {
  # Fallback: install from PyPI by name (version may differ)
  & $py -m pip install --upgrade octopilot-pipeline-tools
  if ($LASTEXITCODE -ne 0) { throw "pip install failed." }
}

Write-Host "Installed. Run 'op --help' or 'octopipeline --help'."

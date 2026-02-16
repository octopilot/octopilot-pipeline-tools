$ErrorActionPreference = 'Stop'
$py = $null
foreach ($exe in @('py', 'python3', 'python')) {
  try {
    $null = & $exe -m pip uninstall -y octopilot-pipeline-tools 2>$null
    if ($LASTEXITCODE -eq 0) { exit 0 }
  } catch {}
}
& python -m pip uninstall -y octopilot-pipeline-tools

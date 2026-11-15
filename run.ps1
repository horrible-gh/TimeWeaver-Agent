$ErrorActionPreference = "Stop"
Push-Location $PSScriptRoot
try {
    $Agent = Join-Path $PSScriptRoot "dist/timeweaver-agent.exe"
    if (-not (Test-Path $Agent)) { throw "Agent is not built. Run: .\setup.ps1" }
    & $Agent run @args
    exit $LASTEXITCODE
}
finally { Pop-Location }

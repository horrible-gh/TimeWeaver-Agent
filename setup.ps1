$ErrorActionPreference = "Stop"
Push-Location $PSScriptRoot
try {
    & (Join-Path $PSScriptRoot "scripts/build.ps1") -NoPull
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    & (Join-Path $PSScriptRoot "dist/timeweaver-agent.exe") setup @args
    exit $LASTEXITCODE
}
finally { Pop-Location }

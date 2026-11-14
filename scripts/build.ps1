param([switch]$NoPull)
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Push-Location $Root
$Temp = $null
$PreviousCGO = $env:CGO_ENABLED
try {
    if (-not $NoPull) {
        & git pull --ff-only
        if ($LASTEXITCODE -ne 0) { throw 'git pull failed; no build was performed.' }
    }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go is required to build.' }
    New-Item -ItemType Directory -Force -Path 'dist' | Out-Null
    $Version = '0.1.0-dev'
    $Sha = & git rev-parse --short HEAD 2>$null
    if ($LASTEXITCODE -eq 0) { $Version += "+$Sha" }
    $Temp = Join-Path $Root ("dist\.timeweaver-agent-" + [guid]::NewGuid().ToString('N') + '.exe')
    $env:CGO_ENABLED = '0'
    & go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $Temp ./cmd/timeweaver-agent
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed; existing executable was not replaced.' }
    # Stop the SpawnPoint process first: Windows locks a running executable.
    Move-Item -Force -Path $Temp -Destination 'dist\timeweaver-agent.exe'
    Write-Host "Built dist\timeweaver-agent.exe ($Version)"
} finally {
    if ($null -eq $PreviousCGO) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $PreviousCGO }
    if ($Temp -and (Test-Path $Temp)) { Remove-Item -Force $Temp }
    Pop-Location
}

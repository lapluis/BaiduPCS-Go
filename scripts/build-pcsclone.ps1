param(
    [string]$Output = 'build/pcsclone.exe',
    [string]$Version = 'v4.0.2-dev'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
$taskPreviousToolchain = $env:GOTOOLCHAIN
$taskPreviousCGO = $env:CGO_ENABLED

Push-Location -LiteralPath $taskRoot
try {
    $taskGoVersion = (Get-Content -LiteralPath (Join-Path $taskRoot '.go-version') -Raw).Trim()
    if ($taskGoVersion -notmatch '^1\.\d+\.\d+$') {
        throw 'Invalid release Go version in .go-version'
    }
    $env:GOTOOLCHAIN = "go$taskGoVersion"
    $env:CGO_ENABLED = '0'
    # Keep Go symbols for linked-code vulnerability scanning; omit DWARF only.
    & go build -trimpath -ldflags "-w -X main.Version=$Version" -o $Output ./cmd/pcsclone
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }
}
finally {
    $env:GOTOOLCHAIN = $taskPreviousToolchain
    $env:CGO_ENABLED = $taskPreviousCGO
    Pop-Location
}

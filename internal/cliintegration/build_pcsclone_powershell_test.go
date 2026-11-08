package cliintegration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPowerShellBuildPinsToolchainAndRestoresEnvironment(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell 7 unavailable")
	}
	for _, mode := range []string{"success", "compiler-failure", "invalid-version"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "repository with spaces")
			if err := os.MkdirAll(filepath.Join(root, "scripts"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{".go-version", "scripts/build-pcsclone.ps1"} {
				data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
				if err != nil {
					t.Fatal(err)
				}
				if name == ".go-version" && mode == "invalid-version" {
					data = []byte("not-a-toolchain\n")
				}
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			const driver = `
$ErrorActionPreference = 'Stop'
$taskRoot = $env:PCS_BUILD_FIXTURE
$taskCaller = Get-Location
$env:GOTOOLCHAIN = 'go1.26.6'
$env:CGO_ENABLED = '1'
$global:taskCompilerCalls = 0
function global:go {
    $global:taskCompilerCalls++
    $expected = 'go' + (Get-Content -LiteralPath (Join-Path $env:PCS_BUILD_FIXTURE '.go-version') -Raw).Trim()
    if ($env:GOTOOLCHAIN -ne $expected -or $env:CGO_ENABLED -ne '0') { throw 'Wrong build environment' }
    if ((Get-Location).Path -ne $env:PCS_BUILD_FIXTURE) { throw 'Wrong build directory' }
    if ($args.Count -ne 7 -or $args[0] -ne 'build' -or $args[1] -ne '-trimpath' -or $args[2] -ne '-ldflags' -or $args[3] -ne '-w -X main.Version=vtest+fixture' -or $args[4] -ne '-o' -or $args[5] -ne 'result.exe' -or $args[6] -ne './cmd/pcsclone') { throw 'Wrong compiler arguments' }
    if ($env:PCS_BUILD_MODE -eq 'compiler-failure') { $global:LASTEXITCODE = 42; return }
    Set-Content -LiteralPath 'result.exe' -Value 'new artifact' -NoNewline
    $global:LASTEXITCODE = 0
}
Set-Content -LiteralPath (Join-Path $taskRoot 'result.exe') -Value 'previous artifact' -NoNewline
$failure = $null
try { & (Join-Path $taskRoot 'scripts/build-pcsclone.ps1') -Output 'result.exe' -Version 'vtest+fixture' } catch { $failure = $_ }
if ($env:GOTOOLCHAIN -ne 'go1.26.6' -or $env:CGO_ENABLED -ne '1' -or (Get-Location).Path -ne $taskCaller.Path) { throw 'Build leaked process environment or working directory' }
$artifact = Get-Content -LiteralPath (Join-Path $taskRoot 'result.exe') -Raw
switch ($env:PCS_BUILD_MODE) {
    'success' { if ($failure -or $global:taskCompilerCalls -ne 1 -or $artifact -ne 'new artifact') { throw "Successful build failed: $failure" } }
    'compiler-failure' { if (-not $failure -or $failure.ToString() -notmatch 'exit code 42' -or $global:taskCompilerCalls -ne 1 -or $artifact -ne 'previous artifact') { throw 'Compiler failure lost or previous artifact changed' } }
    'invalid-version' { if (-not $failure -or $failure.ToString() -notmatch 'Invalid release Go version' -or $global:taskCompilerCalls -ne 0 -or $artifact -ne 'previous artifact') { throw 'Invalid toolchain reached compiler' } }
    default { throw 'Unknown fixture mode' }
}
`
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, pwsh, "-NoProfile", "-NonInteractive", "-Command", driver)
			cmd.Env = append(os.Environ(), "PCS_BUILD_FIXTURE="+root, "PCS_BUILD_MODE="+mode)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build script regression failed: %v\n%s", err, out)
			}
		})
	}
}

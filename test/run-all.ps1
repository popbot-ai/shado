# Run shado's full test suite.
#
#   Go unit tests     fast, no privileges (registry, flags, sizes, parsing)
#   Integration tests require an ELEVATED shell + Hyper-V (real VHDX create/mount):
#     e2e.ps1       base/clone/park/resume/reset/restore lifecycle + isolation
#     recache.ps1   promote warmed main -> new base, re-base every shadow
#     p4-init.ps1   per-slot Perforce workspace via the p4-init hook (live P4 server)
#
# Usage (elevated PowerShell):
#   powershell -ExecutionPolicy Bypass -File test\run-all.ps1 [-SkipIntegration] [-SkipP4]
param([switch]$SkipIntegration, [switch]$SkipP4)

$ErrorActionPreference = 'Continue'
$root = Split-Path $PSScriptRoot -Parent
Push-Location $root
$fail = $false

Write-Host "=== go unit tests ===" -ForegroundColor Cyan
& go test ./...
if ($LASTEXITCODE -ne 0) { $fail = $true }

if (-not $SkipIntegration) {
  $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole('Administrators')
  if (-not $isAdmin) {
    Write-Host "integration tests need an elevated shell - skipping (run as admin, or pass -SkipIntegration)" -ForegroundColor Yellow
  } else {
    & go build -o shado.exe .
    $tests = @('e2e', 'recache')
    if (-not $SkipP4) { $tests += 'p4-init' }
    foreach ($t in $tests) {
      Write-Host "=== integration: $t ===" -ForegroundColor Cyan
      $log = "$env:TEMP\shado-$t.log"
      & "$root\test\$t.ps1" -Log $log
      $res = (Get-Content $log -ErrorAction SilentlyContinue | Select-String 'RESULT=')
      Write-Host "  $res"
      if ("$res" -notmatch 'PASS') { $fail = $true }
    }
  }
}

Pop-Location
Write-Host ""
$color = if ($fail) { 'Red' } else { 'Green' }
Write-Host "SUITE=$(if ($fail) {'FAIL'} else {'PASS'})" -ForegroundColor $color
if ($fail) { exit 1 }

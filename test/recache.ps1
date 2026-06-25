# shado recache test (run elevated). Verifies that warming the main clone and
# running `recache` promotes that warm state into a NEW base and propagates it to
# every shadow (the core, riskiest operation: Convert-VHD flatten + re-base).
param([string]$Log = "$env:TEMP\shado-recache.log")

Start-Transcript -Path $Log -Force | Out-Null
$ErrorActionPreference = 'Continue'
$shado = "c:\Users\benjcooley\projects\popbot-ai\shado\shado.exe"
$env:SHADO_HOME = "C:\shado-rc-home"
$warm = "C:\shado-rc-warm"
$fail = $false
function Check($n, [bool]$c) { if ($c) { Write-Host "PASS  $n" } else { Write-Host "FAIL  $n"; $script:fail = $true } }

& $shado restore --name rc 2>$null | Out-Null
Remove-Item $env:SHADO_HOME, $warm -Recurse -Force -EA SilentlyContinue
New-Item -ItemType Directory "$warm\Source" -Force | Out-Null
"base v1" | Set-Content "$warm\README.md"
$b = New-Object byte[] (4MB); (New-Object Random).NextBytes($b); [IO.File]::WriteAllBytes("$warm\Source\a.bin", $b)

Write-Host "=== create (main + 2 slots) ==="
& $shado create $warm --name rc --count 2

$main = "$env:SHADO_HOME\shadows\rc\main"
$s1 = "$env:SHADO_HOME\shadows\rc\1"
$s2 = "$env:SHADO_HOME\shadows\rc\2"

# warm the main: a file that exists ONLY in main right now
"warmed in main" | Set-Content "$main\WARMED.txt"
Check "WARMED present in main"        (Test-Path "$main\WARMED.txt")
Check "WARMED NOT yet in slot 1"      (-not (Test-Path "$s1\WARMED.txt"))
Check "WARMED NOT yet in slot 2"      (-not (Test-Path "$s2\WARMED.txt"))

Write-Host "`n=== recache (promote warmed main -> new base, re-base all) ==="
& $shado recache --name rc

Check "after recache: main still has WARMED"                  (Test-Path "$main\WARMED.txt")
Check "after recache: slot 1 NOW has WARMED (in new base)"    (Test-Path "$s1\WARMED.txt")
Check "after recache: slot 2 NOW has WARMED (in new base)"    (Test-Path "$s2\WARMED.txt")
Check "after recache: original base content intact (a.bin)"   (Test-Path "$s1\Source\a.bin")
Check "after recache: README intact"                          (Test-Path "$s2\README.md")

Write-Host "`n=== du after recache ==="
& $shado du --name rc

Write-Host "`n=== restore (cleanup) ==="
& $shado restore --name rc | Out-Null
Remove-Item $env:SHADO_HOME, $warm -Recurse -Force -EA SilentlyContinue

Write-Host "`nRECACHE_RESULT=$(if ($fail) {'FAIL'} else {'PASS'})"
Stop-Transcript | Out-Null

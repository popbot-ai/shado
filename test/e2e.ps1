# shado end-to-end test (run elevated). Exercises the full lifecycle against a
# small synthetic warm folder, in an isolated SHADO_HOME, then cleans up.
param([string]$Log = "$env:TEMP\shado-e2e.log")

Start-Transcript -Path $Log -Force | Out-Null
$ErrorActionPreference = 'Continue'
$shado = "c:\Users\benjcooley\projects\popbot-ai\shado\shado.exe"
$env:SHADO_HOME = "C:\shado-test-home"
$warm = "C:\shado-test-warm"
$fail = $false
function Check($name, [bool]$cond) { if ($cond) { Write-Host "PASS  $name" } else { Write-Host "FAIL  $name"; $script:fail = $true } }

# clean slate
& $shado restore --name t 2>$null | Out-Null
Remove-Item $env:SHADO_HOME -Recurse -Force -EA SilentlyContinue
Remove-Item $warm -Recurse -Force -EA SilentlyContinue

# build a small warm folder (~15MB)
New-Item -ItemType Directory -Path "$warm\Source" -Force | Out-Null
"hello from base" | Set-Content "$warm\README.md"
1..3 | ForEach-Object { $b = New-Object byte[] (5MB); (New-Object Random).NextBytes($b); [IO.File]::WriteAllBytes("$warm\Source\a$_.bin", $b) }

Write-Host "`n=== create ==="
& $shado create $warm --name t --count 2 --size-gb 2
Write-Host "`n=== ls ==="
& $shado ls

$s1 = "$env:SHADO_HOME\shadows\t\1"
$s2 = "$env:SHADO_HOME\shadows\t\2"
Check "slot 1 mounted, sees base README"  (Test-Path "$s1\README.md")
Check "slot 2 mounted, sees base asset"   (Test-Path "$s2\Source\a1.bin")

# edit slot 1, prove isolation
"edited in slot 1" | Set-Content "$s1\EDITED.txt"
Check "EDITED present in slot 1"               (Test-Path "$s1\EDITED.txt")
Check "EDITED NOT visible in slot 2 (isolated)" (-not (Test-Path "$s2\EDITED.txt"))

Write-Host "`n=== ls after edit (slot 1 should show *dirty*) ==="
& $shado ls

Write-Host "`n=== clone reset --slot 1 ==="
& $shado clone reset --name t --slot 1
Check "reset cleared EDITED.txt"          (-not (Test-Path "$s1\EDITED.txt"))
Check "reset kept warm base (README)"     (Test-Path "$s1\README.md")

Write-Host "`n=== clone create --slot 5 ==="
& $shado clone create --name t --slot 5
Check "slot 5 created + warm"             (Test-Path "$env:SHADO_HOME\shadows\t\5\Source\a1.bin")

Write-Host "`n=== park / resume slot 5 ==="
& $shado clone park --name t --slot 5
Check "slot 5 unmounted on park"          (-not (Test-Path "$env:SHADO_HOME\shadows\t\5\README.md"))
& $shado clone resume --name t --slot 5
Check "slot 5 remounted on resume"        (Test-Path "$env:SHADO_HOME\shadows\t\5\README.md")

Write-Host "`n=== clone rm --slot 5 ==="
& $shado clone rm --name t --slot 5
Check "slot 5 removed"                    (-not (Test-Path "$env:SHADO_HOME\shadows\t\5"))

Write-Host "`n=== restore ==="
& $shado restore --name t
Write-Host "=== ls after restore ==="
& $shado ls
Check "warm original folder still intact" (Test-Path "$warm\README.md")

# cleanup
Remove-Item $env:SHADO_HOME -Recurse -Force -EA SilentlyContinue
Remove-Item $warm -Recurse -Force -EA SilentlyContinue

Write-Host "`nE2E_RESULT=$(if ($fail) {'FAIL'} else {'PASS'})"
Stop-Transcript | Out-Null

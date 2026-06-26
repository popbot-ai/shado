# shado + Perforce integration test (run elevated). Proves a shado slot becomes an
# INDEPENDENT P4 workspace via the p4-init hook: own client, own .p4config,
# have-list flushed to the base changelist (0 transfer), isolated p4 edit.
param([string]$Log = "$env:TEMP\shado-p4init.log")

Start-Transcript -Path $Log -Force | Out-Null
$ErrorActionPreference = 'Continue'
$shado = "c:\Users\benjcooley\projects\popbot-ai\shado\shado.exe"
$hook  = "c:\Users\benjcooley\projects\popbot-ai\shado\hooks\p4-init.ps1"
$p4    = "C:\pb-tools\p4.exe"
$env:SHADO_HOME = "C:\shado-p4-home"
$warm  = "C:\shado-p4-warm"
$port  = "ssl:136.114.67.118:1666"; $user = "benjcooley"; $view = "//depot/PopBotGame/Main/..."
$fail  = $false
function Check($n, [bool]$c) { if ($c) { Write-Host "PASS  $n" } else { Write-Host "FAIL  $n"; $script:fail = $true } }

# auth (P4PASSWD avoids the CRLF-on-stdin issue) + machine P4CONFIG so .p4config works
$env:P4PASSWD = '@DirkGently9'
& $p4 -p $port trust -y *> $null
& $p4 -p $port -u $user login *> $null
& $p4 set P4CONFIG=.p4config *> $null

# cleanup any prior run
& $shado restore --name p4t 2>$null | Out-Null
$warmClient = "shado_warm_test"
& $p4 -p $port -u $user client -d -f $warmClient 2>$null | Out-Null
& $p4 -p $port -u $user client -d -f shado_p4t_slot1 2>$null | Out-Null
Remove-Item $env:SHADO_HOME, $warm -Recurse -Force -EA SilentlyContinue

# 1. build a warm folder by syncing the depot
New-Item -ItemType Directory $warm -Force | Out-Null
$right = "//$warmClient/PopBotGame/Main/..."
& $p4 -p $port -u $user --field "Root=$warm" --field "View=$view $right" --field "Host=" client -o $warmClient | & $p4 -p $port -u $user client -i | Out-Null
& $p4 -p $port -u $user -c $warmClient sync "//$warmClient/..." 2>&1 | Out-Null
$chLine = (& $p4 -p $port -u $user changes -m1 -s submitted "//depot/PopBotGame/Main/...")
$baseCL = ($chLine -replace '^Change (\d+).*', '$1')
Write-Host "warm synced to CL $baseCL"
Check "warm folder has README" (Test-Path "$warm\PopBotGame\Main\README.md")

# 2. shado create (base + main only), then add a slot WITH the p4-init hook
& $shado create $warm --name p4t --count 0 --size-gb 2

$env:SHADO_P4_VIEW = $view
$env:SHADO_P4_CHANGELIST = $baseCL
$env:P4PORT = $port; $env:P4USER = $user
$env:SHADO_P4_CLIENT = "shado_p4t_slot1"
& $shado clone create --name p4t --slot 1 --hook "$hook"

$slot1 = "$env:SHADO_HOME\shadows\p4t\1"
Check "slot has inherited tree"     (Test-Path "$slot1\PopBotGame\Main\README.md")
Check "slot has its own .p4config"  (Test-Path "$slot1\.p4config")

# 3. the per-slot client is flushed (nothing opened, and a fresh sync transfers nothing)
$syncOut = (& $p4 -p $port -u $user -c shado_p4t_slot1 sync "//shado_p4t_slot1/..." 2>&1) -join "`n"
Check "flushed have-list: sync is a no-op (file(s) up-to-date)" ($syncOut -match 'up-to-date' -or $syncOut -match 'file\(s\) up')

# 4. p4 edit via the slot client opens the file on THAT client (isolated, writable)
& $p4 -p $port -u $user -c shado_p4t_slot1 edit "//shado_p4t_slot1/PopBotGame/Main/README.md" 2>&1 | Out-Null
$opened = (& $p4 -p $port -u $user -c shado_p4t_slot1 opened 2>&1) -join "`n"
Check "p4 edit opened README on the slot client" ($opened -match 'README.md')
Check "edited file is writable in the slot diff" (-not (Get-Item "$slot1\PopBotGame\Main\README.md").IsReadOnly)

# cleanup
& $p4 -p $port -u $user -c shado_p4t_slot1 revert "//shado_p4t_slot1/..." 2>&1 | Out-Null
& $shado restore --name p4t | Out-Null
& $p4 -p $port -u $user client -d -f shado_p4t_slot1 2>&1 | Out-Null
& $p4 -p $port -u $user client -d -f $warmClient 2>&1 | Out-Null
Remove-Item $env:SHADO_HOME, $warm -Recurse -Force -EA SilentlyContinue

Write-Host "`nP4INIT_RESULT=$(if ($fail) {'FAIL'} else {'PASS'})"
Stop-Transcript | Out-Null

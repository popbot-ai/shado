#requires -Version 5.1
<#
  shado - shadow workspace controller

  Manages "shadow workspaces": one saturated, frozen, read-only base VHDX (a full
  Perforce/game-dev tree synced + warmed once) plus many differencing-VHDX child
  clones, one per slot. Each clone inherits the entire warm tree instantly and
  stores only its own deltas - so a fresh, build-ready workspace off a 0.5-1TB
  base costs seconds and tens of MB instead of a full re-sync + cold build.

  Windows + Hyper-V only. Privileged operations (VHDX create/mount) require an
  elevated shell. See `shado doctor`.

  Usage:
    shado doctor
    shado base create  --name <n> [--size-gb 64] [--p4port ssl:host:1666 --p4user u --p4view //depot/Proj/Main/...] [--no-p4]
    shado base saturate --name <n>            # (re)sync the base from Perforce
    shado base freeze  --name <n>             # dismount + mark read-only (immutable)
    shado base ls
    shado base rm      --name <n>
    shado clone create --base <n> --slot <S>  # differencing child + mount
    shado clone reset  --base <n> --slot <S>  # destroy + recreate (instant clean)
    shado clone rm     --slot <S>
    shado clone ls
    shado ls
    shado json <ls|base ls|clone ls>          # machine-readable output

  Store: %SHADO_HOME% (default C:\ProgramData\shado) holds store\*.vhdx + registry.json
#>

$ErrorActionPreference = 'Stop'

# ---------- paths / registry ----------
$script:Home  = if ($env:SHADO_HOME) { $env:SHADO_HOME } else { Join-Path $env:ProgramData 'shado' }
$script:Store = Join-Path $script:Home 'store'
$script:RegPath = Join-Path $script:Home 'registry.json'

function Ensure-Home {
  if (-not (Test-Path $script:Store)) { New-Item -ItemType Directory -Path $script:Store -Force | Out-Null }
}
function Load-Reg {
  if (Test-Path $script:RegPath) {
    return Get-Content $script:RegPath -Raw | ConvertFrom-Json
  }
  return [pscustomobject]@{ bases = @(); clones = @() }
}
function Save-Reg($reg) {
  Ensure-Home
  # force arrays so single-element collections serialize as JSON arrays
  $reg.bases  = @($reg.bases)
  $reg.clones = @($reg.clones)
  $reg | ConvertTo-Json -Depth 8 | Set-Content $script:RegPath -Encoding utf8
}
function Get-Base($reg, $name)  { @($reg.bases)  | Where-Object { $_.name -eq $name } | Select-Object -First 1 }
function Get-Clone($reg, $slot) { @($reg.clones) | Where-Object { $_.slot -eq $slot } | Select-Object -First 1 }

# ---------- helpers ----------
function Is-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Require-Admin {
  if (-not (Is-Admin)) { throw "This operation needs an elevated shell. Right-click PowerShell -> Run as administrator, or run 'shado doctor'." }
}
function Fail($msg) { Write-Host "ERROR: $msg" -ForegroundColor Red; exit 1 }
function Info($msg) { Write-Host $msg -ForegroundColor Cyan }
function Ok($msg)   { Write-Host $msg -ForegroundColor Green }

# Bring a just-mounted VHDX online with a drive letter. Differencing children
# inherit the base's GPT disk/partition GUIDs, so the 2nd+ simultaneous mount can
# land OFFLINE from a signature collision - online it (Windows resignatures into
# the child diff) and assign a letter. Returns "<L>:".
function Mount-Online([string]$vhdxPath) {
  Mount-VHD -Path $vhdxPath -ErrorAction Stop | Out-Null
  $disk = Get-DiskImage -ImagePath $vhdxPath | Get-Disk
  if ($disk.IsOffline)  { Set-Disk -Number $disk.Number -IsOffline $false }
  if ($disk.IsReadOnly) { Set-Disk -Number $disk.Number -IsReadOnly $false }
  Start-Sleep -Milliseconds 600
  $disk = Get-DiskImage -ImagePath $vhdxPath | Get-Disk
  $part = Get-Partition -DiskNumber $disk.Number | Where-Object { $_.Type -eq 'Basic' } |
          Sort-Object Size -Descending | Select-Object -First 1
  if (-not $part) { throw "no basic partition found on $vhdxPath" }
  if (-not $part.DriveLetter) {
    Add-PartitionAccessPath -DiskNumber $disk.Number -PartitionNumber $part.PartitionNumber -AssignDriveLetter
    $part = Get-Partition -DiskNumber $disk.Number -PartitionNumber $part.PartitionNumber
  }
  return "$($part.DriveLetter):"
}
function Dismount-Quiet([string]$vhdxPath) {
  try { Dismount-VHD -Path $vhdxPath -ErrorAction SilentlyContinue } catch {}
}

# ---------- simple flag parser:  --key value  /  --flag (boolean) ----------
function Parse-Flags([string[]]$arglist) {
  $h = @{}
  for ($i = 0; $i -lt $arglist.Count; $i++) {
    $a = $arglist[$i]
    if ($a -like '--*') {
      $key = $a.Substring(2)
      if ($i + 1 -lt $arglist.Count -and $arglist[$i+1] -notlike '--*') { $h[$key] = $arglist[$i+1]; $i++ }
      else { $h[$key] = $true }
    }
  }
  return $h
}
function Need($flags, $name) {
  if (-not $flags.ContainsKey($name) -or [string]::IsNullOrWhiteSpace([string]$flags[$name])) {
    Fail "missing required --$name"
  }
  return [string]$flags[$name]
}

# ---------- p4 ----------
function P4-Path { $p = (Get-Command p4 -ErrorAction SilentlyContinue).Source; if ($p) { return $p } ; if (Test-Path 'C:\pb-tools\p4.exe') { return 'C:\pb-tools\p4.exe' } ; return $null }
function Invoke-P4 {
  param([string]$port,[string]$user,[string]$client,[Parameter(ValueFromRemainingArguments)]$rest)
  $p4 = P4-Path; if (-not $p4) { throw "p4.exe not found on PATH (or C:\pb-tools\p4.exe)" }
  $argv = @()
  if ($port)   { $argv += @('-p', $port) }
  if ($user)   { $argv += @('-u', $user) }
  if ($client) { $argv += @('-c', $client) }
  $argv += $rest
  & $p4 @argv
}

# =====================================================================
# commands
# =====================================================================
function Cmd-Doctor {
  Info "shado doctor"
  $okAll = $true
  $admin = Is-Admin
  "{0,-26} {1}" -f "elevated (admin):", $(if ($admin) { "yes" } else { "NO  - privileged ops will fail" }) | Write-Host
  if (-not $admin) { $okAll = $false }
  $hv = [bool](Get-Command New-VHD -ErrorAction SilentlyContinue) -and [bool](Get-Command Mount-VHD -ErrorAction SilentlyContinue)
  "{0,-26} {1}" -f "Hyper-V cmdlets:", $(if ($hv) { "yes" } else { "NO  - enable Microsoft-Hyper-V-All + reboot" }) | Write-Host
  if (-not $hv) { $okAll = $false }
  $p4 = P4-Path
  "{0,-26} {1}" -f "p4 client:", $(if ($p4) { $p4 } else { "not found (Perforce ops unavailable)" }) | Write-Host
  "{0,-26} {1}" -f "SHADO_HOME:", $script:Home | Write-Host
  $drv = Get-PSDrive ($script:Home.Substring(0,1)) -ErrorAction SilentlyContinue
  if ($drv) { "{0,-26} {1:N1} GB free" -f "store volume:", ($drv.Free/1GB) | Write-Host }
  Write-Host ""
  if ($okAll) { Ok "READY" } else { Write-Host "NOT READY - resolve the items marked above." -ForegroundColor Yellow }
}

function Cmd-BaseCreate($flags) {
  Require-Admin; Ensure-Home
  $name = Need $flags 'name'
  $sizeGb = if ($flags.ContainsKey('size-gb')) { [double]$flags['size-gb'] } else { 64 }
  $reg = Load-Reg
  if (Get-Base $reg $name) { Fail "base '$name' already exists" }
  $vhdx = Join-Path $script:Store "base-$name.vhdx"
  if (Test-Path $vhdx) { Fail "$vhdx already on disk" }

  Info "creating base VHDX '$name' ($sizeGb GB dynamic) at $vhdx"
  New-VHD -Path $vhdx -Dynamic -SizeBytes ([int64]($sizeGb * 1GB)) | Out-Null
  $disk = Mount-VHD -Path $vhdx -Passthru | Get-Disk
  Initialize-Disk -Number $disk.Number -PartitionStyle GPT | Out-Null
  $part = New-Partition -DiskNumber $disk.Number -UseMaximumSize -AssignDriveLetter
  Format-Volume -DriveLetter $part.DriveLetter -FileSystem NTFS -NewFileSystemLabel "shado-$name" -Confirm:$false | Out-Null
  $mount = "$($part.DriveLetter):"
  Ok "base mounted at $mount"

  $base = [pscustomobject]@{
    name = $name; vhdx = $vhdx; sizeGb = $sizeGb; frozen = $false; mount = $mount
    p4port = [string]$flags['p4port']; p4user = [string]$flags['p4user']; p4view = [string]$flags['p4view']
    p4client = "shado_base_$name"; syncedChange = $null
  }
  $reg.bases = @($reg.bases) + $base
  Save-Reg $reg

  if (-not $flags.ContainsKey('no-p4') -and $base.p4port -and $base.p4view) {
    Info "saturating base from Perforce ($($base.p4view)) ..."
    Saturate-Base $base $mount
    $reg = Load-Reg
  } else {
    Info "skipping Perforce sync (--no-p4 or no --p4port/--p4view). Populate $mount yourself, then 'shado base freeze --name $name'."
  }
  Ok "base '$name' created. When fully warmed, run: shado base freeze --name $name"
}

function Saturate-Base($base, $mount) {
  $root = Join-Path $mount 'workspace'
  if (-not (Test-Path $root)) { New-Item -ItemType Directory -Path $root -Force | Out-Null }
  $client = $base.p4client
  $viewLeft = $base.p4view
  $viewRight = "//$client/" + ($viewLeft -replace '^//[^/]+/', '')
  Invoke-P4 -port $base.p4port -user $base.p4user -client $null trust -y | Out-Null
  Invoke-P4 -port $base.p4port -user $base.p4user -client $null --field "Root=$root" --field "View=$viewLeft $viewRight" --field "Host=" client -o $client | Invoke-P4 -port $base.p4port -user $base.p4user -client $null client -i | Out-Null
  Info "p4 sync (this transfers the full tree once)..."
  Invoke-P4 -port $base.p4port -user $base.p4user -client $client sync
  $chg = (Invoke-P4 -port $base.p4port -user $base.p4user -client $client changes -m1 -s submitted "//$client/...") 2>$null
  $reg = Load-Reg; $b = Get-Base $reg $base.name
  if ($b) { $b.syncedChange = "$chg"; Save-Reg $reg }
  Ok "base synced into $root"
}

function Cmd-BaseFreeze($flags) {
  Require-Admin
  $name = Need $flags 'name'
  $reg = Load-Reg; $base = Get-Base $reg $name
  if (-not $base) { Fail "no base '$name'" }
  if ($base.frozen) { Info "base '$name' already frozen"; return }
  Info "freezing base '$name'"
  Dismount-Quiet $base.vhdx
  Set-ItemProperty -Path $base.vhdx -Name IsReadOnly -Value $true
  $base.frozen = $true; $base.mount = $null
  Save-Reg $reg
  Ok "base '$name' frozen read-only at $($base.vhdx) - ready to clone."
}

function Cmd-CloneCreate($flags) {
  Require-Admin; Ensure-Home
  $baseName = Need $flags 'base'
  $slot = Need $flags 'slot'
  $reg = Load-Reg
  $base = Get-Base $reg $baseName
  if (-not $base) { Fail "no base '$baseName'" }
  if (-not $base.frozen) { Fail "base '$baseName' is not frozen yet - run 'shado base freeze --name $baseName'" }
  if (Get-Clone $reg $slot) { Fail "slot $slot already in use - 'shado clone reset --base $baseName --slot $slot' to refresh" }
  $vhdx = Join-Path $script:Store "clone-$baseName-slot$slot.vhdx"
  if (Test-Path $vhdx) { Dismount-Quiet $vhdx; Remove-Item $vhdx -Force }

  Info "creating differencing clone for slot $slot off base '$baseName'"
  New-VHD -Path $vhdx -ParentPath $base.vhdx -Differencing | Out-Null
  $mount = Mount-Online $vhdx
  Ok "slot $slot mounted at $mount (warm, inherited from base)"

  $clone = [pscustomobject]@{ slot = $slot; base = $baseName; vhdx = $vhdx; mount = $mount; created = (Get-Date).ToString('s') }
  $reg.clones = @($reg.clones) + $clone
  Save-Reg $reg

  if (-not $flags.ContainsKey('no-p4') -and $base.p4port -and $base.p4view) {
    Info "wiring per-slot Perforce client (flush to base's have-list, no re-transfer)..."
    try { Setup-CloneP4 $base $slot $mount } catch { Write-Host "  p4 wiring skipped: $($_.Exception.Message)" -ForegroundColor Yellow }
  }
  Ok "slot $slot ready at $mount"
}

function Setup-CloneP4($base, $slot, $mount) {
  $root = Join-Path $mount 'workspace'
  $client = "shado_$($base.name)_slot$slot"
  $viewLeft = $base.p4view
  $viewRight = "//$client/" + ($viewLeft -replace '^//[^/]+/', '')
  Invoke-P4 -port $base.p4port -user $base.p4user -client $null trust -y | Out-Null
  Invoke-P4 -port $base.p4port -user $base.p4user -client $null --field "Root=$root" --field "View=$viewLeft $viewRight" --field "Host=" client -o $client | Invoke-P4 -port $base.p4port -user $base.p4user -client $null client -i | Out-Null
  # flush: tell the server we already have the files (physically present via the base) at the synced changelist
  Invoke-P4 -port $base.p4port -user $base.p4user -client $client flush "//$client/..." | Out-Null
  Ok "  p4 client '$client' rooted at $root (flushed, 0 bytes transferred)"
}

function Cmd-CloneReset($flags) {
  Require-Admin
  $baseName = Need $flags 'base'
  $slot = Need $flags 'slot'
  $reg = Load-Reg
  $clone = Get-Clone $reg $slot
  if ($clone) {
    Info "destroying slot $slot"
    Dismount-Quiet $clone.vhdx
    if (Test-Path $clone.vhdx) { Remove-Item $clone.vhdx -Force }
    $reg.clones = @(@($reg.clones) | Where-Object { $_.slot -ne $slot })
    Save-Reg $reg
  }
  Cmd-CloneCreate $flags
}

function Cmd-CloneRm($flags) {
  Require-Admin
  $slot = Need $flags 'slot'
  $reg = Load-Reg
  $clone = Get-Clone $reg $slot
  if (-not $clone) { Fail "no clone in slot $slot" }
  Info "removing slot $slot"
  Dismount-Quiet $clone.vhdx
  if (Test-Path $clone.vhdx) { Remove-Item $clone.vhdx -Force }
  $reg.clones = @(@($reg.clones) | Where-Object { $_.slot -ne $slot })
  Save-Reg $reg
  Ok "slot $slot removed"
}

function Cmd-BaseRm($flags) {
  Require-Admin
  $name = Need $flags 'name'
  $reg = Load-Reg
  $base = Get-Base $reg $name
  if (-not $base) { Fail "no base '$name'" }
  $kids = @(@($reg.clones) | Where-Object { $_.base -eq $name })
  if ($kids.Count -gt 0) { Fail "base '$name' still has $($kids.Count) clone(s); remove them first" }
  Info "removing base '$name'"
  Dismount-Quiet $base.vhdx
  if (Test-Path $base.vhdx) { Set-ItemProperty -Path $base.vhdx -Name IsReadOnly -Value $false -ErrorAction SilentlyContinue; Remove-Item $base.vhdx -Force }
  $reg.bases = @(@($reg.bases) | Where-Object { $_.name -ne $name })
  Save-Reg $reg
  Ok "base '$name' removed"
}

function Size-Of($path) { if (Test-Path $path) { "{0:N0} MB" -f ((Get-Item $path).Length/1MB) } else { "(missing)" } }

function Cmd-Ls {
  $reg = Load-Reg
  Write-Host "BASES" -ForegroundColor Cyan
  if (@($reg.bases).Count -eq 0) { Write-Host "  (none)" }
  foreach ($b in @($reg.bases)) {
    "  {0,-16} frozen={1,-5} size={2,-10} {3}" -f $b.name, $b.frozen, (Size-Of $b.vhdx), $b.vhdx | Write-Host
  }
  Write-Host "CLONES" -ForegroundColor Cyan
  if (@($reg.clones).Count -eq 0) { Write-Host "  (none)" }
  foreach ($c in @($reg.clones)) {
    "  slot {0,-4} base={1,-16} mount={2,-5} diff={3}" -f $c.slot, $c.base, $c.mount, (Size-Of $c.vhdx) | Write-Host
  }
}

function Cmd-Json($rest) {
  $reg = Load-Reg
  $reg | ConvertTo-Json -Depth 8
}

# =====================================================================
# dispatch
# =====================================================================
$all = @($args)
if ($all.Count -eq 0) { Cmd-Doctor; exit 0 }
$group = $all[0]
$action = if ($all.Count -ge 2) { $all[1] } else { '' }
$flags  = Parse-Flags ($all | Select-Object -Skip 1)

switch ($group) {
  'doctor' { Cmd-Doctor }
  'ls'     { Cmd-Ls }
  'json'   { Cmd-Json ($all | Select-Object -Skip 1) }
  'base' {
    switch ($action) {
      'create'   { Cmd-BaseCreate (Parse-Flags ($all | Select-Object -Skip 2)) }
      'saturate' { Require-Admin; $f = Parse-Flags ($all | Select-Object -Skip 2); $reg = Load-Reg; $b = Get-Base $reg (Need $f 'name'); if (-not $b) { Fail "no base" }; $m = (Mount-Online $b.vhdx); Saturate-Base $b $m }
      'freeze'   { Cmd-BaseFreeze (Parse-Flags ($all | Select-Object -Skip 2)) }
      'ls'       { Cmd-Ls }
      'rm'       { Cmd-BaseRm (Parse-Flags ($all | Select-Object -Skip 2)) }
      default    { Fail "unknown 'base' action '$action' (create|saturate|freeze|ls|rm)" }
    }
  }
  'clone' {
    switch ($action) {
      'create' { Cmd-CloneCreate (Parse-Flags ($all | Select-Object -Skip 2)) }
      'reset'  { Cmd-CloneReset  (Parse-Flags ($all | Select-Object -Skip 2)) }
      'rm'     { Cmd-CloneRm     (Parse-Flags ($all | Select-Object -Skip 2)) }
      'ls'     { Cmd-Ls }
      default  { Fail "unknown 'clone' action '$action' (create|reset|rm|ls)" }
    }
  }
  default { Fail "unknown command '$group' (doctor|base|clone|ls|json)" }
}

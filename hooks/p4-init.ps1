# shado post-mount hook: turn a freshly-mounted shado slot into an INDEPENDENT
# Perforce workspace.
#
# A slot is a byte-for-byte clone of the warmed tree, so it inherits the base's
# Perforce identity (same client name / .p4config / have-list). Two slots on one
# client = have-list collisions and clobbered submits. This hook gives the slot
# its own client + .p4config and flushes its have-list to the base changelist
# with ZERO file transfer (the files are already physically present via the base).
#
# Reads (shado sets SHADO_MOUNT; the caller/PopBot sets the rest):
#   SHADO_MOUNT          - the slot's mount folder (set by shado)        [required]
#   P4PORT, P4USER       - Perforce connection                          [required]
#   SHADO_P4_CLIENT      - the per-slot client name to create           [required]
#   SHADO_P4_VIEW        - depot view, e.g. //depot/Game/Main/...        [required]
#   SHADO_P4_CHANGELIST  - the base's synced changelist (number)        [required]
#                          must match what the base was synced to, or sync drifts

$ErrorActionPreference = 'Stop'

function Need($name) {
  $v = [Environment]::GetEnvironmentVariable($name)
  if ([string]::IsNullOrWhiteSpace($v)) { Write-Error "p4-init: $name not set"; exit 1 }
  return $v
}

$root   = Need 'SHADO_MOUNT'
$port   = Need 'P4PORT'
$user   = Need 'P4USER'
$client = Need 'SHADO_P4_CLIENT'
$view   = Need 'SHADO_P4_VIEW'
$cl     = Need 'SHADO_P4_CHANGELIST'

$p4 = (Get-Command p4 -ErrorAction SilentlyContinue).Source
if (-not $p4) { if (Test-Path 'C:\pb-tools\p4.exe') { $p4 = 'C:\pb-tools\p4.exe' } else { Write-Error 'p4 not found'; exit 1 } }

& $p4 -p $port trust -y *> $null

# Build the client view: depot side -> //<client>/<path-after-depot-root>
$right = '//' + $client + '/' + ($view -replace '^//[^/]+/', '')
$spec = & $p4 -p $port -u $user --field "Root=$root" --field "View=$view $right" --field "Host=" client -o $client
$spec | & $p4 -p $port -u $user client -i | Out-Null

# .p4config at the slot root so every p4 command run inside the slot uses ITS client.
# (requires `p4 set P4CONFIG=.p4config` once per machine)
"P4PORT=$port`r`nP4USER=$user`r`nP4CLIENT=$client" | Set-Content -Path (Join-Path $root '.p4config') -Encoding ascii

# Flush have-list to the base changelist: tell the server we already have these
# files at @$cl. No file transfer - they came in via the inherited base.
& $p4 -p $port -u $user -c $client flush "//$client/...@$cl" | Out-Null

Write-Host "p4-init: client '$client' rooted at $root, flushed to @$cl (0 bytes transferred)"

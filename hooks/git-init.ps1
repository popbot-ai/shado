# shado post-mount hook: prep a freshly-mounted shado slot as an INDEPENDENT git
# working tree.
#
# The slot is a clone of the warmed tree, so it carries a real .git dir. Fixups:
# drop stale lock files left from the frozen base, exclude the volume's system
# folders (the mount root is an NTFS volume root) so they don't show as untracked,
# and switch to the chat's branch. Warm build caches are inherited - no reimport.
#
# Reads (shado sets SHADO_MOUNT; the caller sets the branch):
#   SHADO_MOUNT       - the slot's mount folder (set by shado)     [required]
#   SHADO_GIT_BRANCH  - branch to switch to / create              [optional]

$ErrorActionPreference = 'Continue'

$root = [Environment]::GetEnvironmentVariable('SHADO_MOUNT')
if ([string]::IsNullOrWhiteSpace($root)) { Write-Error 'git-init: SHADO_MOUNT not set'; exit 1 }

$gitDir = Join-Path $root '.git'
if (-not (Test-Path $gitDir)) { Write-Host "git-init: no .git at $root - nothing to do"; exit 0 }

# 1. drop stale lock files captured in the frozen base
Get-ChildItem $gitDir -Filter '*.lock' -Recurse -ErrorAction SilentlyContinue |
  Remove-Item -Force -ErrorAction SilentlyContinue

# 2. keep the NTFS volume-root system folders out of git's untracked list
$exclude = Join-Path $gitDir 'info\exclude'
$sysDirs = @('/System Volume Information/', '/$RECYCLE.BIN/', '.p4config')
$existing = if (Test-Path $exclude) { Get-Content $exclude } else { @() }
foreach ($e in $sysDirs) { if ($existing -notcontains $e) { Add-Content -Path $exclude -Value $e } }

# 3. switch to the chat's branch
Push-Location $root
try {
  $branch = [Environment]::GetEnvironmentVariable('SHADO_GIT_BRANCH')
  if (-not [string]::IsNullOrWhiteSpace($branch)) {
    & git rev-parse --verify --quiet $branch *> $null
    if ($LASTEXITCODE -eq 0) { & git checkout $branch *> $null }
    else { & git checkout -b $branch *> $null }
  }
  $head = (& git rev-parse --abbrev-ref HEAD 2>$null)
  Write-Host "git-init: ready at $root on $head"
} finally { Pop-Location }

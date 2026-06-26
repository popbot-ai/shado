# gen-league-repo.ps1 - generate a LARGE, imaginary "Riot-style" game depot tree
# with dummy data, to stress-test the shado + Perforce pipeline: hundreds of
# thousands of files, deep folders, some very long path names (>260 chars).
#
# Top-level layout (AAA studio shape):
#   ASSETS/        champions/skins/categories/LODs (the bulk - many small binaries)
#   PROPERTIES/    per-champion/skin data files
#   binaries/      compiled engine/tool blobs (fewer, larger)
#   tools/         pipeline/build scripts
#   Translations/  Text/<locale>/... and Audio/<locale>/... (loc text + VO)
#
# Long-path safe (uses the \\?\ prefix) so it works past MAX_PATH. Defaults to
# the D: drive. Fast: .NET static file ops, a reused random-buffer pool, and a
# created-dir cache.
#
# Usage:
#   pwsh scripts/gen-league-repo.ps1 -Root D:\league-clone -Files 250000

[CmdletBinding()]
param(
  [string] $Root  = 'D:\league-clone',
  [int]    $Files = 250000,
  [int]    $Seed  = 1,
  [switch] $Clean
)

$ErrorActionPreference = 'Stop'
$rng = [Random]::new($Seed)
$LP = '\\?\' + $Root   # long-path-safe prefix for all .NET file ops

if ($Clean -and (Test-Path $Root)) {
  Write-Host "cleaning $Root ..."
  cmd /c "rmdir /s /q `"$Root`"" 2>$null
}
[IO.Directory]::CreateDirectory($LP) | Out-Null

# Random-buffer pool (reused; a VHDX stores each file's blocks separately, so
# content reuse is fine for size/count testing and keeps generation fast).
function Pool([int]$size, [int]$n) {
  $a = New-Object 'System.Collections.Generic.List[byte[]]'
  for ($i = 0; $i -lt $n; $i++) { $b = [byte[]]::new($size); $rng.NextBytes($b); $a.Add($b) }
  return $a
}
$small = Pool 4096 12     # 4 KB  (data / small assets)
$med   = Pool 32768 8     # 32 KB (textures / models / VO)
$big   = Pool 262144 4    # 256 KB (binaries)

$dirs = [System.Collections.Generic.HashSet[string]]::new()
function W([string]$rel, [byte[]]$buf) {
  $full = $LP + '\' + $rel
  $dir = [IO.Path]::GetDirectoryName($full)
  if ($dirs.Add($dir)) { [IO.Directory]::CreateDirectory($dir) | Out-Null }
  [IO.File]::WriteAllBytes($full, $buf)
}

# vocab
$champs = @('Ahri','Garen','Lux','Jinx','Yasuo','Darius','Ashe','Zed','Lulu','Thresh',
            'Ekko','Vi','Jhin','Kaisa','Sett','Viego','Akali','Leona','Senna','Ornn',
            'Aatrox','Kayn','Sylas','Aphelios','Gwen','Viktor','Orianna','Camille','Fiora','Riven')
$champs = @($champs) + (31..180 | ForEach-Object { "Champion$_" })   # ~180 champions
$locales = @('en_US','ko_KR','zh_CN','ja_JP','fr_FR','de_DE','es_ES','pt_BR','ru_RU','tr_TR')
$cats = @('Models','Textures','Animations','Particles','Audio','Materials')
$ext  = @{ Models = '.skn'; Textures = '.dds'; Animations = '.anm'; Particles = '.troy'; Audio = '.wav'; Materials = '.mat' }
$longTok = 'ExtremelyDescriptive_VeryLongAssetVariantFolderName_ForPathLengthStressTesting_' * 4
function LongMaybe([string]$s) {
  if ($rng.Next(35) -eq 0) { return $s + '_' + $longTok.Substring(0, 90 + $rng.Next(60)) }
  return $s
}

Write-Host "Generating $($Files.ToString('N0')) files at $Root (seed=$Seed) ..."
$count = 0
$report = [Math]::Max(1, [int]($Files / 20))
while ($count -lt $Files) {
  $roll = $rng.Next(100)
  if ($roll -lt 62) {
    $c = $champs[$rng.Next($champs.Count)]; $skin = 'Skin{0:D2}' -f $rng.Next(0, 12)
    $cat = $cats[$rng.Next($cats.Count)]; $lod = 'LOD{0}' -f $rng.Next(0, 3)
    $sub = LongMaybe ('variant_{0}' -f $rng.Next(0, 8))
    $name = LongMaybe ('asset_{0:D5}' -f $rng.Next(0, 99999))
    $rel = "ASSETS\Champions\$c\$skin\$cat\$lod\$sub\$name$($ext[$cat])"
    $buf = if ($cat -eq 'Models' -or $cat -eq 'Textures' -or $cat -eq 'Audio') { $med[$rng.Next($med.Count)] } else { $small[$rng.Next($small.Count)] }
  } elseif ($roll -lt 74) {
    $c = $champs[$rng.Next($champs.Count)]; $skin = 'Skin{0:D2}' -f $rng.Next(0, 12)
    $rel = "PROPERTIES\Champions\$c\$skin\" + ('props_{0:D4}.bin' -f $rng.Next(0, 9999))
    $buf = $small[$rng.Next($small.Count)]
  } elseif ($roll -lt 88) {
    $loc = $locales[$rng.Next($locales.Count)]; $c = $champs[$rng.Next($champs.Count)]
    if ($rng.Next(2) -eq 0) {
      $rel = "Translations\Text\$loc\$c\" + ('strings_{0:D4}.json' -f $rng.Next(0, 9999)); $buf = $small[$rng.Next($small.Count)]
    } else {
      $rel = "Translations\Audio\$loc\$c\" + ('vo_{0:D5}.wav' -f $rng.Next(0, 99999)); $buf = $med[$rng.Next($med.Count)]
    }
  } elseif ($roll -lt 95) {
    $area = @('build','pipeline','validation','export','localization')[$rng.Next(5)]
    $rel = "tools\$area\sub{0}\" -f $rng.Next(0, 20); $rel += ('tool_{0:D4}.py' -f $rng.Next(0, 9999))
    $buf = $small[$rng.Next($small.Count)]
  } else {
    $sub = @('Win64','Tools','ThirdParty')[$rng.Next(3)]
    $rel = "binaries\$sub\" + ('lib_{0:D4}.bin' -f $rng.Next(0, 9999)); $buf = $big[$rng.Next($big.Count)]
  }
  W $rel $buf
  $count++
  if ($count % $report -eq 0) { Write-Host ('  {0:N0} / {1:N0}' -f $count, $Files) }
}

[IO.File]::WriteAllText($LP + '\project.json', '{ "project": "LeagueClone", "engine": "Riftbreaker", "version": "14.7" }')
[IO.File]::WriteAllText($LP + '\.p4ignore', "Intermediate/`r`nSaved/`r`nDerivedDataCache/`r`n")

# report via .NET enumerate (handles long paths)
$n = 0; $total = 0L
foreach ($f in [IO.Directory]::EnumerateFiles($LP, '*', [IO.SearchOption]::AllDirectories)) {
  $n++; $total += ([IO.FileInfo]::new($f)).Length
  if ($n % 50000 -eq 0) { Write-Host "  counted $($n.ToString('N0'))..." }
}
Write-Host ("DONE  files={0}  total={1:N2} GB  at {2}" -f $n.ToString('N0'), ($total / 1GB), $Root)

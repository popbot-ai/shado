package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Windows copy-on-write backend, via the Hyper-V PowerShell cmdlets.
//
// This is a thin, replaceable layer (the next step is the native VirtDisk API
// through golang.org/x/sys/windows, which drops the Hyper-V-feature dependency).
// Keep every disk primitive behind these functions so that swap stays isolated,
// and so macOS (APFS clonefile) / Linux (reflink) backends can implement the same
// surface later.

func runPS(script string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if verbose && len(out) > 0 {
		os.Stdout.Write(out)
	}
	return string(out), err
}

func parseKV(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func hyperVAvailable() bool {
	out, err := runPS(`if ((Get-Command New-VHD -EA SilentlyContinue) -and (Get-Command Mount-VHD -EA SilentlyContinue)) { 'YES' } else { 'NO' }`)
	return err == nil && strings.Contains(out, "YES")
}

// vhdxCreateBase creates a dynamic VHDX, mounts it WITHOUT a drive letter (so no
// AutoPlay popup), GPT-inits, formats NTFS, and attaches it at the given folder.
func vhdxCreateBase(path string, sizeBytes int64, label, folder string) error {
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
New-VHD -Path '%s' -Dynamic -SizeBytes %d | Out-Null
$d = Mount-VHD -Path '%s' -NoDriveLetter -Passthru | Get-Disk
Initialize-Disk -Number $d.Number -PartitionStyle GPT | Out-Null
$p = New-Partition -DiskNumber $d.Number -UseMaximumSize
Format-Volume -Partition $p -FileSystem NTFS -NewFileSystemLabel '%s' -Confirm:$false | Out-Null
New-Item -ItemType Directory -Path '%s' -Force | Out-Null
Add-PartitionAccessPath -DiskNumber $d.Number -PartitionNumber $p.PartitionNumber -AccessPath '%s'
Write-Output 'MOUNTOK'`, path, sizeBytes, path, label, folder, folder)
	out, err := runPS(script)
	if err != nil || !strings.Contains(out, "MOUNTOK") {
		return fmt.Errorf("create base vhdx: %v\n%s", err, out)
	}
	return nil
}

// vhdxCreateDiff creates a differencing child off parent (not mounted).
func vhdxCreateDiff(path, parent string) error {
	out, err := runPS(fmt.Sprintf(`New-VHD -Path '%s' -ParentPath '%s' -Differencing | Out-Null`, path, parent))
	if err != nil {
		return fmt.Errorf("create differencing vhdx: %v\n%s", err, out)
	}
	return nil
}

// vhdxMountFolder mounts a VHDX and surfaces its volume at an (empty) folder mount
// point. Differencing children inherit the base's GPT/partition GUIDs, so a 2nd+
// simultaneous mount can land OFFLINE from a signature collision - we online it
// (Windows resignatures into the child diff), strip any stray auto drive letter,
// and attach the folder access path.
func vhdxMountFolder(path, folder string) error {
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
Mount-VHD -Path '%s' -NoDriveLetter -ErrorAction SilentlyContinue | Out-Null
$disk = Get-DiskImage -ImagePath '%s' | Get-Disk
if ($disk.IsOffline)  { Set-Disk -Number $disk.Number -IsOffline $false }
if ($disk.IsReadOnly) { Set-Disk -Number $disk.Number -IsReadOnly $false }
Start-Sleep -Milliseconds 600
$disk = Get-DiskImage -ImagePath '%s' | Get-Disk
$part = Get-Partition -DiskNumber $disk.Number | Where-Object { $_.Type -eq 'Basic' } | Sort-Object Size -Descending | Select-Object -First 1
if (-not $part) { throw 'no basic partition' }
$target = '%s'
$norm = $target.TrimEnd('\')
$has = $false
foreach ($ap in @($part.AccessPaths)) { if ($ap.TrimEnd('\') -ieq $norm) { $has = $true } }
if (-not $has) {
  # strip any auto-assigned drive letters / stale folder access paths (keep the \\?\Volume GUID path)
  foreach ($ap in @($part.AccessPaths)) {
    if ($ap -and ($ap -notlike '\\?\*')) {
      Remove-PartitionAccessPath -DiskNumber $disk.Number -PartitionNumber $part.PartitionNumber -AccessPath $ap -ErrorAction SilentlyContinue
    }
  }
  # clear any stale mount-point reparse left at the target, then (re)create it empty
  if (Test-Path $target) { Remove-Item $target -Force -ErrorAction SilentlyContinue }
  New-Item -ItemType Directory -Path $target -Force | Out-Null
  Add-PartitionAccessPath -DiskNumber $disk.Number -PartitionNumber $part.PartitionNumber -AccessPath $target
}
Write-Output 'MOUNTOK'`, path, path, path, folder)
	out, err := runPS(script)
	if err != nil || !strings.Contains(out, "MOUNTOK") {
		return fmt.Errorf("mount %s -> %s: %v\n%s", path, folder, err, out)
	}
	return nil
}

func vhdxDismount(path string) error {
	_, err := runPS(fmt.Sprintf(`Dismount-VHD -Path '%s' -ErrorAction SilentlyContinue`, path))
	return err
}

func vhdxFreeze(path string) error {
	out, err := runPS(fmt.Sprintf(`$ErrorActionPreference='Stop'
Dismount-VHD -Path '%s' -ErrorAction SilentlyContinue
Set-ItemProperty -Path '%s' -Name IsReadOnly -Value $true`, path, path))
	if err != nil {
		return fmt.Errorf("freeze: %v\n%s", err, out)
	}
	return nil
}

func vhdxUnfreeze(path string) error {
	_, err := runPS(fmt.Sprintf(`Set-ItemProperty -Path '%s' -Name IsReadOnly -Value $false -ErrorAction SilentlyContinue`, path))
	return err
}

// vhdxConvert flattens a (differencing) VHDX and its parent chain into a new
// standalone VHDX - used by recache to promote a warmed shadow into a new base.
func vhdxConvert(src, dst string) error {
	out, err := runPS(fmt.Sprintf(`$ErrorActionPreference='Stop'
Convert-VHD -Path '%s' -DestinationPath '%s' -VHDType Dynamic | Out-Null`, src, dst))
	if err != nil {
		return fmt.Errorf("convert (flatten): %v\n%s", err, out)
	}
	return nil
}

func vhdxSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// robocopyMirror copies the contents of src into dst (recursive, no purge so it
// never tries to delete the volume's System Volume Information / $RECYCLE.BIN).
// robocopy exit codes 0-7 are success; >=8 is a real error.
func robocopyMirror(src, dst string) error {
	cmd := exec.Command("robocopy", src, dst,
		"/E", "/COPY:DAT", "/DCOPY:DAT", "/XJ",
		"/XD", "System Volume Information", "$RECYCLE.BIN",
		"/NFL", "/NDL", "/NJH", "/NJS", "/NP", "/R:1", "/W:1")
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if verbose && len(out) > 0 {
		os.Stdout.Write(out)
	}
	code := cmd.ProcessState.ExitCode()
	if code >= 8 {
		return fmt.Errorf("robocopy %s -> %s exit %d: %v\n%s", src, dst, code, err, string(out))
	}
	return nil
}

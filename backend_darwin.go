//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// macBackend implements copy-on-write via APFS clonefile (`cp -c`). A base is a
// frozen (read-only) directory clone of the warm folder; each shadow is an
// instant clonefile copy of that base. Writes diverge at the block level, so a
// fresh shadow shares every block with the base and costs only what it changes.
//
// Unlike the Windows VHDX backend there are no disk images and nothing to
// mount: a shadow's folder *is* its storage, so Shadow.Mount and Shadow.Vhdx
// hold the same path, and Project.BaseVhdx holds the base directory.
//
// clonefile shares blocks only within a single APFS volume, so the store, the
// shadows, and (ideally) the warm folder should all live on the same volume.
type macBackend struct{}

func newBackend() Backend { return macBackend{} }

func (macBackend) Name() string { return "APFS clonefile (macOS)" }

func (macBackend) Ready() (bool, string) {
	if _, err := exec.LookPath("cp"); err != nil {
		return false, "cp(1) not found"
	}
	return true, "APFS clonefile via cp -c"
}

// macOS needs no elevation: clones live under the user's SHADO_HOME.
func (macBackend) Privileged() bool { return true }
func (macBackend) RequireReady()    {}

func (macBackend) BasePath(name string) string {
	return filepath.Join(storeDir(), name+"-base")
}

// run executes a command, echoing output under --verbose and wrapping failures.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if verbose && len(out) > 0 {
		os.Stdout.Write(out)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}

// cloneTree COW-clones the directory hierarchy src -> dst via clonefile(2).
// `cp -c` issues clonefile per file (no data copy); -R recurses; -p preserves
// permissions and timestamps.
func cloneTree(src, dst string) error {
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return run("cp", "-cRp", src, dst)
}

func freezeTree(dir string) error   { return run("chmod", "-R", "a-w", dir) }
func unfreezeTree(dir string) error { return run("chmod", "-R", "u+w", dir) }

func (b macBackend) CreateBase(p *Project, warm string, _ float64) error {
	base := b.BasePath(p.Name)
	if fileExists(base) {
		return fmt.Errorf("%s already on disk", base)
	}
	info("cloning warm folder into frozen base (APFS clonefile) ...")
	if err := cloneTree(warm, base); err != nil {
		return err
	}
	info("freezing base read-only")
	if err := freezeTree(base); err != nil {
		return err
	}
	p.BaseVhdx = base
	p.SizeGB = float64(duBytes(base)) / float64(1<<30)
	return nil
}

func (b macBackend) CreateShadow(p *Project, id string, main bool, mountOverride string) (Shadow, error) {
	mount := mountOverride
	if mount == "" {
		mount = shadowMountPath(p, id)
	}
	if err := cloneTree(p.BaseVhdx, mount); err != nil {
		return Shadow{}, err
	}
	// the base is frozen read-only; the working shadow must be writable.
	if err := unfreezeTree(mount); err != nil {
		return Shadow{}, err
	}
	return Shadow{ID: id, Mount: mount, Vhdx: mount, CleanSize: duBytes(mount), Main: main}, nil
}

func (macBackend) RemoveShadow(s *Shadow) error {
	_ = unfreezeTree(s.Mount)
	return os.RemoveAll(s.Mount)
}

// A shadow folder is always present on macOS (no image to detach), so park /
// resume are state-only no-ops, kept for command-surface parity with Windows.
func (macBackend) ParkShadow(s *Shadow) error   { return nil }
func (macBackend) ResumeShadow(s *Shadow) error { return nil }

func (b macBackend) Recache(p *Project, main *Shadow) error {
	newBase := filepath.Join(storeDir(), p.Name+"-base.new")
	info("promoting warmed main into a new frozen base (clonefile) ...")
	if err := cloneTree(main.Mount, newBase); err != nil {
		return err
	}
	if err := freezeTree(newBase); err != nil {
		return err
	}
	for i := range p.Shadows {
		_ = unfreezeTree(p.Shadows[i].Mount)
		_ = os.RemoveAll(p.Shadows[i].Mount)
	}
	_ = unfreezeTree(p.BaseVhdx)
	_ = os.RemoveAll(p.BaseVhdx)
	finalBase := b.BasePath(p.Name)
	if err := os.Rename(newBase, finalBase); err != nil {
		return err
	}
	p.BaseVhdx = finalBase
	return nil
}

func (macBackend) ExportBase(p *Project, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	// clone the frozen base's contents out into dest, then make them writable.
	if err := run("cp", "-cRp", p.BaseVhdx+string(os.PathSeparator)+".", dest); err != nil {
		return err
	}
	return unfreezeTree(dest)
}

func (macBackend) DestroyBase(p *Project) error {
	_ = unfreezeTree(p.BaseVhdx)
	return os.RemoveAll(p.BaseVhdx)
}

// duBytes returns the on-disk size of a tree via `du -sk` (KiB -> bytes). On
// APFS this counts allocated blocks; cloned (shared) blocks are still counted,
// so it tracks growth from a clean baseline rather than unique bytes. (A 1 TB
// tree therefore takes a full walk — acceptable for v1; a future optimization
// could diff an APFS snapshot instead.)
func duBytes(path string) int64 {
	if !fileExists(path) {
		return 0
	}
	out, err := exec.Command("du", "-sk", path).Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

func (macBackend) DiskUsage(path string) int64     { return duBytes(path) }
func (macBackend) ShadowDiskUsage(s *Shadow) int64 { return duBytes(s.Vhdx) }

// ShadowReportedSize is the shadow's growth beyond its clean clone — the bytes
// it has actually written (clonefile shares the rest with the base).
func (macBackend) ShadowReportedSize(s *Shadow) int64 {
	if d := duBytes(s.Vhdx) - s.CleanSize; d > 0 {
		return d
	}
	return 0
}

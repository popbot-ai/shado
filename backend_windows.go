//go:build windows

package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// winBackend is the Windows copy-on-write backend: a frozen base VHDX with one
// mounted differencing-child VHDX per shadow. It wraps the low-level VirtDisk /
// Hyper-V primitives in vhdx.go.
type winBackend struct{}

func newBackend() Backend { return winBackend{} }

func (winBackend) Name() string { return "VHDX (Windows)" }

func (winBackend) Ready() (bool, string) {
	if hyperVAvailable() {
		return true, "Hyper-V cmdlets present"
	}
	return false, "enable Microsoft-Hyper-V-All + reboot"
}

func (winBackend) Privileged() bool { return isAdmin() }

func (winBackend) RequireReady() {
	if !isAdmin() {
		fail("this operation needs an elevated shell (Run as administrator). See 'shado doctor'.")
	}
}

func isAdmin() bool {
	out, err := runPS(`[bool](([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole('Administrators'))`)
	return err == nil && strings.Contains(strings.ToLower(out), "true")
}

func (winBackend) BasePath(name string) string {
	return filepath.Join(storeDir(), name+"-base.vhdx")
}

func (b winBackend) CreateBase(p *Project, warm string, sizeGB float64) error {
	if sizeGB == 0 {
		sizeGB = float64(dirSizeBytes(warm))/float64(1<<30)*1.4 + 2 // content * 1.4 + headroom
	}
	sizeGB = math.Ceil(sizeGB)             // whole GB => MB-aligned, valid for New-VHD
	sizeBytes := int64(sizeGB) * (1 << 30) // virtual max; dynamic VHDX only allocates what's used
	baseVhdx := b.BasePath(p.Name)
	if fileExists(baseVhdx) {
		return fmt.Errorf("%s already on disk", baseVhdx)
	}
	info("creating base for %q (%.0f GB max) from %s", p.Name, sizeGB, warm)
	baseBuild := filepath.Join(storeDir(), "_build-"+p.Name)
	if err := vhdxCreateBase(baseVhdx, sizeBytes, "shado-"+p.Name, baseBuild); err != nil {
		return err
	}
	info("copying warm folder into base ...")
	if err := robocopyMirror(warm, baseBuild); err != nil {
		return err
	}
	info("freezing base read-only")
	if err := vhdxFreeze(baseVhdx); err != nil {
		return err
	}
	_ = os.RemoveAll(baseBuild)
	p.BaseVhdx = baseVhdx
	p.SizeGB = sizeGB
	return nil
}

func (winBackend) CreateShadow(p *Project, id string, main bool, mountOverride string) (Shadow, error) {
	vhdx := shadowVhdxPath(p, id)
	mount := mountOverride
	if mount == "" {
		mount = shadowMountPath(p, id)
	}
	_ = vhdxDismount(vhdx)
	_ = os.Remove(vhdx)
	if err := vhdxCreateDiff(vhdx, p.BaseVhdx); err != nil {
		return Shadow{}, err
	}
	if err := vhdxMountFolder(vhdx, mount); err != nil {
		return Shadow{}, err
	}
	return Shadow{ID: id, Mount: mount, Vhdx: vhdx, CleanSize: vhdxSize(vhdx), Main: main}, nil
}

func (winBackend) RemoveShadow(s *Shadow) error {
	_ = vhdxDismount(s.Vhdx)
	_ = os.Remove(s.Vhdx)
	_ = os.RemoveAll(s.Mount)
	return nil
}

func (winBackend) ParkShadow(s *Shadow) error   { return vhdxDismount(s.Vhdx) }
func (winBackend) ResumeShadow(s *Shadow) error { return vhdxMountFolder(s.Vhdx, s.Mount) }

func (b winBackend) Recache(p *Project, main *Shadow) error {
	newBase := filepath.Join(storeDir(), p.Name+"-base.new.vhdx")
	_ = vhdxDismount(main.Vhdx)
	_ = os.Remove(newBase)
	if err := vhdxConvert(main.Vhdx, newBase); err != nil {
		return err
	}
	if err := vhdxFreeze(newBase); err != nil {
		return err
	}
	for i := range p.Shadows {
		_ = vhdxDismount(p.Shadows[i].Vhdx)
		_ = os.Remove(p.Shadows[i].Vhdx)
	}
	_ = vhdxUnfreeze(p.BaseVhdx)
	_ = os.Remove(p.BaseVhdx)
	finalBase := b.BasePath(p.Name)
	if err := os.Rename(newBase, finalBase); err != nil {
		return err
	}
	p.BaseVhdx = finalBase
	return nil
}

func (winBackend) ExportBase(p *Project, dest string) error {
	_ = vhdxUnfreeze(p.BaseVhdx)
	tmp := filepath.Join(storeDir(), "_restore-"+p.Name)
	if err := vhdxMountFolder(p.BaseVhdx, tmp); err != nil {
		return err
	}
	err := robocopyMirror(tmp, dest)
	_ = vhdxDismount(p.BaseVhdx)
	_ = os.RemoveAll(tmp)
	return err
}

func (winBackend) DestroyBase(p *Project) error {
	_ = vhdxUnfreeze(p.BaseVhdx)
	return os.Remove(p.BaseVhdx)
}

func (winBackend) DiskUsage(path string) int64        { return vhdxSize(path) }
func (winBackend) ShadowDiskUsage(s *Shadow) int64    { return vhdxSize(s.Vhdx) }
func (winBackend) ShadowReportedSize(s *Shadow) int64 { return vhdxSize(s.Vhdx) }

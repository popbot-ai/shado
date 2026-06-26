//go:build !windows && !darwin

package main

import "fmt"

// otherBackend is a placeholder for platforms whose copy-on-write backend isn't
// wired up yet (Linux btrfs/XFS reflink + overlayfs is planned). doctor,
// version, and help still work; any privileged COW op fails with a clear note.
type otherBackend struct{}

func newBackend() Backend { return otherBackend{} }

func (otherBackend) Name() string { return "unsupported" }
func (otherBackend) Ready() (bool, string) {
	return false, "no copy-on-write backend for this OS yet (Linux reflink/overlayfs planned)"
}
func (otherBackend) Privileged() bool { return false }
func (otherBackend) RequireReady() {
	fail("shado has no copy-on-write backend for this OS yet. See 'shado doctor'.")
}

func (otherBackend) BasePath(string) string                     { return "" }
func (otherBackend) CreateBase(*Project, string, float64) error { return errUnsupported }
func (otherBackend) CreateShadow(*Project, string, bool, string) (Shadow, error) {
	return Shadow{}, errUnsupported
}
func (otherBackend) RemoveShadow(*Shadow) error        { return errUnsupported }
func (otherBackend) ParkShadow(*Shadow) error          { return errUnsupported }
func (otherBackend) ResumeShadow(*Shadow) error        { return errUnsupported }
func (otherBackend) Recache(*Project, *Shadow) error   { return errUnsupported }
func (otherBackend) ExportBase(*Project, string) error { return errUnsupported }
func (otherBackend) DestroyBase(*Project) error        { return errUnsupported }
func (otherBackend) DiskUsage(string) int64            { return 0 }
func (otherBackend) ShadowDiskUsage(*Shadow) int64     { return 0 }
func (otherBackend) ShadowReportedSize(*Shadow) int64  { return 0 }

var errUnsupported = fmt.Errorf("no copy-on-write backend for this OS yet")

package main

// Backend is the per-OS copy-on-write substrate behind shado. Windows uses
// differencing VHDX images mounted as folders; macOS uses APFS clonefile on
// plain directories. Every disk primitive lives behind this interface so the
// platform-neutral command layer (commands.go) never names a concrete backend.
//
// Each OS supplies exactly one newBackend() (build-tagged: backend_windows.go,
// backend_darwin.go, backend_other.go), selected at startup below.
type Backend interface {
	// Name is a short backend label for `shado doctor`.
	Name() string
	// Ready reports whether the COW backend itself is usable, with a human
	// detail line (cmdlets present / volume type / etc.).
	Ready() (bool, string)
	// Privileged reports whether the current process holds the rights the
	// backend needs (admin on Windows; always true on macOS — no elevation).
	Privileged() bool
	// RequireReady aborts (fail) when a privileged COW op cannot run.
	RequireReady()

	// BasePath is where project name's frozen base lives on disk.
	BasePath(name string) string
	// CreateBase freezes the warm folder into p's read-only base, setting
	// p.BaseVhdx and p.SizeGB. sizeGB is an advisory max (0 = auto-size); it is
	// only meaningful to image-based backends.
	CreateBase(p *Project, warm string, sizeGB float64) error
	// CreateShadow makes a writable COW view of p's base for id.
	CreateShadow(p *Project, id string, main bool) (Shadow, error)
	// RemoveShadow tears a shadow's COW storage + mount down.
	RemoveShadow(s *Shadow) error
	// ParkShadow detaches a shadow's mount but keeps its COW storage.
	ParkShadow(s *Shadow) error
	// ResumeShadow re-attaches a parked shadow.
	ResumeShadow(s *Shadow) error
	// Recache promotes the warmed main shadow into a fresh frozen base: it
	// rebuilds + freezes the base from main, tears down EVERY shadow, removes
	// the old base, and points p.BaseVhdx at the new one. The caller recreates
	// the shadows afterward.
	Recache(p *Project, main *Shadow) error
	// ExportBase copies the base's contents out to dest (restore).
	ExportBase(p *Project, dest string) error
	// DestroyBase removes the base entirely (restore).
	DestroyBase(p *Project) error

	// DiskUsage is the on-disk footprint of an arbitrary base/store path.
	DiskUsage(path string) int64
	// ShadowDiskUsage is a shadow's absolute COW footprint, used for dirty
	// detection against Shadow.CleanSize.
	ShadowDiskUsage(s *Shadow) int64
	// ShadowReportedSize is the incremental size shown by ls/du: what the
	// shadow itself has written beyond its clean baseline.
	ShadowReportedSize(s *Shadow) int64
}

// backend is the active COW backend for this OS, chosen at package init.
var backend = newBackend()

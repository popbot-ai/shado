//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMacBackendLifecycle exercises the real APFS clonefile backend end-to-end:
// a frozen base, isolated writable shadows, dirty detection, and recache.
func TestMacBackendLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHADO_HOME", home)
	// frozen bases are read-only; make the tree writable again so t.TempDir's
	// RemoveAll cleanup can unlink it (runs before TempDir's own cleanup).
	t.Cleanup(func() { _ = unfreezeTree(home) })
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}

	warm := filepath.Join(home, "warm")
	if err := os.MkdirAll(filepath.Join(warm, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(warm, "sub", "a.txt"), []byte("warm"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := macBackend{}
	p := &Project{Name: "g", OriginalFolder: warm, ShadowsRoot: filepath.Join(home, "shadows", "g")}
	if err := b.CreateBase(p, warm, 0); err != nil {
		t.Fatalf("CreateBase: %v", err)
	}
	if !fileExists(p.BaseVhdx) {
		t.Fatal("base not created")
	}

	s1, err := b.CreateShadow(p, "1", false)
	if err != nil {
		t.Fatalf("CreateShadow 1: %v", err)
	}
	s2, err := b.CreateShadow(p, "2", false)
	if err != nil {
		t.Fatalf("CreateShadow 2: %v", err)
	}

	// fresh shadow inherits warm content and is writable.
	if got, _ := os.ReadFile(filepath.Join(s1.Mount, "sub", "a.txt")); string(got) != "warm" {
		t.Fatalf("shadow 1 content = %q, want warm", got)
	}
	if err := os.WriteFile(filepath.Join(s1.Mount, "sub", "a.txt"), []byte("changed-in-1"), 0o644); err != nil {
		t.Fatalf("write shadow 1: %v", err)
	}

	// isolation: shadow 2 must not see shadow 1's write.
	if got, _ := os.ReadFile(filepath.Join(s2.Mount, "sub", "a.txt")); string(got) != "warm" {
		t.Fatalf("shadow 2 leaked shadow 1's write: %q", got)
	}

	// dirty detection: small in-place edit is under the margin; a >margin file trips it.
	if shadowDirty(&s1) {
		t.Error("shadow 1 should not be dirty after a tiny edit")
	}
	big := make([]byte, dirtyMarginBytes+(4<<20))
	if err := os.WriteFile(filepath.Join(s1.Mount, "big.bin"), big, 0o644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	if !shadowDirty(&s1) {
		t.Error("shadow 1 should be dirty after writing past the margin")
	}
	if b.ShadowReportedSize(&s1) <= 0 {
		t.Error("reported size should reflect the written bytes")
	}

	// recache: promote a freshly warmed main into the base, then shadows inherit it.
	main, err := b.CreateShadow(p, "main", true)
	if err != nil {
		t.Fatalf("CreateShadow main: %v", err)
	}
	p.Shadows = []Shadow{main, s1, s2}
	if err := os.WriteFile(filepath.Join(main.Mount, "warmed.txt"), []byte("warm2"), 0o644); err != nil {
		t.Fatalf("warm main: %v", err)
	}
	if err := b.Recache(p, &p.Shadows[0]); err != nil {
		t.Fatalf("Recache: %v", err)
	}
	ns, err := b.CreateShadow(p, "1", false)
	if err != nil {
		t.Fatalf("CreateShadow after recache: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(ns.Mount, "warmed.txt")); string(got) != "warm2" {
		t.Fatalf("recached base missing warmed file: %q", got)
	}
}

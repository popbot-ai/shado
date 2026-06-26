package main

import (
	"strings"
	"testing"
)

func TestSlotIDs(t *testing.T) {
	if got := slotIDs(0); len(got) != 0 {
		t.Errorf("slotIDs(0) = %v", got)
	}
	if got := strings.Join(slotIDs(3), ","); got != "1,2,3" {
		t.Errorf("slotIDs(3) = %q", got)
	}
}

func TestParkedTag(t *testing.T) {
	if parkedTag(Shadow{Parked: true}) != " [parked]" {
		t.Error("parked tag")
	}
	if parkedTag(Shadow{}) != "" {
		t.Error("unparked tag should be empty")
	}
}

func TestShadowPaths(t *testing.T) {
	t.Setenv("SHADO_HOME", `C:\sh`)
	p := &Project{Name: "g", ShadowsRoot: `C:\sh\shadows\g`}
	if !strings.HasSuffix(shadowVhdxPath(p, "1"), `g-1.vhdx`) {
		t.Errorf("vhdx path = %s", shadowVhdxPath(p, "1"))
	}
	if !strings.HasSuffix(shadowMountPath(p, "1"), `1`) {
		t.Errorf("mount path = %s", shadowMountPath(p, "1"))
	}
}

func TestShadowDirty(t *testing.T) {
	// a clone whose vhdx is missing reports size 0; with CleanSize 0 it's not dirty
	s := &Shadow{Vhdx: `Z:\does\not\exist.vhdx`, CleanSize: 0}
	if shadowDirty(s) {
		t.Error("missing/zero-size clone should not be dirty")
	}
}

package main

import (
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	t.Setenv("SHADO_HOME", t.TempDir())
	if err := ensureHome(); err != nil {
		t.Fatal(err)
	}
	r := &Registry{Projects: []Project{{
		Name:        "game",
		BaseVhdx:    "b.vhdx",
		ShadowsRoot: "root",
		Shadows: []Shadow{
			{ID: "main", Mount: "m", Vhdx: "v0", Main: true},
			{ID: "1", Mount: "m1", Vhdx: "v1", CleanSize: 123},
		},
	}}}
	if err := saveReg(r); err != nil {
		t.Fatal(err)
	}
	got, err := loadReg()
	if err != nil {
		t.Fatal(err)
	}
	p := got.project("game")
	if p == nil {
		t.Fatal("project lookup nil")
	}
	if p.mainShadow() == nil || p.mainShadow().ID != "main" {
		t.Error("mainShadow")
	}
	if s := p.shadow("1"); s == nil || s.CleanSize != 123 {
		t.Errorf("shadow lookup: %+v", s)
	}
}

func TestResolveProject(t *testing.T) {
	r := &Registry{Projects: []Project{{Name: "only"}}}
	if r.resolveProject("") == nil {
		t.Error("sole project should resolve on empty name")
	}
	r.Projects = append(r.Projects, Project{Name: "two"})
	if r.resolveProject("") != nil {
		t.Error("ambiguous (2 projects) should not resolve on empty name")
	}
	if r.resolveProject("two") == nil {
		t.Error("named lookup failed")
	}
	if r.resolveProject("nope") != nil {
		t.Error("unknown name should be nil")
	}
}

func TestRemoveShadowAndProject(t *testing.T) {
	p := &Project{Shadows: []Shadow{{ID: "main", Main: true}, {ID: "1"}, {ID: "2"}}}
	p.removeShadow("1")
	if p.shadow("1") != nil || len(p.Shadows) != 2 {
		t.Errorf("removeShadow left %d shadows", len(p.Shadows))
	}
	r := &Registry{Projects: []Project{{Name: "a"}, {Name: "b"}}}
	r.removeProject("a")
	if r.project("a") != nil || len(r.Projects) != 1 {
		t.Errorf("removeProject left %d projects", len(r.Projects))
	}
}

func TestLoadRegMissing(t *testing.T) {
	t.Setenv("SHADO_HOME", filepath.Join(t.TempDir(), "does-not-exist"))
	r, err := loadReg()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Projects) != 0 {
		t.Errorf("expected empty registry, got %d projects", len(r.Projects))
	}
}

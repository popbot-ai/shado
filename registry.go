package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Shadow is one writable copy-on-write view of a base, surfaced as a folder.
type Shadow struct {
	ID        string `json:"id"`        // "main", "1", "2", ...
	Mount     string `json:"mount"`     // folder mount point
	Vhdx      string `json:"vhdx"`      // differencing child path (Windows backend)
	CleanSize int64  `json:"cleanSize"` // child size right after create/reset (dirty baseline)
	Main      bool   `json:"main,omitempty"`
	Parked    bool   `json:"parked,omitempty"`
}

// Project is one frozen base plus its shadows.
type Project struct {
	Name           string   `json:"name"`
	OriginalFolder string   `json:"originalFolder"` // the warm folder given to create (for restore)
	BaseVhdx       string   `json:"baseVhdx"`       // frozen, read-only
	SizeGB         float64  `json:"sizeGb"`
	ShadowsRoot    string   `json:"shadowsRoot"`
	Shadows        []Shadow `json:"shadows"`
}

type Registry struct {
	Projects []Project `json:"projects"`
}

func shadoHome() string {
	if h := os.Getenv("SHADO_HOME"); h != "" {
		return h
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "shado")
}

func storeDir() string { return filepath.Join(shadoHome(), "store") }
func regPath() string  { return filepath.Join(shadoHome(), "registry.json") }
func ensureHome() error {
	return os.MkdirAll(storeDir(), 0o755)
}

func loadReg() (*Registry, error) {
	b, err := os.ReadFile(regPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{}, nil
		}
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func saveReg(r *Registry) error {
	if err := ensureHome(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(regPath(), b, 0o644)
}

func (r *Registry) project(name string) *Project {
	for i := range r.Projects {
		if r.Projects[i].Name == name {
			return &r.Projects[i]
		}
	}
	return nil
}

// resolveProject returns the named project, or the sole project when name is empty.
func (r *Registry) resolveProject(name string) *Project {
	if name != "" {
		return r.project(name)
	}
	if len(r.Projects) == 1 {
		return &r.Projects[0]
	}
	return nil
}

func (r *Registry) removeProject(name string) {
	out := r.Projects[:0]
	for _, p := range r.Projects {
		if p.Name != name {
			out = append(out, p)
		}
	}
	r.Projects = out
}

func (p *Project) shadow(id string) *Shadow {
	for i := range p.Shadows {
		if p.Shadows[i].ID == id {
			return &p.Shadows[i]
		}
	}
	return nil
}

func (p *Project) mainShadow() *Shadow {
	for i := range p.Shadows {
		if p.Shadows[i].Main {
			return &p.Shadows[i]
		}
	}
	return nil
}

func (p *Project) removeShadow(id string) {
	out := p.Shadows[:0]
	for _, s := range p.Shadows {
		if s.ID != id {
			out = append(out, s)
		}
	}
	p.Shadows = out
}

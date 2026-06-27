package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const dirtyMarginBytes = 16 * 1024 * 1024 // delta growth beyond this = "has changes"

// ---- helpers shared by commands ----

func resolve(reg *Registry, f flags) *Project {
	p := reg.resolveProject(f.str("name"))
	if p == nil {
		if f.has("name") {
			fail("no project %q", f.str("name"))
		}
		fail("which project? pass --name (or create one first)")
	}
	return p
}

func shadowDirty(s *Shadow) bool {
	return backend.ShadowDiskUsage(s) > s.CleanSize+dirtyMarginBytes
}

func shadowVhdxPath(p *Project, id string) string {
	return filepath.Join(storeDir(), fmt.Sprintf("%s-%s.vhdx", p.Name, id))
}
func shadowMountPath(p *Project, id string) string {
	return filepath.Join(p.ShadowsRoot, id)
}

// ============================ commands ============================

func cmdDoctor() {
	fmt.Println("shado doctor")
	ready := true
	fmt.Printf("  %-22s %s\n", "backend:", backend.Name())
	priv := backend.Privileged()
	fmt.Printf("  %-22s %s\n", "privileges:", yn(priv, "yes", "NO  - privileged ops will fail"))
	if !priv {
		ready = false
	}
	bok, detail := backend.Ready()
	fmt.Printf("  %-22s %s\n", "COW backend:", yn(bok, "yes ("+detail+")", "NO  - "+detail))
	if !bok {
		ready = false
	}
	fmt.Printf("  %-22s %s\n", "SHADO_HOME:", shadoHome())
	fmt.Println()
	fmt.Println(yn(ready, "READY", "NOT READY - resolve the items above."))
}

func cmdCreate(f flags, pos []string) {
	backend.RequireReady()
	must(ensureHome())
	if len(pos) < 1 {
		fail("usage: shado create <warm-folder> --name <n> [--count C] [--size-gb G] [--no-main]")
	}
	warm := pos[0]
	if !fileExists(warm) {
		fail("warm folder not found: %s", warm)
	}
	name := f.need("name")
	count := f.intv("count", 1)
	reg := mustReg()
	if reg.project(name) != nil {
		fail("project %q already exists", name)
	}

	shadowsRoot := f.str("shadows-root")
	if shadowsRoot == "" {
		shadowsRoot = filepath.Join(shadoHome(), "shadows", name)
	}
	p := &Project{Name: name, OriginalFolder: warm, ShadowsRoot: shadowsRoot}
	must(backend.CreateBase(p, warm, f.float("size-gb", 0)))

	// --no-main: skip the default primary "main" clone. Callers that drive
	// their own slot clones (e.g. PopBot, which uses the original folder as the
	// live workspace) don't want an unused main shadow eating disk.
	var ids []string
	if !f.has("no-main") {
		ids = append(ids, "main")
	}
	ids = append(ids, slotIDs(count)...)
	for _, id := range ids {
		info("creating shadow %q", id)
		s, err := backend.CreateShadow(p, id, id == "main", "")
		must(err)
		p.Shadows = append(p.Shadows, s)
	}
	reg.Projects = append(reg.Projects, *p)
	must(saveReg(reg))
	ok("project %q ready: base + %d shadow(s) under %s", name, len(ids), shadowsRoot)
	cmdLs()
}

func slotIDs(count int) []string {
	var ids []string
	for i := 1; i <= count; i++ {
		ids = append(ids, fmt.Sprintf("%d", i))
	}
	return ids
}

func cmdRecache(f flags) {
	backend.RequireReady()
	reg := mustReg()
	p := resolve(reg, f)
	main := p.mainShadow()
	if main == nil {
		fail("project %q has no main shadow", p.Name)
	}
	// guard: any non-base (non-main) shadow with changes blocks recache unless --force
	if !f.has("force") {
		var dirty []string
		for i := range p.Shadows {
			s := &p.Shadows[i]
			if !s.Main && shadowDirty(s) {
				dirty = append(dirty, s.ID)
			}
		}
		if len(dirty) > 0 {
			fail("shadows have uncommitted changes: %s\n  commit/shelve them via your VCS, then retry, or pass --force to discard", strings.Join(dirty, ", "))
		}
	}

	// backend promotes the warmed main into a fresh frozen base and tears down
	// every shadow; we then recreate each one off the new base.
	must(backend.Recache(p, main))

	old := p.Shadows
	p.Shadows = nil
	for _, s := range old {
		info("recreating shadow %q", s.ID)
		ns, err := backend.CreateShadow(p, s.ID, s.Main, s.Mount)
		must(err)
		p.Shadows = append(p.Shadows, ns)
	}
	must(saveReg(reg))
	ok("recached %q: new base from main, %d shadow(s) reset", p.Name, len(p.Shadows))
}

func cmdRestore(f flags) {
	backend.RequireReady()
	reg := mustReg()
	p := resolve(reg, f)
	if !f.has("force") {
		for i := range p.Shadows {
			if !p.Shadows[i].Main && shadowDirty(&p.Shadows[i]) {
				fail("shadow %q has changes; commit/shelve first or pass --force", p.Shadows[i].ID)
			}
		}
	}
	// remove shadows
	for i := range p.Shadows {
		_ = backend.RemoveShadow(&p.Shadows[i])
	}
	// copy base contents back to the original folder
	dest := nz(f.str("to"), p.OriginalFolder)
	if dest != "" {
		info("restoring base contents to %s ...", dest)
		if err := backend.ExportBase(p, dest); err != nil {
			fmt.Printf("  could not copy base out: %v\n", err)
		}
	}
	_ = backend.DestroyBase(p)
	_ = os.RemoveAll(p.ShadowsRoot)
	reg.removeProject(p.Name)
	must(saveReg(reg))
	ok("restored %q back to normal disk (%s)", p.Name, nz(dest, "base removed"))
}

// ---- clone family ----

func cmdCloneCreate(f flags) {
	backend.RequireReady()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	if p.shadow(slot) != nil {
		fail("slot %s already exists - use 'shado clone reset' to refresh it", slot)
	}
	info("adding shadow %q off base", slot)
	s, err := backend.CreateShadow(p, slot, false, f.str("mount"))
	must(err)
	p.Shadows = append(p.Shadows, s)
	must(saveReg(reg))
	runHook(f.str("hook"), s.Mount)
	ok("shadow %s ready at %s", slot, s.Mount)
}

func cmdCloneReset(f flags) {
	backend.RequireReady()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	s := p.shadow(slot)
	if s == nil {
		fail("no shadow in slot %s", slot)
	}
	main := s.Main
	info("resetting shadow %q to clean warm base", slot)
	_ = backend.RemoveShadow(s)
	ns, err := backend.CreateShadow(p, slot, main, s.Mount)
	must(err)
	*s = ns
	must(saveReg(reg))
	runHook(f.str("hook"), ns.Mount)
	ok("shadow %s reset (clean + warm) at %s", slot, ns.Mount)
}

func cmdClonePark(f flags) {
	backend.RequireReady()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	s := p.shadow(slot)
	if s == nil {
		fail("no shadow in slot %s", slot)
	}
	must(backend.ParkShadow(s))
	s.Parked = true
	must(saveReg(reg))
	ok("shadow %s parked (diff kept, unmounted)", slot)
}

func cmdCloneResume(f flags) {
	backend.RequireReady()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	s := p.shadow(slot)
	if s == nil {
		fail("no shadow in slot %s", slot)
	}
	must(backend.ResumeShadow(s))
	s.Parked = false
	must(saveReg(reg))
	runHook(f.str("hook"), s.Mount)
	ok("shadow %s resumed at %s", slot, s.Mount)
}

func cmdCloneRm(f flags) {
	backend.RequireReady()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	s := p.shadow(slot)
	if s == nil {
		fail("no shadow in slot %s", slot)
	}
	if s.Main {
		fail("refusing to remove the main shadow (use 'shado restore' to tear down the project)")
	}
	if !f.has("force") && shadowDirty(s) {
		fail("shadow %s has changes; commit/shelve first or pass --force", slot)
	}
	_ = backend.RemoveShadow(s)
	p.removeShadow(slot)
	must(saveReg(reg))
	ok("shadow %s removed", slot)
}

// cmdRemount re-attaches EVERY shadow of a project. Windows VHDX mounts don't
// survive a reboot, so after a restart the registry still lists the clones but
// their mount folders are empty. This brings them all back in one elevated
// call (idempotent — ResumeShadow is a no-op for an already-mounted clone).
// Per-shadow failures are reported but don't abort the rest.
func cmdRemount(f flags) {
	backend.RequireReady()
	reg := mustReg()
	p := resolve(reg, f)
	mounted, failed := 0, 0
	for i := range p.Shadows {
		s := &p.Shadows[i]
		if err := backend.ResumeShadow(s); err != nil {
			fmt.Printf("  slot %s: %v\n", s.ID, err)
			failed++
			continue
		}
		s.Parked = false
		mounted++
	}
	must(saveReg(reg))
	ok("remounted %d shadow(s) for %q (%d failed)", mounted, p.Name, failed)
}

// ---- inspection ----

func cmdLs() {
	reg := mustReg()
	if len(reg.Projects) == 0 {
		fmt.Println("(no projects)")
		return
	}
	for _, p := range reg.Projects {
		fmt.Printf("PROJECT %s   base=%s (%s)\n", p.Name, humanBytes(backend.DiskUsage(p.BaseVhdx)), p.BaseVhdx)
		for i := range p.Shadows {
			s := &p.Shadows[i]
			tag := ""
			if s.Main {
				tag = " [main]"
			}
			if s.Parked {
				tag += " [parked]"
			}
			dirty := ""
			if shadowDirty(s) {
				dirty = " *dirty*"
			}
			fmt.Printf("  %-6s mount=%-28s diff=%-9s%s%s\n", s.ID, s.Mount, humanBytes(backend.ShadowReportedSize(s)), tag, dirty)
		}
	}
}

// cmdDu reports on-disk usage: the shared read-only base, the main clone, and the
// slot clones (each a thin differencing delta), with subtotals and a grand total.
func cmdDu(f flags) {
	reg := mustReg()
	projects := reg.Projects
	if f.has("name") {
		p := reg.project(f.str("name"))
		if p == nil {
			fail("no project %q", f.str("name"))
		}
		projects = []Project{*p}
	}
	if len(projects) == 0 {
		fmt.Println("(no projects)")
		return
	}
	var grand int64
	for _, p := range projects {
		fmt.Printf("PROJECT %s\n", p.Name)
		baseSz := backend.DiskUsage(p.BaseVhdx)
		fmt.Printf("  %-16s %12s   (shared, read-only)\n", "base", humanBytes(baseSz))

		var mainSz, slotSz int64
		var slots int
		for i := range p.Shadows {
			s := &p.Shadows[i]
			if !s.Main {
				continue
			}
			sz := backend.ShadowReportedSize(s)
			mainSz += sz
			fmt.Printf("  %-16s %12s%s\n", "main clone", humanBytes(sz), parkedTag(*s))
		}
		for i := range p.Shadows {
			s := &p.Shadows[i]
			if s.Main {
				continue
			}
			sz := backend.ShadowReportedSize(s)
			slotSz += sz
			slots++
			fmt.Printf("  %-16s %12s%s\n", "slot "+s.ID, humanBytes(sz), parkedTag(*s))
		}
		fmt.Println("  " + strings.Repeat("-", 30))
		fmt.Printf("  %-16s %12s   (%d clone(s))\n", "slot clones", humanBytes(slotSz), slots)
		total := baseSz + mainSz + slotSz
		grand += total
		fmt.Printf("  %-16s %12s\n\n", "project total", humanBytes(total))
	}
	if len(projects) > 1 {
		fmt.Printf("GRAND TOTAL %s\n", humanBytes(grand))
	}
}

func parkedTag(s Shadow) string {
	if s.Parked {
		return " [parked]"
	}
	return ""
}

func cmdJSON() {
	reg := mustReg()
	b, _ := jsonIndent(reg)
	fmt.Println(string(b))
}

func cmdVersion() {
	v := version
	if commit != "" {
		v += " (" + commit
		if date != "" {
			v += ", " + date
		}
		v += ")"
	}
	fmt.Println("shado", v)
}

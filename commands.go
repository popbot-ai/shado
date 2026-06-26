package main

import (
	"fmt"
	"math"
	"os"
	"os/exec"
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
	return vhdxSize(s.Vhdx) > s.CleanSize+dirtyMarginBytes
}

func shadowVhdxPath(p *Project, id string) string {
	return filepath.Join(storeDir(), fmt.Sprintf("%s-%s.vhdx", p.Name, id))
}
func shadowMountPath(p *Project, id string) string {
	return filepath.Join(p.ShadowsRoot, id)
}

// createOneShadow makes a differencing child + folder mount and returns the record.
func createOneShadow(p *Project, id string, main bool) (Shadow, error) {
	vhdx := shadowVhdxPath(p, id)
	mount := shadowMountPath(p, id)
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

func runHook(hook, mount string) {
	if hook == "" {
		return
	}
	info("  running hook: %s", hook)
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", hook)
	cmd.Env = append(os.Environ(), "SHADO_MOUNT="+mount)
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("  hook failed: %v\n%s\n", err, string(out))
	} else if verbose && len(out) > 0 {
		os.Stdout.Write(out)
	}
}

// ============================ commands ============================

func cmdDoctor() {
	fmt.Println("shado doctor")
	ready := true
	admin := isAdmin()
	fmt.Printf("  %-22s %s\n", "elevated (admin):", yn(admin, "yes", "NO  - privileged ops will fail"))
	if !admin {
		ready = false
	}
	hv := hyperVAvailable()
	fmt.Printf("  %-22s %s\n", "VHDX backend:", yn(hv, "yes (Hyper-V cmdlets)", "NO  - enable Microsoft-Hyper-V-All + reboot"))
	if !hv {
		ready = false
	}
	fmt.Printf("  %-22s %s\n", "SHADO_HOME:", shadoHome())
	fmt.Println()
	fmt.Println(yn(ready, "READY", "NOT READY - resolve the items above."))
}

func cmdCreate(f flags, pos []string) {
	requireAdmin()
	must(ensureHome())
	if len(pos) < 1 {
		fail("usage: shado create <warm-folder> --name <n> [--count C] [--size-gb G]")
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

	sizeGB := f.float("size-gb", 0)
	if sizeGB == 0 {
		sizeGB = float64(dirSizeBytes(warm))/float64(1<<30)*1.4 + 2 // content * 1.4 + headroom
	}
	sizeGB = math.Ceil(sizeGB)             // whole GB => MB-aligned, valid for New-VHD
	sizeBytes := int64(sizeGB) * (1 << 30) // virtual max; dynamic VHDX only allocates what's used
	baseVhdx := filepath.Join(storeDir(), name+"-base.vhdx")
	if fileExists(baseVhdx) {
		fail("%s already on disk", baseVhdx)
	}

	info("creating base for %q (%.0f GB max) from %s", name, sizeGB, warm)
	baseBuild := filepath.Join(storeDir(), "_build-"+name)
	must(vhdxCreateBase(baseVhdx, sizeBytes, "shado-"+name, baseBuild))
	info("copying warm folder into base ...")
	must(robocopyMirror(warm, baseBuild))
	info("freezing base read-only")
	must(vhdxFreeze(baseVhdx))
	_ = os.RemoveAll(baseBuild)

	shadowsRoot := f.str("shadows-root")
	if shadowsRoot == "" {
		shadowsRoot = filepath.Join(shadoHome(), "shadows", name)
	}
	p := &Project{Name: name, OriginalFolder: warm, BaseVhdx: baseVhdx, SizeGB: sizeGB, ShadowsRoot: shadowsRoot}

	ids := append([]string{"main"}, slotIDs(count)...)
	for i, id := range ids {
		info("creating shadow %q", id)
		s, err := createOneShadow(p, id, i == 0)
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
	requireAdmin()
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

	info("promoting warmed main into a new base (flatten) ...")
	newBase := filepath.Join(storeDir(), p.Name+"-base.new.vhdx")
	_ = vhdxDismount(main.Vhdx)
	_ = os.Remove(newBase)
	must(vhdxConvert(main.Vhdx, newBase))
	must(vhdxFreeze(newBase))

	// tear down all shadows + old base
	for i := range p.Shadows {
		_ = vhdxDismount(p.Shadows[i].Vhdx)
		_ = os.Remove(p.Shadows[i].Vhdx)
	}
	_ = vhdxUnfreeze(p.BaseVhdx)
	oldBase := p.BaseVhdx
	_ = os.Remove(oldBase)

	// swap base, recreate every shadow fresh
	finalBase := filepath.Join(storeDir(), p.Name+"-base.vhdx")
	must(os.Rename(newBase, finalBase))
	p.BaseVhdx = finalBase

	old := p.Shadows
	p.Shadows = nil
	for _, s := range old {
		info("recreating shadow %q", s.ID)
		ns, err := createOneShadow(p, s.ID, s.Main)
		must(err)
		p.Shadows = append(p.Shadows, ns)
	}
	must(saveReg(reg))
	ok("recached %q: new base from main, %d shadow(s) reset", p.Name, len(p.Shadows))
}

func cmdRestore(f flags) {
	requireAdmin()
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
		_ = vhdxDismount(p.Shadows[i].Vhdx)
		_ = os.Remove(p.Shadows[i].Vhdx)
		_ = os.RemoveAll(p.Shadows[i].Mount)
	}
	// copy base contents back to the original folder
	dest := nz(f.str("to"), p.OriginalFolder)
	if dest != "" {
		info("restoring base contents to %s ...", dest)
		_ = vhdxUnfreeze(p.BaseVhdx)
		tmp := filepath.Join(storeDir(), "_restore-"+p.Name)
		if err := vhdxMountFolder(p.BaseVhdx, tmp); err == nil {
			_ = robocopyMirror(tmp, dest)
			_ = vhdxDismount(p.BaseVhdx)
			_ = os.RemoveAll(tmp)
		} else {
			fmt.Printf("  could not remount base to copy out: %v\n", err)
		}
	}
	_ = vhdxUnfreeze(p.BaseVhdx)
	_ = os.Remove(p.BaseVhdx)
	_ = os.RemoveAll(p.ShadowsRoot)
	reg.removeProject(p.Name)
	must(saveReg(reg))
	ok("restored %q back to normal disk (%s)", p.Name, nz(dest, "base removed"))
}

// ---- clone family ----

func cmdCloneCreate(f flags) {
	requireAdmin()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	if p.shadow(slot) != nil {
		fail("slot %s already exists - use 'shado clone reset' to refresh it", slot)
	}
	info("adding shadow %q off base", slot)
	s, err := createOneShadow(p, slot, false)
	must(err)
	p.Shadows = append(p.Shadows, s)
	must(saveReg(reg))
	runHook(f.str("hook"), s.Mount)
	ok("shadow %s ready at %s", slot, s.Mount)
}

func cmdCloneReset(f flags) {
	requireAdmin()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	s := p.shadow(slot)
	if s == nil {
		fail("no shadow in slot %s", slot)
	}
	main := s.Main
	info("resetting shadow %q to clean warm base", slot)
	_ = vhdxDismount(s.Vhdx)
	_ = os.Remove(s.Vhdx)
	ns, err := createOneShadow(p, slot, main)
	must(err)
	*s = ns
	must(saveReg(reg))
	runHook(f.str("hook"), ns.Mount)
	ok("shadow %s reset (clean + warm) at %s", slot, ns.Mount)
}

func cmdClonePark(f flags) {
	requireAdmin()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	s := p.shadow(slot)
	if s == nil {
		fail("no shadow in slot %s", slot)
	}
	must(vhdxDismount(s.Vhdx))
	s.Parked = true
	must(saveReg(reg))
	ok("shadow %s parked (diff kept, unmounted)", slot)
}

func cmdCloneResume(f flags) {
	requireAdmin()
	reg := mustReg()
	p := resolve(reg, f)
	slot := f.need("slot")
	s := p.shadow(slot)
	if s == nil {
		fail("no shadow in slot %s", slot)
	}
	must(vhdxMountFolder(s.Vhdx, s.Mount))
	s.Parked = false
	must(saveReg(reg))
	runHook(f.str("hook"), s.Mount)
	ok("shadow %s resumed at %s", slot, s.Mount)
}

func cmdCloneRm(f flags) {
	requireAdmin()
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
	_ = vhdxDismount(s.Vhdx)
	_ = os.Remove(s.Vhdx)
	_ = os.RemoveAll(s.Mount)
	p.removeShadow(slot)
	must(saveReg(reg))
	ok("shadow %s removed", slot)
}

// ---- inspection ----

func cmdLs() {
	reg := mustReg()
	if len(reg.Projects) == 0 {
		fmt.Println("(no projects)")
		return
	}
	for _, p := range reg.Projects {
		fmt.Printf("PROJECT %s   base=%s (%s)\n", p.Name, sizeOf(p.BaseVhdx), p.BaseVhdx)
		for _, s := range p.Shadows {
			tag := ""
			if s.Main {
				tag = " [main]"
			}
			if s.Parked {
				tag += " [parked]"
			}
			dirty := ""
			if shadowDirty(&s) {
				dirty = " *dirty*"
			}
			fmt.Printf("  %-6s mount=%-28s diff=%-9s%s%s\n", s.ID, s.Mount, sizeOf(s.Vhdx), tag, dirty)
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
		baseSz := vhdxSize(p.BaseVhdx)
		fmt.Printf("  %-16s %12s   (shared, read-only)\n", "base", humanBytes(baseSz))

		var mainSz, slotSz int64
		var slots int
		for _, s := range p.Shadows {
			if !s.Main {
				continue
			}
			mainSz += vhdxSize(s.Vhdx)
			fmt.Printf("  %-16s %12s%s\n", "main clone", humanBytes(vhdxSize(s.Vhdx)), parkedTag(s))
		}
		for _, s := range p.Shadows {
			if s.Main {
				continue
			}
			sz := vhdxSize(s.Vhdx)
			slotSz += sz
			slots++
			fmt.Printf("  %-16s %12s%s\n", "slot "+s.ID, humanBytes(sz), parkedTag(s))
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

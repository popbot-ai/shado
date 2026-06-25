package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// build-time vars (set via -ldflags by GoReleaser)
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// verbose echoes child-process (powershell/robocopy/hook) output inline with
// shado's own output. Off by default (children run windowless and silent);
// enabled by --verbose or SHADO_VERBOSE=1.
var verbose bool

// ---- flag parsing: `--key value` / `--flag` (bool) + positional args ----
type flags map[string]string

func parseFlags(args []string) (flags, []string) {
	f := flags{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			key := strings.TrimPrefix(a, "--")
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				f[key] = args[i+1]
				i++
			} else {
				f[key] = "true"
			}
		} else {
			pos = append(pos, a)
		}
	}
	return f, pos
}

func (f flags) has(k string) bool { _, ok := f[k]; return ok }
func (f flags) str(k string) string { return f[k] }
func (f flags) need(k string) string {
	v, ok := f[k]
	if !ok || strings.TrimSpace(v) == "" {
		fail("missing required --%s", k)
	}
	return v
}
func (f flags) float(k string, def float64) float64 {
	if v, ok := f[k]; ok {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			return x
		}
	}
	return def
}
func (f flags) intv(k string, def int) int {
	if v, ok := f[k]; ok {
		if x, err := strconv.Atoi(v); err == nil {
			return x
		}
	}
	return def
}

// ---- output ----
func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", a...)
	os.Exit(1)
}
func info(format string, a ...any) { fmt.Printf(format+"\n", a...) }
func ok(format string, a ...any)   { fmt.Printf("OK "+format+"\n", a...) }
func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}
func mustReg() *Registry {
	r, err := loadReg()
	must(err)
	return r
}

// ---- misc helpers ----
func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func yn(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}
func nz(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
func trimLine(s string) string { return strings.TrimSpace(s) }
func jsonIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

func isAdmin() bool {
	out, err := runPS(`[bool](([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole('Administrators'))`)
	return err == nil && strings.Contains(strings.ToLower(out), "true")
}
func requireAdmin() {
	if !isAdmin() {
		fail("this operation needs an elevated shell (Run as administrator). See 'shado doctor'.")
	}
}

func dirSizeBytes(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total
}
func sizeOf(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return "(missing)"
	}
	return fmt.Sprintf("%.0f MB", float64(fi.Size())/(1024*1024))
}

func humanBytes(n int64) string {
	const unit = 1024.0
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	f := float64(n)
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

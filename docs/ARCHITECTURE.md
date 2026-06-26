# shado architecture

`shado` hands out **instant, writable, copy-on-write (COW) clones of a very
large folder**. You warm one tree once (sync + build + populate caches); shado
freezes it into a read-only **base** and serves any number of **shadow** folders
that each share the base's blocks and store only their own writes.

This document describes how the program is put together: the data model, the
platform-neutral command layer, the per-OS backends, and the invariants that
keep it correct. For user-facing usage see [`README.md`](../README.md) and
[`man shado`](shado.1).

---

## 1. Core idea

```
              base   (frozen, read-only — the warm tree, stored once)
                │
   ┌────────────┼────────────┬────────────┐
 main         slot 1       slot 2   …    slot N      shadows: writable COW folders,
(you warm)   (agent A)    (agent B)      (agent N)   ~tens of MB each, isolated, warm
```

- A **shadow** reads fall through to the base; writes land in the shadow. A
  fresh shadow shares every block with the base, so its cost tracks *what it
  writes*, not the size of the project.
- **`main`** is a conventional shadow (`Main: true`) that you re-warm; `recache`
  promotes it into a new base.
- shado does **COW + mount + lifecycle only**. It never runs `p4 sync`, `git`,
  cooks, or builds — "warm" means something different for every project. You
  produce a warm folder; project-specific wiring runs as an optional post-mount
  `--hook`, never inside the engine.

---

## 2. Module map

| File | Build tag | Responsibility |
|---|---|---|
| `main.go` | — | CLI entry: arg dispatch, `--verbose`, usage text |
| `commands.go` | — | Platform-neutral command implementations (the verbs) |
| `registry.go` | — | `Project` / `Shadow` / `Registry` model + JSON persistence + `SHADO_HOME` resolution |
| `util.go` | — | Flag parsing, output helpers (`fail`/`info`/`ok`/`must`), sizing helpers |
| `backend.go` | — | The `Backend` interface + `var backend = newBackend()` |
| `backend_windows.go` | `windows` | `winBackend`: differencing-VHDX backend + admin checks |
| `vhdx.go` | `windows` | Low-level VHDX/Hyper-V primitives (PowerShell + robocopy) |
| `backend_darwin.go` | `darwin` | `macBackend`: APFS `clonefile` (`cp -c`) backend |
| `backend_other.go` | `!windows && !darwin` | `otherBackend`: unsupported stub (Linux is planned) |
| `proc_windows.go` | `windows` | `hideConsole` (CREATE_NO_WINDOW) + PowerShell `runHook` |
| `proc_other.go` | `!windows` | no-op `hideConsole` + `/bin/sh` `runHook` |
| `*_test.go` | mixed | unit tests (`vhdx_test.go`/`backend_darwin_test.go` are OS-tagged) |

The dependency direction is strict and one-way:

```
main.go ─▶ commands.go ─▶ backend (interface)  ◀── winBackend / macBackend / otherBackend
                  │                                        │
                  └────────▶ registry.go ◀────────────────┘
```

`commands.go` never names a concrete backend or a platform primitive — it talks
only to the `Backend` interface and the registry. Every disk operation that
differs by OS lives behind `Backend`.

---

## 3. Data model (`registry.go`)

```go
type Shadow struct {
    ID        string // "main", "1", "2", ...
    Mount     string // folder where the shadow is surfaced (the working dir)
    Vhdx      string // backend storage handle (see note)
    CleanSize int64  // backend footprint right after create/reset — the dirty baseline
    Main      bool   // the re-warmable shadow that recache promotes
    Parked    bool   // detached/idle (Windows: unmounted; macOS: logical only)
}

type Project struct {
    Name           string
    OriginalFolder string   // the warm folder given to `create` (restore target)
    BaseVhdx       string   // the frozen base handle
    SizeGB         float64
    ShadowsRoot    string   // default parent for shadow mounts
    Shadows        []Shadow
}

type Registry struct { Projects []Project }
```

The whole registry is persisted as pretty-printed JSON at
`$SHADO_HOME/registry.json`. There is no daemon and no lock file: every command
is a short-lived process that loads the registry, mutates it, and saves it.

**Naming note — `BaseVhdx` / `Vhdx`.** The struct field names are historical
(the Windows backend came first) but are now **backend-neutral handles**:

| Field | Windows means | macOS means |
|---|---|---|
| `Project.BaseVhdx` | path to the frozen `*-base.vhdx` image | path to the frozen `*-base/` directory |
| `Shadow.Vhdx` | path to the differencing child `.vhdx` | the shadow directory (== `Mount`) |
| `Shadow.Mount` | folder the VHDX volume is surfaced at | the shadow directory (== `Vhdx`) |

The JSON keys are kept stable for registry compatibility; think of them as
"base handle" and "storage handle," not literally VHDX files.

### `SHADO_HOME`

`$SHADO_HOME` overrides the store root. Default per OS (`defaultShadoHome()`):

| OS | Default |
|---|---|
| Windows | `%ProgramData%\shado` (machine-wide; mounts visible to all sessions) |
| macOS | `~/Library/Application Support/shado` |
| Linux | `$XDG_DATA_HOME/shado` or `~/.local/share/shado` |

Layout: `$SHADO_HOME/store/` holds bases (+ build scratch), `$SHADO_HOME/registry.json`
is the registry, and shadows default to `$SHADO_HOME/shadows/<project>/<id>`
(overridable per-clone with `--mount`).

---

## 4. The `Backend` interface (`backend.go`)

`Backend` is the entire OS-specific surface. One implementation is selected at
startup via `var backend = newBackend()` (each OS file defines exactly one
`newBackend()` under mutually-exclusive build tags).

```go
type Backend interface {
    // identity & readiness (for `doctor` and privilege gating)
    Name() string
    Ready() (bool, string)      // is the COW mechanism usable? + human detail
    Privileged() bool           // does this process hold the rights it needs?
    RequireReady()              // fail() if a privileged op can't run

    // base lifecycle
    BasePath(name string) string
    CreateBase(p *Project, warm string, sizeGB float64) error  // fill + freeze; sets BaseVhdx, SizeGB
    ExportBase(p *Project, dest string) error                  // copy base contents out (restore)
    DestroyBase(p *Project) error

    // shadow lifecycle
    CreateShadow(p *Project, id string, main bool, mountOverride string) (Shadow, error)
    RemoveShadow(s *Shadow) error
    ParkShadow(s *Shadow) error
    ResumeShadow(s *Shadow) error

    // recache: promote warmed main into a fresh frozen base, tear down every
    // shadow, swap p.BaseVhdx — the caller then recreates shadows.
    Recache(p *Project, main *Shadow) error

    // sizing (dirty detection + ls/du reporting)
    DiskUsage(path string) int64           // footprint of a base/store path
    ShadowDiskUsage(s *Shadow) int64        // shadow's absolute footprint (for dirty)
    ShadowReportedSize(s *Shadow) int64     // incremental size shown by ls/du
}
```

### Backend contract / invariants

A correct backend must guarantee:

1. **Isolation.** A write in one shadow is never visible in another shadow or in
   the base.
2. **Frozen base.** After `CreateBase`/`Recache`, the base is read-only; shadows
   created from it inherit the full warm tree.
3. **Instant + cheap shadows.** `CreateShadow` shares the base's blocks; a fresh
   shadow's `ShadowReportedSize` is ~0.
4. **Dirty = growth past a margin.** `shadowDirty(s)` is
   `ShadowDiskUsage(s) > s.CleanSize + dirtyMarginBytes` (16 MiB). `CleanSize` is
   captured at create/reset time.
5. **`mountOverride`.** When non-empty, the shadow is surfaced at that exact path
   instead of `<ShadowsRoot>/<id>` (PopBot computes a per-slot worktree path and
   everything downstream uses it).

---

## 5. Platform backends

### 5.1 Windows — differencing VHDX (`backend_windows.go`, `vhdx.go`)

The proven, original backend. Uses Hyper-V / VirtDisk via PowerShell cmdlets
(`New-VHD`, `Mount-VHD`, `Convert-VHD`, …) plus `robocopy` for bulk copies.

- **base** = a dynamic VHDX, NTFS-formatted, filled by robocopy, then set
  read-only (`vhdxFreeze`).
- **shadow** = a *differencing child* VHDX (`New-VHD -Differencing -ParentPath`)
  mounted at a folder access path (no drive letter → no AutoPlay).
- **recache** = `Convert-VHD` flattens the warmed main + its parent chain into a
  new standalone base.
- **dirty/size** = the child `.vhdx` file size (a single `stat`; O(1)).
- Differencing children inherit the base's GPT/partition GUIDs, so a 2nd+
  simultaneous mount can land OFFLINE from a signature collision — `vhdxMountFolder`
  onlines it (Windows resignatures into the child) and strips stray drive letters.
- **Requires elevation** (mounts are privileged); `RequireReady()` checks admin.

### 5.2 macOS — APFS clonefile (`backend_darwin.go`)

- **base** = a directory clone of the warm folder (`cp -cRp`, which issues
  `clonefile(2)` per file — COW, no data copy), then frozen read-only
  (`chmod -R a-w`).
- **shadow** = `cp -cRp base shadow` (instant clone), then made writable
  (`chmod -R u+w`). The shadow folder **is** both the storage and the mount.
- **recache** = clone the warmed main into a new base dir and freeze it.
- **dirty/size** = `du -sk` of the shadow tree. On APFS this counts allocated
  blocks (shared clone blocks included), so it tracks *growth* from the clean
  baseline. `ShadowReportedSize` returns `du - CleanSize` (what the shadow added).
- **park/resume** are logical no-ops — there is no image to detach; the folder is
  always present. The `Parked` flag is still tracked for surface parity.
- **No elevation required** — everything lives under the user's `SHADO_HOME`.

> clonefile shares blocks only *within a single APFS volume*. The store, shadows,
> and ideally the warm folder should be on the same volume; a cross-volume
> warm→base copy is a one-time real copy (still correct, just not instant).

### 5.3 Linux / other — unsupported stub (`backend_other.go`)

Builds and runs `doctor`/`version`/`help`; every privileged op fails with a clear
message. Keeps the `ubuntu-latest` CI job green until the planned
btrfs/XFS reflink (`cp --reflink`) / overlayfs backend lands.

### Platform summary

| | Windows | macOS | Linux |
|---|---|---|---|
| Mechanism | differencing VHDX | APFS `clonefile` | reflink/overlayfs (planned) |
| Base | frozen VHDX image | frozen directory | — |
| Shadow | mounted child VHDX | cloned directory | — |
| Mount vs storage | separate (image ↔ folder) | same folder | — |
| Dirty cost | O(1) file stat | O(tree) `du` walk | — |
| Elevation | required | none | — |
| Park/resume | unmount / remount | logical no-op | — |

---

## 6. Command flows (`commands.go`)

Every mutating command follows the same skeleton: `backend.RequireReady()` →
load registry → mutate disk via `backend` → `saveReg`. Highlights:

- **`create <warm> --name N [--count C] [--size-gb G]`** — `CreateBase`, then
  `CreateShadow` for `main` + `slotIDs(C)`. Records the project.
- **`clone create --slot S [--mount PATH] [--hook CMD]`** — one new shadow off
  the base; `--mount` overrides its path; `--hook` runs post-mount.
- **`clone reset --slot S`** — `RemoveShadow` + `CreateShadow` (clean, warm),
  preserving the existing mount. Re-runs the hook.
- **`clone park / resume --slot S`** — toggle `Parked` (+ unmount/remount on
  Windows).
- **`clone rm --slot S [--force]`** — refuses the `main` shadow; refuses a dirty
  shadow without `--force`.
- **`recache --name N [--force]`** — guards against dirty non-main shadows, then
  `backend.Recache` (promote warmed main → new base, tear down shadows) and
  recreates every shadow off the new base.
- **`restore --name N [--to DIR] [--force]`** — remove shadows, `ExportBase` to
  the original folder (or `--to`), `DestroyBase`, drop the project.
- **`ls` / `du` / `json`** — inspection; sizes come from `DiskUsage` /
  `ShadowReportedSize`. **`doctor`** — `Name`/`Privileged`/`Ready` + `SHADO_HOME`.

### Example: `clone reset` end-to-end

```
cmdCloneReset
  backend.RequireReady()                 // admin (win) / no-op (mac)
  reg := loadReg(); s := project.shadow(slot)
  backend.RemoveShadow(s)                 // dismount+delete (win) / rm -rf (mac)
  backend.CreateShadow(p, slot, s.Main, s.Mount)   // fresh COW clone at same mount
  saveReg(reg)
  runHook(--hook, mount)                  // optional post-mount wiring
```

---

## 7. Cross-cutting concerns

- **Hooks (`runHook`, per-OS in `proc_*`).** Optional post-mount command with the
  shadow path exported as `SHADO_MOUNT`. PowerShell on Windows, `/bin/sh`
  elsewhere. Used for per-slot Perforce/git wiring.
- **Child process output.** Children run silent by default; `--verbose` /
  `SHADO_VERBOSE=1` echoes their output inline. On Windows `hideConsole` sets
  `CREATE_NO_WINDOW` so spawned `powershell`/`robocopy` don't flash windows when
  shado is launched by a GUI app.
- **Privilege gating.** A single `backend.RequireReady()` at the top of every
  mutating command; `doctor` reports the same readiness non-fatally.
- **No daemon, no locks.** State is the on-disk registry; commands are atomic
  enough for the single-operator model. (Concurrent invocations on one project
  are not currently coordinated — see limitations.)

---

## 8. Extending: adding a backend (e.g. Linux reflink)

1. Add `backend_linux.go` with `//go:build linux` and a `linuxBackend`
   implementing every `Backend` method; define `newBackend()` there.
2. Narrow `backend_other.go`'s tag to exclude `linux`
   (`//go:build !windows && !darwin && !linux`) so exactly one `newBackend()`
   exists per platform.
3. Reuse the macOS model closely: base = frozen reflinked dir
   (`cp --reflink=always`), shadow = reflinked clone, dirty via `du`. overlayfs
   is an alternative for a true union mount.
4. Add `backend_linux_test.go` (`//go:build linux`) mirroring
   `backend_darwin_test.go`'s lifecycle/isolation/dirty/recache assertions.
5. Flip the README/​man-page/​goreleaser status notes.

Because the command layer only sees `Backend`, no change to `commands.go` is
required.

---

## 9. Testing & CI

- **Unit/lifecycle tests** (`go test ./...`): registry round-trip, flag parsing,
  sizing, path logic, and a full macOS backend lifecycle
  (`backend_darwin_test.go`: base → isolated shadows → dirty → recache →
  `--mount` override). OS-specific tests are build-tagged so each platform
  compiles and runs only what applies.
- **CI** (`.github/workflows/ci.yml`): `go vet` + `go build` + `go test` on
  `ubuntu-latest` and `windows-latest`. (No macOS runner yet — the darwin
  lifecycle test runs locally; adding a `macos-latest` matrix entry would
  exercise it upstream.)
- **Windows integration tests** (`test/*.ps1`) create/mount real VHDX and need an
  elevated Hyper-V shell — not run in CI.

---

## 10. Known limitations / non-goals

- **macOS dirty/`du` is an O(tree) walk.** Correct, but on a 1 TB shadow it's a
  full scan rather than the O(1) file-stat the VHDX backend enjoys. A future
  optimization could diff an APFS snapshot.
- **macOS base creation scales with file count.** `cp -cRp` clones COW per file,
  so it walks the tree (cheap on bytes, not free on millions of inodes). A single
  recursive `clonefile(2)` on the top directory would be truly instant but needs
  a syscall binding rather than the dependency-free `cp` shell-out.
- **macOS park/resume don't reclaim space** (logical only).
- **No concurrency control.** One registry file, no locking; assumes a single
  operator/orchestrator per host.
- **shado never warms.** Producing the warm tree and any per-slot VCS wiring is
  the caller's job (via `--hook`).

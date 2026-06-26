# shado

**Instant copy-on-write workspaces for very large projects.**

`shado` makes 0.5–1 TB project trees — AAA game checkouts, Perforce depots, giant
monorepos — practical to work on in parallel. You point it at one already-**warm**
folder (synced, built, caches populated — whatever "ready" means for your project).
shado freezes that into a read-only **base**, then hands out any number of **shadow**
folders: writable copy-on-write views of the base.

Each shadow inherits the entire warm tree instantly and stores only *its own*
changes. So a fresh, build-ready workspace costs **seconds and tens of MB** instead
of a full re-sync and a cold rebuild.

```
              base   (frozen, read-only — the warm tree, stored once)
                │
   ┌────────────┼────────────┬────────────┐
 main         slot 1       slot 2   …    slot N      shadows: writable COW folders,
(you warm)   (agent A)    (agent B)      (agent N)   ~tens of MB each, isolated, warm
```

It's the storage substrate behind [PopBot](https://github.com/popbot-ai/popbot-ai)'s
warm-slot model — extended from git worktrees to projects far too big to copy.

## What shado does and doesn't do

shado does **copy-on-write + mount + lifecycle**. It **never warms anything** — it
doesn't run `p4 sync`, `git`, engine imports, cooks, or builds, because "warm" means
something different for every project. **You** produce a warm folder; shado clones it
cheaply, tells you what changed, and cleans up. Project-specific wiring (a Perforce
`p4 flush`, a `git checkout`) runs as an optional post-mount `--hook`, never as part
of the engine.

## Why it's cheap

A shadow is copy-on-write: reads fall through to the read-only base, writes go to the
shadow. Measured on real hardware, a fresh shadow's footprint stays roughly constant
(tens of MB of filesystem metadata) **no matter how big the base is** — so a shadow's
cost tracks *what it writes*, not the size of the project.

## Platforms

Same commands everywhere; the copy-on-write backend is per-OS:

| OS | Backend | Notes |
|---|---|---|
| **Windows** | Differencing **VHDX** (VirtDisk API) | implemented; needs elevation for mounts |
| **macOS** | APFS **`clonefile`** (`cp -c`) | implemented; no elevation required |
| **Linux** | btrfs/XFS **reflink** (`cp --reflink`) / overlayfs | planned |

## Install

> Cross-platform packaging is being wired up (see [Status](#status)). Until releases
> are published these are the intended install paths.

**macOS / Linux (Homebrew):**
```sh
brew install popbot-ai/tap/shado
```

**Windows (Scoop):**
```powershell
scoop bucket add popbot-ai https://github.com/popbot-ai/scoop-bucket
scoop install shado
```

**From source (any platform with Go 1.26+):**
```sh
git clone https://github.com/popbot-ai/shado.git
cd shado
go build -o shado .
./shado doctor
```

Run `shado doctor` first — it checks privileges, the COW backend, and the store path.

## Usage

```sh
# One-time: freeze a warm folder into a base + 8 warm shadows
shado create D:\Game\Main --name game --count 8

# A clean, fully-WARM slot back instantly (no cold rebuild)
shado clone reset --name game --slot 3

# Close a slot without losing work, reopen later
shado clone park   --name game --slot 3
shado clone resume --name game --slot 3

# Re-warm the main shadow yourself, then refresh every slot from it
shado recache --name game            # --force to discard dirty slots

# Tear it all down, flatten the base back to a normal folder
shado restore --name game

shado ls        # inspect bases + shadows
```

Full reference: [`man shado`](docs/shado.1) (also `docs/shado.1`).

## Status

Early. The command surface is locked, and both the Windows VHDX backend and the
macOS APFS `clonefile` backend are implemented (frozen base + simultaneous
isolated shadows + instant reset/recache/restore). In progress: the Linux
reflink/overlayfs backend, the folder watcher behind the dirty-checks, and
release packaging (Homebrew tap + Scoop bucket via GoReleaser). A validated
PowerShell reference of the core flow lives in [`prototype/`](prototype/).

## Tests

```sh
go test ./...                              # unit tests (fast, no privileges) — also runs in CI
powershell test\run-all.ps1                # full suite (unit + integration); run elevated
powershell test\run-all.ps1 -SkipP4        # skip the Perforce integration test
```

- **Unit tests** (`*_test.go`) cover the registry, flag parsing, sizing, and path logic; CI runs `go vet` + `go build` + `go test` on Windows and Linux (`.github/workflows/ci.yml`).
- **Integration tests** (need an elevated shell + Hyper-V — they create/mount real VHDX):
  - `test/e2e.ps1` — base/clone/park/resume/reset/restore lifecycle + isolation
  - `test/recache.ps1` — promote a warmed main into a new base and re-base every shadow
  - `test/p4-init.ps1` — per-slot Perforce workspace via the `p4-init` hook (against a live P4 server)

## License

[MIT](LICENSE) © 2026 Benjamin Cooley

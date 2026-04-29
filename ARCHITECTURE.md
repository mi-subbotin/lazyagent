# Architecture

A contributor's tour of the lazyagent codebase. Read this before opening a non-trivial PR.

## Build & run

```bash
go run ./cmd/lazyagent           # real adapters (Claude / Codex / Gemini / Shared store)
go run ./cmd/lazyagent --mock    # mock source — useful when ~/.claude etc. are empty
go build ./cmd/lazyagent         # produces ./lazyagent
go vet ./...
go test ./...
```

Module path is `lazyagent` (Go 1.24). Tests live next to the package they cover (`*_test.go`).

## Big picture

The TUI is a single bubbletea `Model` with a flat `[]Item` list as the data backbone. Everything else — tree, filter, detail panel, modal overlays — is derived state recomputed on each frame.

```
adapters (per tool)        post-pass             derived
────────────────────       ───────────           ───────────
sources/claude   ─┐                              tree (rebuildTree)
sources/codex    ─┤  ──▶  []Item  ──▶  loadCmd  ──▶  detail
sources/gemini   ─┤        flat list   tags Shared    overlays
sources/lazyagent ┘                    + Drift        status line
```

## Data flow

1. `cmd/lazyagent/main.go` detects a project root via `detectProject` — looks for `.claude/`, `.codex/`, `.gemini/`, `.agents/`, `.mcp.json`, or `{CLAUDE,AGENTS,GEMINI}.md` in cwd. **Local-scope items must not appear when `projectDir == ""`.** Adapters enforce this and the TUI hides empty Local sections.
2. `main` also calls `store.Init()` unconditionally so `~/.lazyagent/store/{skills,agents,mcp,prompts,memory}/` exists before any user action — the Shared origin is data-driven and stays invisible until items appear.
3. Each `sources.Source` adapter (`internal/sources/{claude,codex,gemini,lazyagent,mock}`) implements `List(ctx, projectDir) []Item`. Adapters return a flat list; grouping by Origin → Kind → Scope happens in the TUI.
4. `tui.Model.loadCmd` aggregates all sources, runs a post-pass that tags each item with `Shared` / `Drift` against the canonical store, then dispatches `itemsLoadedMsg`. `rebuildTree()` produces the visible flat `[]node` for rendering.

## Item model — `internal/model/item.go`

`Item` is the unified currency. Three orthogonal axes:

- **Origin** — tool: `Claude` / `Codex` / `Gemini` / `Shared` (canonical lazyagent store).
- **Kind** — `Skill` / `Agent` / `MCP` / `Prompt` / `Memory`.
- **Scope** — `Global` / `Local`.

Plus a `Storage` discriminator that controls how `internal/actions` mutates the item:

- `StorageFile` — single file at `Item.Path`.
- `StorageDir` — directory at `filepath.Dir(Item.Path)` (a Skill folder with `SKILL.md` + assets).
- `StorageEntry` — entry inside a shared config file (e.g. `mcpServers/<name>` in `.claude.json`); the slash-separated path inside the config sits in `ConfigKey`.

For `StorageEntry` items the adapter fills `RawJSON` and/or `RawTOML` so the detail panel's `t` toggle can show the entry in either format. **When adding a new config-shaped item type, populate both Raw fields if you want the toggle to work.**

Two flags get set in the TUI's load post-pass, not by adapters:

- `Shared` — bytes resolve into the canonical store. True for `Origin == Shared`, true for symlink projections (path-based), and true for copy-mode projections (name-indexed against `store.ListItems()`).
- `Drift` — only set on shared items whose body bytes differ from canonical. Symlink projections always read through the link, so they're in sync by construction; copies on cloud-sync volumes are where drift actually shows up.

## Actions — `internal/actions/`

Single-item operations on `Item`:

- `Delete` / `Copy` / `Move` — `Move = Copy + Delete`. `remapToOtherScope` is the only interesting bit: Memory files live at *different relative paths* in global vs local scope (under tool home dir vs at project root), and Codex skills live under `~/.agents/` rather than `~/.codex/`. These are special-cased; everything else uses `remapStandard` which assumes parallel layouts.
- `CrossCopy(item, targetOrigin, targetScope)` — duplicate across tools. Pre-flight checks `SupportsCross` and `IsLossyCross`; the TUI builds the cross picker from these so disabled targets are shown with a reason instead of silently filtered.
- `Share(item, targets, overwrite)` — move bytes into `~/.lazyagent/store/<kind>/<name>/`, write `manifest.toml`, project to each selected tool via `EnsureLink` (symlink default, byte-copy on cloud-synced volumes via `store.PickLinkMode`).
- `Reshare(item, newTargets, overwrite)` — diffs current projections (`CurrentProjections` reads filesystem state, not manifest intent) against `newTargets` and applies add/remove deltas.
- `Resync(item, direction)` — handles `(drift)` cleanup. `ResyncCanonicalWins` reprojects every tool target, leaving healthy symlinks alone and replacing drifted copies. `ResyncToolWins` promotes the tool's bytes to canonical first, then reprojects.
- `Create(origin, kind, scope, name, projectDir)` — scaffolds a new item from `internal/actions/templates/*.md` (embedded via `go:embed`).

`StorageEntry` actions round-trip through `internal/parse/configfile.go` so JSON/TOML configs survive untouched.

### Pre-flight + confirm pattern

Any action that could replace existing on-disk content follows the same shape:

1. **Pure pre-flight** — e.g. `ShareConflicts(item, targets)` walks target paths, never mutates, returns a list of conflicts.
2. **Action takes `overwrite bool`** — without it, returns `ErrXxxConflicts` and leaves the filesystem untouched. With it, `RemoveAll`s the conflicts before doing the work.
3. **TUI overlay has two phases** — pick → confirm. Empty pre-flight commits immediately; non-empty flips to a confirmation view listing each path. `o`/`enter`/`y` confirms, `esc` returns to picker (selections preserved), `q` cancels.

Reuse this shape for every destructive action.

## Shared store — `internal/store/`

Canonical home for items the user opted into sharing.

```
~/.lazyagent/store/
  <kind>/<name>/
    SKILL.md | agent.md | prompt.md | memory.md   ← body file
    manifest.toml                                  ← metadata
```

Layout owners:

- `store.Root()` — root path, override via `LAZYAGENT_STORE`.
- `store.CanonicalBodyName(kind)` — per-Kind body filename. Single source of truth for adapters, share/reshare, and drift detection.
- `store.ItemDir(kind, name)` and `store.ManifestPath(itemDir)` — path helpers.
- `store.ListItems()` — walks the store, returns `map[Kind][]ItemEntry`.
- `store.ResolvesToStore(path)` — does this path, after `EvalSymlinks`, live inside the store? Path-based — only catches symlink projections.
- `store.CanonicalItemDir(path)` — same idea, but returns the canonical `<root>/<kind>/<name>/` for projections.
- `store.PickLinkMode(target)` — `LinkSymlink` by default, `LinkCopy` when the target lives under iCloud / Dropbox / OneDrive / Google Drive (where symlinks don't sync reliably).
- `store.EnsureLink(source, target, mode)` / `store.RemoveLink(source, target)` — projector with conflict detection (`ErrLinkConflict` when target exists with unrelated content).
- `store.IsDriftedAgainst(item, canonicalDir)` — body-bytes comparison; the TUI's load post-pass calls this with a prebuilt name index so we don't re-walk the store per item.

## TUI — `internal/tui/`

`Update` is a layered switch on modal state. **Order matters** — earlier branches swallow input:

1. `helpOpen` — any key closes.
2. `pending` (delete/copy/move confirm) — only y/n/esc respected.
3. `crossPicker` — digits select, esc cancels.
4. `sharePicker` — multi-select checklist; pick → confirm phase on conflicts.
5. `resyncPicker` — single key (c/t/esc) for drift resolution.
6. `creating` — typed-text input for new-item flow.
7. `editing` — built-in editor (textarea) with mtime conflict detection.
8. `filterMode` — keystrokes go into `filterText`; arrows/Tab still navigate.
9. `detailFull` (zoomed-in detail) — scroll keys + esc/tab back out.
10. Default tree/detail handlers.

Glamour-rendered markdown is cached in `m.glamourCache` keyed by `path|format|width` so resize and format-toggle are cheap. Cache is wiped on reload (`r`) and on item-targeted writes; **if you add a new write action, also evict matching cache entries** via `invalidateBodyCache(path)`.

## Adding a new Source (tool)

1. Create `internal/sources/<tool>/<tool>.go` with a `Source` struct implementing `Name()` and `List(ctx, projectDir)`.
2. Wire it in `cmd/lazyagent/main.go::main` (real-mode adapter list).
3. If the new tool needs a new memory-file name, add it to `detectProject` markers.
4. If the layout is *not* parallel between global and local, add a special case in `actions.remapToOtherScope`.
5. Add the tool to `originOrder` in `tui.rebuildTree` and to `defaultExpanded()` so the section appears in the tree.

### Symlink-aware directory walks

When walking a tool dir for kind=Skill, `os.ReadDir` returns `DirEntry` whose `IsDir()` reports false for symlinks-to-directories. Without a follow-the-link helper, shared-store projections silently disappear. Use `dirOrLinkToDir(parent, e)` (each adapter has it) — `Stat` resolves the link.

## Adding a new Kind

1. Add the constant + `String()` + `ParseKind()` cases in `internal/model/item.go`.
2. Extend `defaultExpanded()` and `kindOrder` in `internal/tui/app.go::rebuildTree` so the new section appears in the tree.
3. If shareable: add a case in `store.CanonicalBodyName(k)` and `actions.canShareKind` / `canProjectTo`.
4. Update the in-app `helpText()` if the new Kind needs special keys.

## Where things live

```
cmd/lazyagent/         ← entry point, project detection, CLI subcommands
internal/model/        ← Item, Origin, Kind, Scope, Storage
internal/sources/      ← per-tool adapters; one List() per Source
  claude/
  codex/
  gemini/
  lazyagent/           ← reads ~/.lazyagent/store as Origin=Shared
  mock/                ← fixture data for --mock
internal/actions/      ← Delete/Copy/Move/CrossCopy/Share/Reshare/Resync/Create
  templates/           ← go:embed scaffolds for `n` (new item)
internal/parse/        ← frontmatter parsing + JSON/TOML round-trip helpers
internal/store/        ← canonical store layout, manifests, projector, drift
internal/tui/          ← bubbletea Model, modal overlays, rendering
```

## Style notes

- Adapters stay layout-agnostic for the shared store. Don't import `store` to filter or transform; do the post-pass in `tui.loadCmd`.
- Default to no comments. Add one only when the *why* is non-obvious — a hidden constraint, a workaround for a specific bug, behavior that would surprise a reader.
- Atomic writes for everything that mutates user state: `os.CreateTemp` next to the target, write, `Close`, `os.Rename`. See `store.WriteManifest` and `actions.SaveFile` for the canonical pattern.
- When you add a destructive action, write a pure pre-flight first, take an `overwrite bool`, and let the TUI confirm before the act.

<!--
PRI-7 will drop a 200x200 logo here. Until then we run with an emoji
hero so the layout already matches the eventual design.

<p align="center">
  <img src="assets/logo.png" alt="lazyagent logo" width="200">
</p>
-->

<h1 align="center">🦥 lazyagent</h1>

<p align="center"><b>A lazygit-style TUI for skills, subagents, MCP servers, prompts and memory across Claude Code, Codex and Gemini CLI — one tree, one hotkey to share between tools.</b></p>

<p align="center">
  <a href="https://github.com/mi-subbotin/lazyagent/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/mi-subbotin/lazyagent/actions/workflows/ci.yml/badge.svg"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/mi-subbotin/lazyagent?logo=go&logoColor=white"></a>
  <a href="https://github.com/mi-subbotin/lazyagent/actions/workflows/ci.yml"><img alt="Coverage" src="https://img.shields.io/badge/coverage-63%25-yellowgreen?logo=go&logoColor=white"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/mi-subbotin/lazyagent?color=blue"></a>
</p>

<p align="center">
  <a href="https://github.com/mi-subbotin/lazyagent/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/mi-subbotin/lazyagent?display_name=tag&sort=semver&color=brightgreen"></a>
  <a href="https://github.com/mi-subbotin/homebrew-tap"><img alt="Homebrew tap" src="https://img.shields.io/badge/brew-mi--subbotin%2Ftap%2Flazyagent-orange?logo=homebrew&logoColor=white"></a>
  <a href="#go-install"><img alt="go install" src="https://img.shields.io/badge/go_install-ready-00ADD8?logo=go&logoColor=white"></a>
  <a href="https://github.com/mi-subbotin/lazyagent/stargazers"><img alt="GitHub stars" src="https://img.shields.io/github/stars/mi-subbotin/lazyagent?logo=github&color=yellow"></a>
</p>

<p align="center">
  <a href="https://github.com/mi-subbotin/lazyagent/issues"><img alt="Open issues" src="https://img.shields.io/github/issues/mi-subbotin/lazyagent?logo=github"></a>
  <a href="https://github.com/mi-subbotin/lazyagent/discussions"><img alt="Discussions" src="https://img.shields.io/badge/Discussions-open-181717?logo=github"></a>
</p>

<p align="center">
  <a href="#install"><img alt="Install" src="https://img.shields.io/badge/%F0%9F%9A%80_install-009688?style=for-the-badge"></a>&nbsp;
  <a href="#quickstart"><img alt="Quickstart" src="https://img.shields.io/badge/%E2%9A%A1_quickstart-3949AB?style=for-the-badge"></a>
</p>

<p align="center">
  <a href="assets/tui-overview.png"><img alt="lazyagent — split-pane TUI showing skills, agents, MCP servers and memory grouped by tool" src="assets/tui-overview.png" width="860"></a>
</p>

<p align="center"><sub>Split-pane tree on the left, glamour-rendered detail on the right. <code>j</code>/<code>k</code> to navigate, <code>tab</code> to drill in, <code>?</code> for help.</sub></p>

> **TL;DR** — One TUI for every Claude Code, Codex and Gemini CLI artifact in your home directory. Browse, edit, **place** a single canonical version into the lazyagent library and project it into all three tools, install skills/agents from any public GitHub repo. Local-first, no telemetry, brew-installable.

---

## 📣 News

- **2026-05-01** — `PRI-26` Hooks are now a first-class Kind — Claude `PreToolUse` / `PostToolUse` / `SessionStart` entries in your `settings.json` show up in the tree with a "⚠ runs shell" warning so you see what will execute before it does.
- **2026-05-01** — `PRI-10` `~/.lazyagent/ignore` keeps work / corporate trees out of the global indexer. Manage it from the CLI: `lazyagent ignore add '~/work/'`.
- **2026-05-01** — `PRI-19` Weekly GitHub releases check + amber "↑ vX.Y.Z available" footer when your build is behind. Disable with `[updates] notify = false` or `--no-update-check`.
- **2026-05-01** — `PRI-4` Global project indexer + `A` toggle to fold every discovered project's local items into the same tree. Cache lives at `~/.lazyagent/index.json`.
- **2026-05-01** — `PRI-20` First-run empty-state with an ASCII logo + per-section "no \<kind\> yet" placeholders so a fresh install no longer paints a blank panel.
- **2026-04-30** — `PRI-3` Install skills / agents / prompts from any public GitHub repo: `i` in the TUI or `lazyagent install <url>` from the shell. `U` updates an installed item to the origin's latest sha.
- **2026-04-30** — `PRI-18` Broken frontmatter is now surfaced with an `(invalid)` badge instead of being silently skipped, with the diagnostic shown in the detail panel.
- **2026-04-30** — `PRI-21` Shell completions for bash / zsh / fish; Homebrew installs them automatically, otherwise `lazyagent completion <shell>` prints the script.
- **2026-04-30** — `PRI-17` Structured logging via `slog` + `lumberjack`. `lazyagent logs tail` is the new way to grab context for bug reports.
- **2026-04-30** — `PRI-16` Optional `~/.lazyagent/config.toml` for tool toggles, search roots, logging level and more — managed via `lazyagent config {init,show,edit,validate}`.
- **2026-04-29** — `PRI-5` Resume Claude / Codex / Gemini sessions straight from the tree with `R`.
- **2026-04-29** — `PRI-2` Shared canonical store with symlink projection and drift detection (`s` / `R`).

---

## Why

Three CLIs, three home directories, three ways of doing the same thing. If you use Claude Code, Codex and Gemini CLI in the same week you end up with:

- **Three parallel hierarchies** — `~/.claude/`, `~/.codex/` (+ `~/.agents/`), `~/.gemini/` — each with its own skills/, agents/, prompts/, memory file and MCP config.
- **Duplicates by hand**: the only way to use a skill in all three tools is to `cp -r` it, which forks the moment you edit one copy.
- **No common view**: there's no way to see at a glance which prompts you have, which MCP servers are wired up, or which agent is project-local vs global.

`lazyagent` puts all of that in a single tree and — through a single unified `place` action — keeps **one canonical copy** of each item in `~/.lazyagent/library/` and projects it into every tool you pick via symlinks (or copies on cloud-synced volumes), so editing one place updates everywhere.

## Install

### Homebrew (macOS arm64 / amd64)

<!-- PRI-8 ships the tap; until then this command will 404. -->
```bash
brew install mi-subbotin/tap/lazyagent
```

> First launch on macOS triggers Gatekeeper. Right-click the binary → **Open**, or `xattr -d com.apple.quarantine $(which lazyagent)`. Codesigning / notarization is on the roadmap but not yet shipped.

<a id="go-install"></a>

### `go install`

```bash
go install github.com/mi-subbotin/lazyagent/cmd/lazyagent@latest
```

### Pre-built binaries

Download from the [latest release](https://github.com/mi-subbotin/lazyagent/releases/latest) — `darwin-arm64` and `darwin-amd64` archives include a single `lazyagent` binary. Linux and Windows builds are tracked in the roadmap.

## Quickstart

```bash
cd ~/Projects/your-project   # or anywhere
lazyagent
```

The tree is grouped **Origin → Kind → Scope**:

```
Claude
  Skills
    Global  (3)
    Local   (1)
  Agents
  ...
Codex
  ...
Gemini
  ...
Shared              ← appears once you press `p` to put an item into the library
```

Local-scope rows show only when `lazyagent` is launched from a directory that contains tool markers (`.claude/`, `.codex/`, `.gemini/`, `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.mcp.json`). Otherwise local sections are hidden — there's no project to attach to.

## Features

| Feature                                    | Claude | Codex | Gemini |
| ------------------------------------------ | :----: | :---: | :----: |
| Skills (`SKILL.md` + assets)               |   ✓    |   ✓   |   ✓    |
| Subagents                                  |   ✓    |   ✓ ¹ |   ✓    |
| MCP servers                                |   ✓    |   ✓   |   ✓    |
| Prompts / slash commands                   |   ✓    |   ✓   |   ✓ ²  |
| Memory file (`CLAUDE.md`/`AGENTS.md`/...)  |   ✓    |   ✓   |   ✓    |
| Hooks (PreToolUse / PostToolUse / …)       |   ✓ ⁴  |   —   |   —    |
| Place to library + projections (`p`)       |   ✓    |   ✓ ³ |   ✓ ³  |
| Inline edit (`E`) + external editor (`e`)  |   ✓    |   ✓   |   ✓    |
| Drift detection + resync (`R`)             |   ✓    |   ✓   |   ✓    |

¹ Codex agents are stored as `[profiles.<name>]` entries in `config.toml`; format conversion to/from `agent.md` is deferred (PRI-68).<br>
² Gemini commands are TOML; conversion from Claude/Codex markdown is deferred (PRI-68).<br>
³ The place picker greys out cells that need format conversion until PRI-68 lands. MCP/Hook entry-shape items are also deferred from `p` (PRI-69) — use the legacy CLI flow for now.<br>
⁴ Codex / Gemini hook adapters are deferred — formats need verification on a live install (PRI-57).

<p align="center">
  <img src="assets/detail-zoom.png" alt="lazyagent — fullscreen detail view of a SKILL.md, glamour-styled markdown body" width="860">
</p>

<p align="center"><sub>Press <code>tab</code> on a leaf to zoom into a glamour-rendered detail view.</sub></p>

<!--
PRI-15: replace the hero image with assets/demo.gif (or an asciinema
embed) once a demo recording lands. The two static shots stay as
fallbacks for the GitHub crawler that doesn't render videos.
-->

## Keys

```
  j/k      navigate up/down (scroll body in detail)
  h/l      collapse / expand group
  tab      open item full-screen / back out (toggle)
  enter    same as tab (drill into item or expand group)
  space    page down in fullscreen detail
  g / G    jump to top / end in fullscreen detail
  /        filter items by name/description
  esc      clear filter / cancel filter editor / cancel confirm
  t        toggle JSON / TOML for MCP entries
  d        delete item (asks y/n)
  p        place item — pick which (Origin × Scope) cells project the
           item from the library; bytes live once in ~/.lazyagent/library
           and project back to each chosen cell (replaces legacy c/m/x/s)
  R        resync drifted library item (canonical / tool wins)
  e        open in $EDITOR (external)
  E        edit in built-in editor (ctrl+s save · esc cancel)
  n        create new Skill / Agent / Prompt
  r        reload all sources
  ?        toggle this help (in-app)
  q        quit
```

`lazyagent` — read-only by default for everything it doesn't recognise. Editing actions are explicit and confirm before destructive ops.

## How storage works — the library

`lazyagent` stores every shareable item **once**, in the library at `~/.lazyagent/library/`, and projects it back to each tool that needs it. No duplicate copies, no fork-on-edit. The library is created on first launch — no separate `init` step. (An older `~/.lazyagent/store/` directory is migrated automatically.)

```
~/.lazyagent/library/
  skills/<name>/{SKILL.md, manifest.toml, ...}
  agents/<name>/{agent.md, manifest.toml}
  prompts/<name>/{prompt.md, manifest.toml}
  memory/<name>/{memory.md, manifest.toml}
  mcp/...                 (deferred — PRI-69)
```

Pressing `p` on any per-tool item opens a single picker:

```
Place skill foo:

  Library: yes  (canonical bytes — for optimization)

           Global   Local
  Claude   [x]      [ ]
  Codex    [ ]      [ ]
  Gemini   [x]      [ ]

  [arrows] move  [space] toggle  [enter] apply  [esc] cancel
```

Apply moves the bytes into the library (if not already there) and reconciles every `(Origin, Scope)` cell — symlink by default, byte-copy on iCloud / Dropbox / OneDrive / Google Drive volumes where symlinks don't sync. Unticking a cell unprojects it; an empty matrix is valid (item lives only in the library, no projections — like `git stash`). Pressing `p` again on a placed item lets you reshape the projection set. Pressing `R` on an item the detector flagged with `(drift)` opens the canonical-vs-tool resync overlay.

<p align="center">
  <img src="assets/detail-zoom.png" alt="Zoomed detail view of a placed skill — note the canonical path under ~/.lazyagent/library/ and the json/toml/back footer" width="900">
</p>

## Configuration

Optional. `lazyagent` works out of the box with no config file at all — every option below has a baked-in default. When you want to override something, run `lazyagent config init` to seed `~/.lazyagent/config.toml`, then edit it (`lazyagent config edit` is a shortcut for `$EDITOR`).

```toml
[search]
roots            = ["$HOME"]                        # PRI-4 global indexer roots
ignore_paths     = []                               # PRI-10 .gitignore-style excludes
follow_symlinks  = false

[ui]
theme            = "tokyonight"                     # tokyonight | catppuccin | nord
default_mode     = "cwd"                            # cwd | global
display_mode     = "origin"                         # origin | local-grouped
default_origin   = "all"                            # all | claude | codex | gemini

[tools]
claude           = true
codex            = true
gemini           = true
shared           = false

[install]
cache_dir        = "~/.lazyagent/cache"             # PRI-3 GitHub-install cache
gc_after_days    = 30

[updates]
check_interval_days = 7                             # PRI-19 release check cadence
notify              = true

[logging]
level            = "warn"                           # debug | info | warn | error
file             = "~/.lazyagent/logs/lazyagent.log"
format           = "text"                           # text | json
```

CLI helpers:

| Command                       | What it does                                                  |
| ----------------------------- | ------------------------------------------------------------- |
| `lazyagent config init`       | Write the defaults above to `~/.lazyagent/config.toml`. Refuses if the file exists; pass `--force` to overwrite. |
| `lazyagent config show`       | Print the effective config (defaults + your overrides) to stdout. |
| `lazyagent config edit`       | Open the file in `$EDITOR` (creates it from defaults first if missing). |
| `lazyagent config validate`   | Parse the file, list unknown keys and invalid enum values, exit non-zero on any. |

Partial files are fine — only put the keys you want to override; everything else stays at the default. Unknown keys and invalid enum values become warnings on stderr and the offender resets to its default rather than aborting startup.

## Install items from GitHub

Pull skills, agents and prompts straight from a public repo — either via the TUI (`i`) or the shell.

```bash
# show what's installable in a repo
lazyagent install github.com/anthropics/skills --list

# install everything into Claude (global)
lazyagent install github.com/anthropics/skills --all

# install one item by name into Codex (global)
lazyagent install github.com/foo/bar --target=codex --name=cool-skill

# refresh an installed item to the origin's latest sha
# (TUI: cursor on the item, then U)
# CLI: re-run install with --overwrite

# remove an installed item; --target/--scope to disambiguate when needed
lazyagent uninstall cool-skill --target=codex

# sweep abandoned tarball cache (auto-runs every 30 days at startup)
lazyagent cache gc
```

Supported URL shapes:

- `github.com/<owner>/<repo>` — install at the default branch
- `github.com/<owner>/<repo>/tree/<ref>[/path]` — pin a tag/sha or narrow to one subdir
- `github.com/<owner>/<repo>/blob/<ref>/path/file.md` — single file
- `gist.github.com/<id>` *(coming soon — currently rejected)*

The detection is convention-based: `skills/<name>/SKILL.md` → Skill, `agents/<name>.md` → Agent, `commands/<name>.md` → Prompt. Set `GH_TOKEN` (or `GITHUB_TOKEN`) for private repos. Pinned shas land in `~/.lazyagent/installed.toml`; tarballs cache under `~/.lazyagent/cache/` and are swept automatically.

## Shell completions

`lazyagent completion <bash|zsh|fish>` prints the completion script for the chosen shell to stdout.

Homebrew installs all three automatically — restart your shell after `brew install` and tab-completion just works.

For non-brew installs:

```bash
# bash (one-shot, current shell)
source <(lazyagent completion bash)

# bash (persistent, system-wide on macOS)
lazyagent completion bash | sudo tee /opt/homebrew/etc/bash_completion.d/lazyagent > /dev/null

# zsh (persistent — pick any directory in $fpath)
lazyagent completion zsh > "${fpath[1]}/_lazyagent"

# fish
lazyagent completion fish > ~/.config/fish/completions/lazyagent.fish
```

Completions cover all subcommands (`config / logs / shared / completion`) and the global flags (`--mock`, `--verbose`/`-v`, `--log-file`, `--log-format`).

## Troubleshooting

`lazyagent` writes a structured log to `~/.lazyagent/logs/lazyagent.log` (rotated daily, kept for a week). Adapter parse failures, edit/copy/share/delete actions and startup metadata land there — nothing ever goes to stdout/stderr while the TUI is running, so the altscreen stays clean.

```bash
lazyagent --verbose                  # bump log level to debug for the next run
lazyagent logs path                  # print the resolved log file path
lazyagent logs tail                  # last 50 lines of the active log
lazyagent logs tail -n 200           # more lines
lazyagent logs clean                 # remove the active log + rotated siblings
```

Override location and format on a per-run basis:

```bash
lazyagent --log-file /tmp/lz.log --log-format json
```

For bug reports, please attach `lazyagent logs tail` output — the [`bug_report`](.github/ISSUE_TEMPLATE/bug_report.yml) issue form has a dedicated field for it.

## Roadmap

- [x] Read-only TUI MVP across Claude / Codex / Gemini
- [x] Unified place picker (`p`) — Origin × Scope matrix backed by a single library copy (PRI-65)
- [x] Inline editor with mtime conflict detection (`E`)
- [x] Shared canonical store + symlink projector + drift detection (`s` / `R`)
- [ ] [Backlog: editing all tool-specific configs from one place (`PRI-1`)](https://linear.app/obscurectl/issue/PRI-1)
- [ ] [Backlog: GitHub install for skills / agents (`PRI-3`)](https://linear.app/obscurectl/issue/PRI-3)
- [ ] [Backlog: Global search across the whole tree (`PRI-4`)](https://linear.app/obscurectl/issue/PRI-4)
- [ ] [Backlog: Browse historical sessions across tools (`PRI-5`)](https://linear.app/obscurectl/issue/PRI-5)
- [ ] [Backlog: One-button sync of every shareable item (`PRI-27`)](https://linear.app/obscurectl/issue/PRI-27)

## Privacy

`lazyagent` runs entirely local. It reads files from your home directory and your project, and writes only when you trigger an explicit action (delete / place / resync / edit). No telemetry, no analytics.

The only outbound traffic is an optional weekly check of the GitHub releases API for an "↑ vX.Y.Z available" banner. Disable with `[updates] notify = false` in `~/.lazyagent/config.toml` or `--no-update-check` on a single launch.

### Filtering local projects

The global indexer (`A` hotkey) walks `$HOME` to find every directory that looks like a project root. To keep work / corporate / experimental trees out of that view, drop a gitignore-syntax file at `~/.lazyagent/ignore`:

```
# work projects
~/work/
$HOME/Company/

# anything matching a glob
**/private-*
```

Manage it from the CLI without touching the file directly:

```bash
lazyagent ignore add '~/work/'   # append a pattern
lazyagent ignore list            # print the active rules
lazyagent ignore path            # print the file path
```

`~/` and `$HOME/` are expanded to your home directory at load time. Anything else passes through to standard gitignore semantics — negations (`!`), globs, and anchored paths all work.

## Contributing

Bug reports and feature requests via [GitHub Issues](https://github.com/mi-subbotin/lazyagent/issues). PRs welcome — please open an issue first to discuss scope. Read [`VISION.md`](VISION.md) for what's in scope and what isn't, [`ARCHITECTURE.md`](ARCHITECTURE.md) for a codebase tour, and [`CONTRIBUTING.md`](CONTRIBUTING.md) for the workflow. Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).

## License

[Apache-2.0](LICENSE) — see also [NOTICE](NOTICE) for attribution requirements when redistributing modified versions.

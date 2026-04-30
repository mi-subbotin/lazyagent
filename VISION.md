# Vision

`lazyagent` is a unified TUI for managing the configuration that surrounds Claude Code, Codex and Gemini CLI — skills, subagents, MCP servers, prompts and memory. It is read-modify-write on those tools' on-disk state. Nothing more.

This document exists so feature requests have something to point at. If a request is in scope, it ships eventually. If it's out of scope, no amount of polish will get it merged.

## What lazyagent IS

- A **single tree** over the three parallel hierarchies — `~/.claude/`, `~/.codex/` (+ `~/.agents/`), `~/.gemini/` — and the matching project-local directories.
- A **read-modify-write** TUI on existing on-disk formats. Each tool stays the source of truth for its own format; lazyagent is a projection.
- **Mac-first** for v1. Linux and Windows are tracked but not promised — they ship if there is demand and a maintainer.
- **Open source, Apache-2.0**, local-first, terminal-only.

## What lazyagent IS NOT

- **Not a replacement** for the Claude / Codex / Gemini CLIs. Those remain authoritative for invoking the agents themselves.
- **Not an agent orchestrator.** lazyagent does not run agents. Resume of past sessions (`R`) is an exec-handoff into the native CLI, not its own runtime.
- **Not a registry or marketplace.** GitHub-based install (PRI-3) fetches public repos; lazyagent never hosts content of its own.
- **Not a general-purpose markdown editor.** The built-in editor is for quick config edits. Anything serious goes to `$EDITOR`.
- **Not a GUI.** The terminal is part of the design, not a limitation.
- **Not a lossless format converter.** Cross-tool copy surfaces lossy diffs explicitly; it never silently guesses an equivalent.
- **Not telemetered.** No analytics, no opt-out toggle, no phone-home. There is nothing to disable because nothing is collected.

## Core principles

- **The tool owns its format.** Claude Code, Codex and Gemini CLI define the schemas; lazyagent reads and writes them, but never invents new ones.
- **Lossy operations are explicit.** Cross-copy and format conversions show what will be dropped or rewritten before the user confirms.
- **Atomic writes.** Saves go through `rename(2)`. No backup files; the user is presumed to be in git.
- **Local-first.** Everything is files under `$HOME`. No database, no cloud sync requirement, no daemon.
- **Offline by default.** The TUI itself never makes network calls. `install` and `update` are the only commands that touch the network, and they say so.
- **Safe by default.** Read-only for anything lazyagent doesn't recognise. Destructive actions (delete / move / overwrite) confirm.

## When this document changes

`VISION.md` only changes when the fundamental scope changes. Adding a feature inside the scope above doesn't require an edit. Expanding scope (e.g. supporting a fourth tool, or adding a runtime layer) does.

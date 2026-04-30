# Contributing to lazyagent

Thanks for your interest. This document covers the practical bits — for the *why* of the project, see [`VISION.md`](VISION.md).

## Before you start

1. **Skim `VISION.md`.** Feature requests outside the documented scope are usually closed; opening an issue first saves both of us time on a PR that won't merge.
2. **Open a Linear / GitHub issue** for non-trivial changes. Bug fixes with a clear repro can go straight to a PR.
3. **One concern per PR.** A bug fix and an unrelated refactor in the same PR will be asked to split.

## Local development

Requirements: Go 1.24+ (the version pinned in `go.mod`).

```bash
git clone https://github.com/mi-subbotin/lazyagent
cd lazyagent
go build ./...
go vet ./...
go test ./... -race -count=1
```

Run against your real config:

```bash
go run ./cmd/lazyagent
```

Run against the bundled mock source (handy when `~/.claude` etc. are empty):

```bash
go run ./cmd/lazyagent --mock
```

For UI iteration in a project that has tool markers (`.claude/`, `.codex/`, `.gemini/`, `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.mcp.json`), `cd` into the project before launching — local-scope items only appear when `lazyagent` detects a project root.

## Architecture

Read [`ARCHITECTURE.md`](ARCHITECTURE.md) for the codebase tour. Short version:

- `cmd/lazyagent` — entrypoint and project-root detection.
- `internal/sources/{claude,codex,gemini,mock}` — per-tool adapters returning a flat `[]Item`.
- `internal/model` — the `Item` type that everything else operates on.
- `internal/actions` — `Delete`, `Copy`, `Move`, `CrossCopy`, `Share`, `Resync`, `Resume`, etc.
- `internal/store` — the canonical shared store under `~/.lazyagent/store/`.
- `internal/tui` — the bubbletea `Model`, all rendering and key handling.

When adding a new source or a new `Kind`, the architecture doc has a short checklist.

## Tests

- Unit tests live next to the package they cover (`*_test.go`).
- Use the standard `testing` package; `testify/require` is fine for assertions if it makes the test clearer.
- Golden files go in `testdata/`.
- New behaviour gets a test. Bug fixes get a regression test that fails before the fix and passes after.

CI runs `go vet`, `go build`, `go test -race -count=1` and `govulncheck ./...` on macOS.

## Style

- Prefer existing patterns in the file you're editing over importing new conventions.
- No comments that re-state the code. Reserve comments for non-obvious *why*.
- No emojis in source files unless they were already there for a reason (badge labels, etc.).
- Keep TUI key bindings consistent with the rest of the app — if you add a new key, update `helpText()` and the README.

## Pull requests

- Fill in the PR template; the checklist is short on purpose.
- Mention the Linear issue (`PRI-NNN`) the PR closes or relates to.
- A reviewer may ask for a rebase rather than a merge commit — `main` history is linear.

## Reporting security issues

See [`SECURITY.md`](.github/SECURITY.md). Please use GitHub's private vulnerability reporting, not a public issue.

## Code of conduct

Participation in this project is governed by the [Contributor Covenant](CODE_OF_CONDUCT.md). By contributing, you agree to abide by it.

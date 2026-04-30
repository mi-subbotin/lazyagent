# Security policy

## Supported versions

Only the latest released minor version of `lazyagent` receives security fixes. Older versions are not patched; please upgrade via `brew upgrade lazyagent` or by re-running `go install`.

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |
| older   | :x:                |

## Reporting a vulnerability

Please **do not open a public issue** for security reports. Use GitHub's private vulnerability reporting:

1. Go to the [Security tab](https://github.com/mi-subbotin/lazyagent/security) of the repository.
2. Click **Report a vulnerability**.
3. Describe the issue, ideally with a minimal reproduction and the version of `lazyagent` (`lazyagent --version`), Go (`go version`), and OS.

You can expect:

- An acknowledgement within **7 days**.
- A triage decision (accepted / not-a-vulnerability / duplicate) within **14 days**.
- A fix and coordinated release for accepted reports as quickly as the fix complexity allows.

## What we scan

Every push and pull request runs `govulncheck ./...` in CI against the Go module graph. Dependabot is configured for weekly updates of Go modules and GitHub Actions, so known CVEs in dependencies surface as PRs.

## Scope

In scope:

- The `lazyagent` binary and the Go module under `github.com/mi-subbotin/lazyagent`.
- The Homebrew formula in `mi-subbotin/homebrew-tap`.

Out of scope:

- Vulnerabilities in upstream tools (Claude Code, Codex, Gemini CLI) — please report those to the respective vendors.
- Vulnerabilities in transitive Go dependencies that already have an upstream fix — open a regular issue or PR to bump the version.

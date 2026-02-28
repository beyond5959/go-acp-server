# AGENTS

This repository implements the Code Agent Hub Server.

## Mandatory Rules

- MUST use Go 1.24.
- MUST keep `go test ./...` passing for every change.
- MUST listen on `127.0.0.1` by default.
- MUST require explicit `--allow-public=true` to listen on public interfaces.
- MUST validate inputs:
  - `agent` must be in server allowlist.
  - `cwd` must be an absolute path.
  - `cwd` must be inside configured allowed roots, otherwise reject.
- MUST enforce concurrency model:
  - one active turn per thread at a time.
  - cancel must take effect quickly.
  - permission workflow is fail-closed by default.
- MUST keep stdout and HTTP output protocol-only.
- MUST send logs to stderr with JSON `slog`.
- MUST redact sensitive information in logs and errors.

## Long-Term Memory Rules

- At the end of every completed phase, update `PROGRESS.md`.
- Write key technical/product decisions to `docs/DECISIONS.md`.
- Track known limitations and risks in `docs/KNOWN_ISSUES.md`.
- Keep acceptance checklist in `docs/ACCEPTANCE.md`.
- Keep implementation design in `docs/SPEC.md`.

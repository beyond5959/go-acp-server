# CLAUDE.md

This repository implements the **Code Agent Hub Server** — a local-first Go service exposing HTTP/JSON APIs and SSE streaming for multi-client, multi-thread ACP-compatible agent turns.

## Common Commands

```bash
make test          # go test ./...
make fmt           # gofmt -w all .go files
make build-web     # cd web && npm ci && npm run build  (generates web/dist)
make run           # build-web + go run ./cmd/agent-hub-server
go build ./...     # compile check
```

## Module Structure

| Package | Responsibility |
|---|---|
| `cmd/agent-hub-server` | Entry point, CLI flags, startup summary |
| `internal/httpapi` | Routing, request validation, error encoding |
| `internal/runtime` | Thread controller, turn state machine, cancel coordination |
| `internal/agents` | Agent providers: fake, ACP stdio, embedded codex |
| `internal/context` | Prompt injection (summary + recent turns + current input) |
| `internal/sse` | SSE formatting, fanout, resume helpers |
| `internal/storage` | SQLite repository, migration runner |
| `internal/observability` | Structured JSON logging, redaction helpers |

## Mandatory Constraints

- Use **Go 1.24**. Module path: `github.com/beyond5959/go-acp-server`.
- `go test ./...` must pass for every change.
- Input validation:
  - `agent` must be in the server allowlist.
  - `cwd` must be an absolute path.
- Codex provider runs in **embedded mode** (`github.com/beyond5959/codex-acp/pkg/codexacp`). Do not add user-facing binary path flags.
- Concurrency model:
  - one active turn per thread at a time (`409 CONFLICT` on conflict).
  - cancel must take effect quickly.
  - permission workflow is **fail-closed** by default (timeout/disconnect → `declined`).
- stdout and HTTP response bodies carry **protocol data only**.
- All logs go to **stderr** as JSON via `slog`. Redact sensitive data in logs and errors.

## Key Documentation

| File | Contents |
|---|---|
| `PROGRESS.md` | Milestone status and next actions — **update at end of every completed phase** |
| `docs/DECISIONS.md` | Architecture Decision Records — **record key technical/product decisions here** |
| `docs/KNOWN_ISSUES.md` | Known limitations and risks — **track new issues here** |
| `docs/ACCEPTANCE.md` | Executable acceptance checklist (`go test` + `curl` commands) |
| `docs/SPEC.md` | Implementation design and API overview |
| `docs/API.md` | Full HTTP endpoint and schema contracts |
| `docs/ARCHITECTURE.md` | Module graph and runtime model |
| `docs/FRONTEND_SPEC.md` | Frontend UI design, feature list, tech stack, Go embed integration |
| `docs/FRONTEND_TASKS.md` | Frontend milestone tasks (F0–F9) with acceptance criteria |

# SPEC

## 1. Goal

Build a local-first Code Agent Hub Server that:

- serves HTTP/JSON APIs.
- streams turn events via SSE (`text/event-stream`).
- supports multi-client and multi-thread execution.
- persists interaction state/events in SQLite.
- forwards runtime permissions to the owning client with fail-closed behavior.

## 2. High-Level Architecture

Modules:

- `internal/httpapi`: routing, request validation, response/error encoding.
- `internal/runtime`: thread controller, turn state machine, cancellation coordination.
- `internal/agents`: agent providers (fake, ACP stdio, codex), plus context-bound permission callback bridge.
  - per-turn provider resolution selects implementation by thread metadata (agent id + cwd).
- `internal/context`: prompt injection strategy assembled in HTTP/runtime path from summary + recent turns + current input.
- `internal/sse`: event formatting, stream fanout, resume helpers.
- `internal/storage`: SQLite repository and migration management.
- `internal/observability`: structured JSON logging and redaction helpers.

## 3. Concurrency Model

- Thread is the concurrency isolation unit.
- Each thread has exactly one active turn.
- New turn requests on active thread return conflict error.
- Cancel request transitions turn state immediately and propagates cancellation token to provider.
- Permission requests suspend the turn until a client decision arrives or timeout occurs.

## 4. Lazy Agent Startup

- On server boot: no agent process is started.
- On first thread usage: runtime requests provider instance for that thread.
- On first turn execution for codex thread: server creates ACP stdio client and starts `codex-acp-go` process.
- ACP process working directory is `thread.cwd` (already validated against allowed roots at thread creation).
- Provider instances are cached per thread and reclaimed by idle TTL (`--agent-idle-ttl`) when thread has no active turn.

## 5. Permission Bridge

Runtime permission categories:

- `command`
- `file`
- `network`
- `mcp`

Flow:

1. agent emits permission request event.
2. server persists event and forwards it to client stream/API.
   - SSE emits `permission_required` with `permissionId`.
3. turn waits for explicit decision.
   - client submits `POST /v1/permissions/{permissionId}` with outcome.
4. if decision is missing/late/invalid, default is deny (fail-closed).

## 6. Persistence Model

SQLite stores:

- clients
- threads
- turns
- events (append-only stream records)

Properties:

- all outbound stream events are persisted before or atomically with emission strategy.
- each event has monotonic sequence per thread or turn.
- restart can rebuild state from durable turn status plus event log.

## 7. Recovery Strategy

On restart:

- load latest thread and turn states from SQLite.
- rebuild turn context window from persisted `threads.summary` and recent non-internal turns.
- mark previously active turns as interrupted/recovering depending on provider capability.
- allow clients to query history and continue with new turn.
- replay SSE history from stored events for continuity.
- graceful shutdown drains active turns with timeout and force-cancel fallback for stuck requests.

## 8. API Overview

- health and server metadata
- thread CRUD (create/list/get)
- turn create/cancel
- thread compact (`POST /v1/threads/{threadId}/compact`)
- SSE stream for real-time events
- permission decision endpoint

See `docs/API.md` for endpoint and schema contracts.

## 9. Security and Runtime Constraints

- default bind: `127.0.0.1:8686`.
- public bind only when `--allow-public=true`.
- strict input validation:
  - agent must be allowlisted.
  - cwd absolute and inside allowed roots.
- logs are JSON on stderr and redact sensitive data.
- HTTP payloads contain protocol data only.

## 10. Error Contract

All API errors follow a common envelope with:

- stable machine code
- user-friendly message
- optional hint
- request id
- optional details map

See `docs/API.md` for concrete schema and codes.

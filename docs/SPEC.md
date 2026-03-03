# SPEC

## 1. Goal

Build a local-first Code Agent Hub Server that:

- serves HTTP/JSON APIs.
- streams turn events via SSE (`text/event-stream`).
- supports multi-client and multi-thread execution.
- is designed to support ACP-compatible agents (for example Claude Code, Gemini, OpenCode, Codex).
- persists interaction state/events in SQLite.
- forwards runtime permissions to the owning client with fail-closed behavior.

## 2. High-Level Architecture

Modules:

- `internal/httpapi`: routing, request validation, response/error encoding.
- `internal/runtime`: thread controller, turn state machine, cancellation coordination.
- `internal/agents`: agent providers (fake + ACP-compatible implementations), plus context-bound permission callback bridge.
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
- On first turn execution for embedded-provider thread (currently `codex`): server creates the in-process runtime and initializes ACP session lazily.
- Embedded runtime `session/new` is created with `cwd=thread.cwd` (validated as absolute path at thread creation).
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
- thread deletion removes dependent rows in order (`events` -> `turns` -> `threads`) in one transaction.
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
- thread CRUD (create/list/get/delete)
- turn create/cancel
- thread compact (`POST /v1/threads/{threadId}/compact`)
- SSE stream for real-time events
- permission decision endpoint

See `docs/API.md` for endpoint and schema contracts.

## 9. Security and Runtime Constraints

- default bind: `0.0.0.0:8686` (LAN-accessible).
- local-only bind when `--allow-public=false`.
- strict input validation:
  - agent must be allowlisted.
  - cwd must be absolute.
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

## 11. Planned Qwen ACP Integration (2026-03-03)

### 11.1 Objective and Scope

- add `qwen` as a first-class ACP provider using `qwen --acp`.
- preserve all existing contracts and behavior for `codex`, `opencode`, and `gemini`.
- keep runtime/security invariants unchanged:
  - one active turn per thread.
  - fail-closed permission workflow.
  - allowlisted `agent` and absolute+allowed `cwd` validation.
  - protocol-only stdout/HTTP payloads, JSON logs on stderr.

### 11.2 Qwen ACP Protocol Profile

The integration must follow Qwen ACP requirements observed from local `qwen 0.11.0`
and upstream ACP schema:

- `initialize` requires:
  - `protocolVersion: 1`
  - `clientCapabilities.fs.readTextFile`
  - `clientCapabilities.fs.writeTextFile`
- `session/new` requires:
  - `cwd` (thread cwd)
  - `mcpServers` (use empty array when not configured)
- `session/prompt` request format:
  - `prompt` must be `[]contentBlock`, use text block for hub input.
- `session/update` notifications:
  - stream deltas from `update.sessionUpdate == "agent_message_chunk"`
  - consume text from `update.content.text` only.
- permission request/response:
  - handle `session/request_permission` request from provider.
  - reply with ACP outcome shape:
    - approve: `{outcome:{outcome:"selected", optionId:<allow option>}}`
    - decline: `{outcome:{outcome:"selected", optionId:<reject option>}}`
    - cancel: `{outcome:{outcome:"cancelled"}}`
  - default deny if hub decision is missing/invalid/timeout.
- cancellation:
  - on context cancel, send `session/cancel` with `sessionId`.
  - return `StopReasonCancelled` quickly.

### 11.3 Implementation Blueprint

- add `internal/agents/qwen` as a standalone provider package:
  - `Config{Dir string, ModelID string}`
  - `Preflight()` checks `qwen` binary in PATH.
  - `Client.Stream()` uses process-per-turn ACP stdio flow.
- keep implementation additive:
  - do not refactor shared `internal/agents/acp` or existing providers.
- main wiring in `cmd/agent-hub-server/main.go`:
  - qwen preflight and status in `/v1/agents`.
  - add `qwen` to `AllowedAgentIDs`.
  - add `TurnAgentFactory` case for `thread.AgentID == "qwen"`.
  - optional `agentOptions.modelId` mapped to ACP `session/set_model` (best effort).

### 11.4 R&D Task Breakdown

1. Implement provider package `internal/agents/qwen`.
2. Add provider unit tests with fake ACP process simulation.
3. Add optional env-gated real smoke test for local `qwen`.
4. Wire qwen into startup preflight, supported agents list, and thread factory.
5. Update/extend tests in `cmd/agent-hub-server` for qwen registration/status.
6. Update docs (`README`, `SPEC`, `ACCEPTANCE`, `DECISIONS`, `KNOWN_ISSUES`, `PROGRESS`).
7. Run regression gates:
   - `cd internal/webui/web && npm run build`
   - `go test ./...`

### 11.5 Detailed Execution Plan

- Phase P0: protocol validation and risk framing.
  - Deliverable: local ACP handshake evidence and compatibility notes.
  - DoD: required request fields and response shapes confirmed.
- Phase P1: `internal/agents/qwen` implementation.
  - Deliverable: compilable provider with stream/cancel/permission support.
  - DoD: provider fake tests pass.
- Phase P2: server wiring and contract alignment.
  - Deliverable: `/v1/agents` + thread creation + turn factory support `qwen`.
  - DoD: wiring tests pass and existing providers unchanged.
- Phase P3: docs and acceptance checklist.
  - Deliverable: this plan plus executable acceptance commands.
  - DoD: docs reviewed and committed with explicit pending status where applicable.
- Phase P4: full regression.
  - Deliverable: clean baseline for merge.
  - DoD: `npm run build` and `go test ./...` pass.

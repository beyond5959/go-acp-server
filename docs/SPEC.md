# SPEC

## 1. Goal

Build a local-first Code Agent Hub Server that:

- serves HTTP/JSON APIs.
- streams turn events via SSE (`text/event-stream`).
- supports multi-client and multi-thread execution.
- is designed to support ACP-compatible agents (for example Claude Code, Gemini, Kimi, OpenCode, Codex).
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
- If `thread.agent_options_json` contains `modelId`, providers apply it as model override during thread-level runtime/session initialization.
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
- thread CRUD (create/list/get/update/delete)
- turn create/cancel
- thread compact (`POST /v1/threads/{threadId}/compact`)
- SSE stream for real-time events
- permission decision endpoint

See `docs/API.md` for endpoint and schema contracts.

## 9. Security and Runtime Constraints

- default bind: `127.0.0.1:8686` (local-only).
- public bind allowed when `--allow-public=true`.
- strict input validation:
  - agent must be allowlisted.
  - cwd must be absolute.
- thread option updates are rejected while a turn is active on the same thread.
- logs are JSON on stderr and redact sensitive data.
- `--debug=true` raises log verbosity to debug level and emits sanitized ACP JSON-RPC request/response traces on stderr.
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
  - map `update.sessionUpdate == "plan"` into hub `plan_update` SSE events; each `entries[]` payload replaces the current plan list.
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
- main wiring in `cmd/ngent/main.go`:
  - qwen preflight and status in `/v1/agents`.
  - add `qwen` to `AllowedAgentIDs`.
  - add `TurnAgentFactory` case for `thread.AgentID == "qwen"`.
  - optional `agentOptions.modelId` mapped to ACP `session/prompt.model`.

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

## 12. Thread Model Selection and Switching (2026-03-05)

### 12.1 Scope

- New thread creation supports explicit model selection by writing `agentOptions.modelId`.
- Existing thread supports model switching via thread update API and Web UI action.
- Applies to all providers:
  - `codex` and `claude` embedded runtimes receive `model` in ACP `session/new`.
  - `gemini` ACP stdio provider receives `model` in `session/new` and `session/prompt`.
  - `kimi` ACP stdio provider receives `model` in `session/prompt`, and during config/model discovery it sends both `model` and `modelId` in `session/new`.
  - `opencode` passes `modelId` in `session/prompt`.
  - `qwen` passes `model` in `session/prompt`.

### 12.2 API and Runtime Behavior

## 13. Kimi CLI ACP Integration (2026-03-09)

### 13.1 Objective and Scope

- add `kimi` as a first-class ACP provider using the Kimi CLI ACP mode.
- preserve existing runtime/security invariants:
  - one active turn per thread.
  - fail-closed permission workflow.
  - allowlisted `agent` and absolute+allowed `cwd` validation.
  - protocol-only stdout/HTTP payloads, JSON logs on stderr.

### 13.2 Kimi ACP Protocol Profile

- startup command:
  - official Kimi docs currently show both `kimi acp` and `kimi --acp`.
  - the hub must try `kimi acp` first and fall back to `kimi --acp` when ACP initialization closes immediately.
- local config sourcing:
  - model catalog and default thinking state should be read from local Kimi config (`config.toml`) when available.
  - avoid creating ACP `session/new` calls for model discovery or thread config queries that do not send a real prompt, to prevent empty Kimi sessions.
  - if local config is unavailable or cannot be parsed, fall back to ACP handshake-based discovery/query behavior.
- ACP flow:
  - `initialize` with `protocolVersion: 1` and `clientCapabilities.fs`.
  - `session/new` with `cwd` and `mcpServers: []`.
  - `session/prompt` with ACP prompt blocks (`[{type:"text", text:...}]`).
- streaming:
  - consume `session/update` deltas only when `update.sessionUpdate == "agent_message_chunk"`.
  - delta text is read from `update.content.text`.
  - map `update.sessionUpdate == "plan"` into hub `plan_update` SSE events; each `entries[]` payload replaces the current plan list.
- permissions and cancellation:
  - handle `session/request_permission` with fail-closed approval mapping.
  - on context cancellation, send `session/cancel` quickly and converge to `stopReason=cancelled`.

### 13.3 Server Wiring

- add `internal/agents/kimi` provider package with:
  - `Preflight()` checking `kimi` in PATH.
  - `DiscoverModels()` from ACP `session/new.models.availableModels`.
  - `ConfigOptionManager` support via transient ACP sessions and `session/set_config_option`.
- wire `kimi` into:
  - startup preflight diagnostics and `/v1/agents`.
  - thread agent allowlist and `TurnAgentFactory`.
  - startup config-catalog refresh.

- Added `PATCH /v1/threads/{threadId}` with request body containing optional `title` and `agentOptions`.
- Added `GET /v1/agents/{agentId}/models`:
  - backend queries provider ACP handshake (`initialize` + `session/new`) and extracts runtime model options from `configOptions` / `models.availableModels`.
  - returns normalized `[{"id","name"}]` entries for Web UI dropdowns.
- Ownership rule is unchanged (`404` for cross-client or missing thread).
- Active turn safety is strict:
  - when the thread currently has an active turn, update returns `409 CONFLICT`.
- On successful update:
  - when `title` is provided, persist `threads.title` and update `updated_at`.
  - when `agentOptions` is provided, persist `threads.agent_options_json` and update `updated_at`.
  - close cached per-thread provider instance only when `agentOptions` changes, so the next turn starts with updated model config.

### 12.3 Web UI Behavior

- New thread modal no longer exposes model selector; it keeps agent/cwd/title/advanced-options creation flow.
- Active thread header uses model dropdown + apply button (runtime options from `GET /v1/agents/{agentId}/models`):
  - apply calls `PATCH /v1/threads/{threadId}`.
  - controls are disabled while model list is loading or while a turn is streaming.

## 13. Thread Session Config Options and Immediate Model Switch (2026-03-05)

### 13.1 Scope

- Model configuration is sourced from ACP session config options, not hardcoded or static agent catalogs.
- On thread open/new, UI reads current model from `session/new` equivalent data (`configOptions` where `category=model` or `id=model`).
- Model switching is immediate on dropdown change (no apply button).
- Reasoning configuration is sourced from the same thread `configOptions` payload and remains model-specific.

### 13.2 API Contract

- Added thread-scoped config options APIs:
  - `GET /v1/threads/{threadId}/config-options`
  - `POST /v1/threads/{threadId}/config-options` with body:
    - `configId` (string, required)
    - `value` (string, required)
- `GET` response includes:
  - `threadId`
  - `configOptions[]` (ACP-shaped option objects with `currentValue` and `options[]`, including descriptions)
- `POST` behavior:
  - rejects while turn is active (`409 CONFLICT`).
  - applies runtime update through ACP `session/set_config_option`.
  - returns updated `configOptions`.
  - persists thread config state for future turns:
    - `configId=model` mirrors selected runtime value into `agentOptions.modelId`.
    - non-model current values are mirrored into `agentOptions.configOverrides[configId]`.

### 13.3 Provider Behavior

- Introduced provider capability interface `ConfigOptionManager`:
  - `ConfigOptions(ctx) ([]ConfigOption, error)`
  - `SetConfigOption(ctx, configID, value) ([]ConfigOption, error)`
- Embedded providers (`codex`, `claude`):
  - keep session-local config options in cached runtime.
  - `SetConfigOption` calls ACP `session/set_config_option` in the same session.
  - on fresh runtime/session initialization, replay persisted `agentOptions.configOverrides` after `session/new`.
  - codex embedded runtime depends on acp-adapter compatibility with codex app-server server-request variants; current baseline includes command/file approval request compatibility (`item/commandExecution/requestApproval`, `item/fileChange/requestApproval`) with fail-closed fallback for unsupported request types.
- Stdio providers (`opencode`, `qwen`, `gemini`):
  - perform ACP handshake for config query/apply and persist resulting config state for subsequent turns.
  - on each fresh `session/new`, replay persisted `agentOptions.configOverrides` before `session/prompt`.
  - preserve existing process-per-turn streaming execution model.

### 13.4 Web UI Behavior

- Composer footer now uses thread-level config source (`/v1/threads/{id}/config-options`) for both `Model` and `Reasoning`.
- Selecting a model or reasoning value calls `POST /v1/threads/{id}/config-options` immediately.
- Reasoning choices are re-read from the latest ACP response after model changes so the control stays model-specific.
- frontend cache is keyed by `agent + selected model`, so same-agent threads can reuse the same model-specific catalog without incorrectly sharing another model's reasoning list.
- Option descriptions are rendered inside the dropdown menus for selectable values.
- During streaming or in-flight switch request, both controls are disabled to preserve turn/config safety.
- ACP `plan_update` events render as a live plan card above the active agent bubble, and the latest persisted plan is restored when reloading thread history.
- Sidebar thread actions now live behind a thread-row drawer:
  - trigger stays in the row action area.
  - drawer contains inline `Rename` and `Delete` actions.
  - rename opens an inline text field and saves through `PATCH /v1/threads/{threadId}`.
  - delete remains confirm-gated and styled as the only dangerous drawer action.

### 13.5 Catalog Persistence and Restart Refresh

- Added sqlite table `agent_config_catalogs`:
  - `agent_id`
  - `model_id`
  - `config_options_json`
  - `updated_at`
- Storage semantics:
  - one reserved row per agent stores the default snapshot used when the thread has no explicit `modelId` yet.
  - additional rows are stored per concrete model id and hold the ACP `configOptions` snapshot for that model, including model/reasoning option lists.
- Read semantics:
  - `GET /v1/threads/{threadId}/config-options` reads the persisted row matching the thread's selected model (or default row), then overlays thread-local selected values from `agentOptions.modelId` and `agentOptions.configOverrides`.
  - `GET /v1/agents/{agentId}/models` derives normalized model list from persisted catalog rows before using live discovery fallback.
- Write semantics:
  - `POST /v1/threads/{threadId}/config-options` persists:
    - thread-local current selection state into `threads.agent_options_json`
    - current model's latest `configOptions` snapshot into `agent_config_catalogs`
- Startup refresh:
  - after HTTP server initialization, the process launches a background refresher goroutine.
  - refresher queries default + per-model ACP config catalogs for all built-in agents and writes them back to sqlite.
  - refresh is best-effort and non-blocking for HTTP startup.
  - if some model refreshes fail, successful rows are upserted while older rows for failed models are preserved.

## 14. Embedded Tool-Interaction Server Request Compatibility (2026-03-06)

- Problem:
  - codex app-server can issue server requests `item/tool/requestUserInput` and `item/tool/call` during MCP/tool flows.
  - hard `-32000 not supported` responses from adapter abort these flows.
- Current behavior:
  - `item/tool/requestUserInput` is answered with a schema-compatible payload that auto-selects the first option label for each question.
  - `item/tool/call` returns a schema-compatible failure response (`success=false`, text content item), avoiding method-level RPC failure.
- Limitation:
  - this is a compatibility fallback, not full interactive tool-user-input UX.
  - multi-question/multi-select semantics and arbitrary free-text answers are not yet exposed through hub APIs/UI.

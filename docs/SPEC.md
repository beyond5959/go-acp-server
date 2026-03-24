# SPEC

## 1. Goal

Build a local-first Ngent Server that:

- serves HTTP/JSON APIs.
- streams turn events via SSE (`text/event-stream`).
- supports multi-client and multi-thread execution.
- is designed to support ACP-compatible agents (for example Claude Code, Cursor CLI, Gemini, Kimi, OpenCode, Qwen, BLACKBOX AI, Codex).
- persists interaction state/events in SQLite.
- forwards runtime permissions to the owning client with fail-closed behavior.

## 2. High-Level Architecture

Modules:

- `internal/httpapi`: routing, request validation, response/error encoding.
- `internal/runtime`: thread controller, turn state machine, cancellation coordination.
- `internal/agents`: agent providers (fake + ACP-compatible implementations), plus context-bound permission/reasoning/session/plan callback bridges.
  - per-turn provider resolution selects implementation by thread metadata (agent id + cwd).
  - `internal/agents/acpcli` is the shared ACP CLI driver used by `qwen`, `opencode`, `gemini`, `kimi`, `blackbox`, and `cursor`; provider-specific hooks own command startup, request parameter shaping, permission mapping, auth/model quirks, and cancel behavior.
- `internal/context`: prompt injection strategy assembled in HTTP/runtime path from summary + recent turns + current input.
- `internal/sse`: event formatting, stream fanout, resume helpers.
- `internal/storage`: SQLite repository and migration management.
- `internal/observability`: human-readable stderr logging, access-log formatting, ANSI-color helpers, and redaction helpers.

## 3. Concurrency Model

- `(thread, sessionId)` is the turn-execution isolation unit; empty `sessionId` is the backend's provisional "new session" state.
- the Web UI may further split one empty-session thread into client-only fresh-session scopes while the user is explicitly iterating on `New session` before ACP emits a real `session_bound`.
- Each thread/session scope has at most one active turn.
- New turn requests on an active scope return conflict error, while different sessions on the same thread may run concurrently.
- Thread-level destructive or shared-state operations (for example delete/compact and thread-wide config changes) remain whole-thread guarded.
- Cancel request transitions turn state immediately and propagates cancellation token to provider.
- Permission requests suspend the turn until a client decision arrives or timeout occurs.

## 4. Lazy Agent Startup

- On server boot: no agent process is started.
- On first thread usage: runtime requests provider instance for that thread.
- On first turn execution for embedded-provider thread (currently `codex`): server creates the in-process runtime and initializes ACP session lazily.
- Process-per-operation ACP CLI providers (`qwen`, `opencode`, `gemini`, `kimi`, `blackbox`, `cursor`) reuse the shared `acpcli` driver; each provider opens a fresh ACP stdio process per stream/config/list/discovery/transcript operation while keeping provider-specific startup hooks.
- Embedded runtime `session/new` is created with `cwd=thread.cwd` (validated as absolute path at thread creation).
- If `thread.agent_options_json` contains `modelId` / `configOverrides`, those values are the persisted desired session config for the thread.
- Provider instances are cached per thread + session/fresh-session scope and reclaimed by idle TTL (`--agent-idle-ttl`) when that scope has no active turn.
- Changing thread model/reasoning selection only updates persisted thread state; ngent applies any config diff to the cached provider when the next turn begins, immediately before `session/prompt`.
- Clearing `thread.agent_options_json.sessionId` to represent Web UI `New session` also invalidates any idle cached provider under the provisional empty-session scope so the following turn must resolve a fresh ACP session.
- Explicit Web UI `New session` also persists one internal fresh-session marker until the next `session_bound`; while that marker is set, ngent skips `[Conversation Summary]` / `[Recent Turns]` prompt injection and sends raw user input into the fresh ACP session.

## 5. Permission Bridge

Runtime permission categories:

- `command`
- `file`
- `network`
- `mcp`

Flow:

1. agent emits permission request event.
2. server persists event and forwards it to client stream/API.
   - SSE emits `permission_required` with `permissionId` and any provider-advertised `options[]`.
3. turn waits for explicit decision.
   - client submits `POST /v1/permissions/{permissionId}` with `outcome`, `optionId`, or both.
4. if decision is missing/late/invalid, default is deny (fail-closed).

Turn-side auxiliary callbacks:

- hidden reasoning/thinking deltas are forwarded separately from visible assistant text.
- reasoning is streamed/persisted as `reasoning_delta` events instead of being merged into `responseText`.
- the Web UI renders reasoning in a lightweight collapsible reasoning toggle: labeled `Thinking` during live streaming, relabeled `Thought` once finalized history is reconstructed, collapsed by default after completion, with indented left-border content when opened and sanitized markdown rendering for finalized reasoning text.
- plan replacements continue to flow as `plan_update`.

## 6. Persistence Model

SQLite stores:

- clients
- threads
- turns
- events (append-only stream records)

Properties:

- all outbound stream events are persisted before or atomically with emission strategy.
- streamed auxiliary events such as `message_content`, `reasoning_delta`, `plan_update`, and `permission_required` share the same append-only event log as `message_delta`.
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
- turn history with optional persisted event replay (`message_delta`, `message_content`, `reasoning_delta`, `plan_update`, terminal/error events)

See `docs/API.md` for endpoint and schema contracts.

## 9. Security and Runtime Constraints

- default bind: `127.0.0.1:8686` (local-only).
- public bind allowed when `--allow-public=true`.
- startup preflight determines the runtime agent allowlist; agents that fail preflight are omitted from `/v1/agents`, rejected by create-thread validation, and skipped by startup catalog refresh.
- strict input validation:
  - agent must be allowlisted.
  - cwd must be absolute.
- thread option updates that change shared thread state are rejected while any session on that thread is active; session-only selection updates are allowed while a different session is running.
- logs are human-readable on stderr and redact sensitive data.
- `--debug=true` raises log verbosity to debug level and emits sanitized ACP JSON-RPC request/response traces on stderr.
- access logs use a compact `INFO: <local-time> <client-ip> - "<method> <path> <proto>" <status> <text> <duration>` shape; ANSI colors are only used when stderr is a TTY.
- HTTP payloads contain protocol data only.

## 10A. Shared ACP CLI Driver

- The shared ACP CLI driver owns the common lifecycle for ACP-capable external CLIs:
  - open process and stdio transport
  - `initialize`
  - `session/new`, `session/load`, `session/list`, `session/prompt`
  - `session/set_config_option`
  - model discovery via `session/new`
  - transcript replay via `session/load`
- Provider-specific hooks remain responsible for:
  - command/env startup shape
  - request parameter schemas
  - permission-request response encoding
  - cancel strategy
  - provider quirks such as:
    - Kimi model/reasoning startup hints
    - Gemini/BLACKBOX stdout-noise filtering
    - Cursor ACP `authenticate(cursor_login)` and model selection via `session/set_config_option("model", ...)`
- `internal/agents/acpstdio` now supports opt-in stdout-noise tolerance so providers like Gemini can ignore non-JSON stdout lines without maintaining a separate transport implementation.

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
  - one active turn per `(thread, session)` scope.
  - fail-closed permission workflow.
  - allowlisted `agent` and absolute+allowed `cwd` validation.
  - protocol-only stdout/HTTP payloads, human-readable logs on stderr.

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
  - preserve provider option ids all the way through the hub permission flow when `options[]` are present.
  - reply with ACP outcome shape:
    - exact selected option: `{outcome:{outcome:"selected", optionId:<selected option>}}`
    - generic approve fallback: `{outcome:{outcome:"selected", optionId:<allow option>}}`
    - generic decline fallback: `{outcome:{outcome:"selected", optionId:<reject option>}}`
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
5. Update/extend tests in `cmd/ngent` for qwen registration/status.
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
  - one active turn per `(thread, session)` scope.
  - fail-closed permission workflow.
  - allowlisted `agent` and absolute+allowed `cwd` validation.
  - protocol-only stdout/HTTP payloads, human-readable logs on stderr.

### 13.2 Kimi ACP Protocol Profile

- startup command:
  - official Kimi docs currently show both `kimi acp` and `kimi --acp`.
  - the hub must try `kimi acp` first and fall back to `kimi --acp` when ACP initialization closes immediately.
- ACP flow:
  - `initialize` with `protocolVersion: 1` and `clientCapabilities.fs`.
  - `session/new` with `cwd` and `mcpServers: []`.
  - `session/prompt` with ACP prompt blocks (`[{type:"text", text:...}]`).
  - model and config catalogs are sourced directly from `session/new` / returned `configOptions`, matching the other direct ACP providers.
- streaming:
  - consume `session/update` deltas only when `update.sessionUpdate == "agent_message_chunk"`.
  - delta text is read from `update.content.text`.
  - map `update.sessionUpdate == "plan"` into hub `plan_update` SSE events; each `entries[]` payload replaces the current plan list.
- permissions and cancellation:
  - handle `session/request_permission` with fail-closed approval mapping.
  - on context cancellation, send `session/cancel` quickly and converge to `stopReason=cancelled`.
- tradeoff:
  - ACP-only metadata discovery can create provider-owned empty Kimi sessions during startup refresh or config/model probes because `session/new` is the discovery surface.

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

## 14. BLACKBOX AI ACP Integration (2026-03-22)

### 14.1 Objective and Scope

- add `blackbox` as a first-class ACP provider using the BLACKBOX CLI ACP mode.
- preserve existing runtime/security invariants:
  - one active turn per `(thread, session)` scope.
  - fail-closed permission workflow.
  - allowlisted `agent` and absolute+allowed `cwd` validation.
  - protocol-only stdout/HTTP payloads, human-readable logs on stderr.

### 14.2 Observed BLACKBOX ACP Protocol Profile

The integration follows the official ACP startup form `blackbox --experimental-acp` plus local probing of `blackbox 1.2.47`:

- startup/runtime:
  - BLACKBOX currently emits extra non-JSON stdout lines (for example process-info / telemetry errors) before or between ACP frames.
  - ngent must therefore use stdout-noise-tolerant ACP transport for BLACKBOX.
  - the hub also adds `--skip-update` and `--telemetry=false` when launching ACP mode.
- ACP requests:
  - `initialize` works with `protocolVersion: 1` and `clientCapabilities.fs`.
  - `session/new` accepts `cwd`, `mcpServers: []`, and optional model hints.
  - `session/prompt` uses ACP content blocks (`[{type:"text", text:...}]`) and accepts optional `model`.
- current upstream capability limits:
  - `initialize` currently advertises `agentCapabilities.loadSession=false`.
  - real `session/load` returns `-32601 method not found`.
  - `session/new` currently returns no `models.availableModels` or `configOptions`.
- auth:
  - ACP startup itself does not require explicitly passing `BLACKBOX_API_KEY` when the local CLI already has another usable auth method configured.
  - actual prompt execution still depends on valid BLACKBOX/upstream auth and network readiness.

### 14.3 Server Wiring

- add `internal/agents/blackbox` provider package with:
  - `Preflight()` checking `blackbox` in PATH.
  - `DiscoverModels()` wired through the shared ACP `session/new` probe, even though current upstream returns no model catalog.
  - shared structured permission handling and `session/cancel` support.
- wire `blackbox` into:
  - startup preflight diagnostics and `/v1/agents`.
  - thread agent allowlist and `TurnAgentFactory`.
  - shared model-discovery/config-catalog plumbing.
- keep session browsing/replay unsupported until upstream BLACKBOX ACP exposes `session/list` / `session/load`.

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

## 15. ACP Session Sidebar and Resume (2026-03-11)

### 15.1 Thread Metadata

- `threads.agent_options_json` now may contain:
  - `modelId`
  - `configOverrides`
  - `sessionId`
- `sessionId` is optional and represents the selected provider-owned ACP session that the thread should resume.

### 15.2 Backend API

- New endpoint: `GET /v1/threads/{threadId}/sessions`
  - ownership/tenancy matches existing thread endpoints.
  - query string:
    - `cursor` (optional): forwarded to ACP `session/list`.
  - response:
    - `threadId`
    - `supported`
    - `sessions`
    - `nextCursor`
- Session selection remains a normal thread metadata update:
  - `PATCH /v1/threads/{threadId}` with `agentOptions.sessionId="<existing>"` binds an existing ACP session.
  - `PATCH /v1/threads/{threadId}` with `agentOptions.sessionId` omitted/empty clears the binding and means "create a new session on next turn".
  - when that clear transitions from a previously bound session to empty, ngent records an internal fresh-session marker so the first turn of the new session does not inherit prior thread prompt wrappers.

### 15.3 Provider Behavior

- Built-in providers parse ACP initialize capabilities and distinguish:
  - `session/list` availability for sidebar discovery.
  - `session/load` availability for actual session resume.
- Turn setup:
  - if thread `sessionId` is present, provider calls ACP `session/load`.
  - otherwise provider calls ACP `session/new`.
  - if the thread is in explicit fresh-session mode, the first prompt sent into that new ACP session is the raw user input instead of a locally wrapped context prompt.
  - provider reports the effective session id back through a thread-scoped callback.
- Session transcript replay:
  - providers may optionally expose replayable transcript messages for one selected session through `GET /v1/threads/{threadId}/session-history`.
  - `GET /v1/threads/{threadId}/session-history` first checks SQLite `session_transcript_cache` keyed by `(agent_id, cwd, session_id)`.
  - on cache miss, `codex`, `kimi`, `opencode`, and `qwen` implement replay by calling ACP `session/load` and collecting the replayed `session/update` stream (`user_message_chunk` / `agent_message_chunk`) into displayable transcript messages.
  - successful replay snapshots, including empty transcript results, are written back to `session_transcript_cache` for reuse across later clicks and server restarts.
  - replay collectors must ignore provider-specific non-message updates (for example Qwen `tool_call_update`) instead of treating them as fatal transport errors.
  - `codex` must resolve the selected session and call `session/load` within the same embedded runtime because the raw ACP `sessionId` values returned by `session/list` are runtime-scoped.
  - replay content is provider-owned and is returned as-is from the ACP stream; ngent caches it separately from `turns/events` and does not import it into persisted SQLite turn/event history.
  - current real-provider behavior is provider-dependent:
    - `opencode`, `codex`, and `qwen` replay transcript messages over ACP `session/load`.
    - Kimi CLI 1.20.0 successfully resumes historical sessions through `session/load` but currently emits no replay `session/update` notifications for those sessions, so transcript replay stays empty under the ACP-only implementation.
- HTTP turn handling persists the bound session id back into `threads.agent_options_json` and emits SSE `session_bound`.
- HTTP turn handling also forwards ACP `session_info_update` title changes as SSE `session_info_update` events keyed by `sessionId`; `title: null` is ignored.

### 15.4 Prompt Construction

- For threads without `sessionId`, prompt construction remains unchanged:
  - inject rolling summary + recent visible turns + current user input.
- For threads with `sessionId`, prompt construction returns only the current user input.
  - rationale: ACP `session/load` already restored the provider's own transcript, so reinjecting hub-local turns would duplicate context.

### 15.5 Web UI

- Layout:
  - left edge: permanently expanded agent/thread rail plus a collapsible session panel for the active thread.
  - center: chat.
  - the agent/thread rail stays permanently expanded and always shows full thread rows and thread action controls; `New agent` sits below the list rather than in the header.
- Session panel behavior:
  - lives between the agent rail and the chat area instead of on the right edge.
  - stays hidden until an active thread is selected, so the initial empty state does not reserve session-panel width.
  - when expanded, shows the active thread title, provider badge, project path, and a full-width `New session` action before the session list.
  - can collapse independently into a slim strip with an expand affordance.
  - loads the first page automatically when a thread becomes active and the session panel is expanded.
  - shows `Show more` when `nextCursor` is present.
  - highlights the currently selected `sessionId`.
  - offers `New session` to clear `sessionId`.
  - when the active thread is already unbound, `New session` still rotates into a fresh client-side scope so the composer starts blank instead of reusing the previous anonymous buffer.
  - refreshes after turns complete so newly created/bound sessions appear in the list.
  - applies live `session_info_update.title` notifications to the matching session row immediately and keeps that runtime title override until a later notification replaces it.
  - skips server history hydration for that temporary fresh-session scope until a real ACP session id is bound back into the thread.
  - filters empty cancelled placeholders from empty-session history replay so page reload does not resurrect abandoned pre-bind attempts.

## 16. ACP Slash Commands Cache and Composer Picker (2026-03-13)

### 16.1 Backend Parsing and Persistence

- Extend shared ACP `session/update` parsing to normalize `available_commands_update` into:
  - `name`
  - `description`
  - `inputHint`
- Add a shared per-turn callback for slash-command snapshots so all built-in ACP providers reuse the same forwarding path.
- Providers must install their `session/update` observer before `session/new` or `session/load` completes, because real ACP agents can emit `available_commands_update` before the first prompt starts.
- For providers that also replay transcript chunks during `session/load`, pre-prompt `agent_message_chunk` / `user_message_chunk` updates must be ignored for live output while capability snapshots such as `available_commands_update` are still accepted.
- Embedded runtimes that do not replay already-emitted notifications to late subscribers must additionally keep a provider-local slash-command cache fed by a runtime-lifecycle update monitor, then replay that cached snapshot into the active turn before `session/prompt`; otherwise first-turn and pre-configured-session slash commands can still be lost even if prompt-time streaming is correct.
- Stdio providers using `acpstdio.Conn` must register `SetNotificationHandler` before `session/new` / `session/load`; that transport does not buffer notifications for later delivery, so early capability snapshots are otherwise dropped on the floor.
- Kimi, Qwen, OpenCode, and Gemini implement that ACP notification rule through one shared handler builder that:
  - always forwards pre-prompt `available_commands_update`.
  - suppresses pre-prompt message/plan replay.
  - forwards message chunks and plan updates only after the provider marks prompt start.
- Direct ACP providers that also expose config-session probes must reuse the same slash-command snapshot source outside streaming turns:
  - keep a provider-local `SlashCommandsCache`.
  - wrap both `Stream()` and `runConfigSession()` contexts so every observed `available_commands_update` refreshes that cache before the normal turn-scoped handler runs.
  - implement `SlashCommandsProvider` by first reading the provider-local cache and then, if still unknown, running a best-effort config session to populate it.
- Persist the latest observed slash-command snapshot in SQLite table `agent_slash_commands`:
  - `agent_id TEXT PRIMARY KEY`
  - `commands_json TEXT NOT NULL`
  - `updated_at TEXT NOT NULL`
- Persistence semantics:
  - treat each `available_commands_update` as a full replacement snapshot for that agent.
  - overwrite the existing row every time a new snapshot arrives.

### 16.2 HTTP API

- Add `GET /v1/threads/{threadId}/slash-commands`.
- Ownership model matches other thread-scoped endpoints.
- Response shape:
  - `threadId`
  - `agentId`
  - `commands`
- Read semantics:
  - resolve owned thread.
  - read cached slash commands by `thread.agent_id`.
  - if the active provider supports exposing a live slash-command snapshot and sqlite does not have one yet, initialization flows such as `GET /v1/threads/{threadId}/config-options` may backfill sqlite before the composer asks for `/slash-commands`; Codex uses this path so a fresh thread can surface slash commands before the first turn.
  - if `GET /v1/threads/{threadId}/slash-commands` still misses sqlite, the handler may probe the live provider through `SlashCommandsProvider` and persist the result before responding; this removes races between thread-open initialization and the user's first `/`.
  - return empty list when no snapshot has been observed yet.

### 16.3 Web UI

- Keep slash-command cache client-side by agent id to avoid repeated fetches across same-agent threads.
- Fetch slash commands on demand:
  - when the user types `/` at the start of the composer, load cached slash commands through `GET /v1/threads/{threadId}/slash-commands`.
  - the first bare `/` in each slash interaction must force a thread-scoped refresh even if the same agent already has an in-memory browser cache, so the UI always re-checks sqlite at slash-entry time.
  - refresh the cache again after turn completion/error/disconnect so newly streamed snapshots become visible in the composer.
- Composer behavior:
  - only show the slash-command picker when the textarea value starts with `/`.
  - typing `/` in an otherwise empty input opens the full list.
  - after the slash-entry refresh finishes, continue filtering from the refreshed in-memory snapshot until the user leaves slash mode.
  - if the fetched or cached slash-command snapshot is empty, keep `/` as ordinary message text and leave the picker hidden.
  - additional text after the first `/` filters by command name and description.
  - `ArrowUp` / `ArrowDown` move selection.
  - `Enter` inserts the highlighted command.
  - `Escape` closes the picker.
  - clicking a command inserts `/<name>` and appends a trailing space when the command advertises an input hint.

## 17. Rich ACP Permission Requests and Streaming Placeholder (2026-03-14)

### 17.1 Provider Permission Bridging

- Direct ACP providers must treat `session/request_permission.toolCall` as a structured object, not a flat string map.
- Real payloads may include:
  - `title`
  - `toolCallId`
  - `content[]` entries such as diff previews or embedded text blocks
  - `locations[]` entries with explicit `path` fields
  - `rawInput` keys such as `path`, `filepath`, or `parentDir` when the provider does not emit a richer content preview
- Permission-bridge normalization rules:
  - every direct ACP stdio provider that supports permissions must install a `HandlePermissionRequest` hook; otherwise ACP request handling falls back to JSON-RPC `method not found`
  - preserve `sessionId` and `toolCallId` in `PermissionRequest.RawParams`
  - preserve the first path-like preview when available
  - derive the Web UI badge class from the tool preview:
    - file edits/diffs -> `file`
    - directory/path previews -> `file`
    - MCP requests -> `mcp`
    - network-style requests -> `network`
    - fallback -> `command`
  - use the tool title as the primary display string in the permission card when it is specific enough; otherwise fall back to the resolved path preview
- Malformed payloads or missing handlers still fail closed, but only after structured decode is attempted.

### 17.2 Web UI Streaming State

- Before the first visible assistant delta arrives, the live streaming bubble keeps its text region empty and only shows the typing indicator.
- The first real delta populates the existing bubble without changing any other streaming semantics.
- Hidden `agent_thought_chunk` content still remains non-user-visible while the assistant is waiting on a visible delta.

## 18. ACP Tool Calls in Streaming and History (2026-03-16)

### 18.1 Backend Event Model

- Shared ACP `session/update` parsing must recognize:
  - non-text assistant `agent_message_chunk` payloads that carry structured `content`
  - `tool_call`
  - `tool_call_update`
- Parsed assistant message content keeps the ACP field structure instead of flattening to text:
  - `message_delta` remains the transport for visible text deltas
  - non-text assistant blocks become `message_content` events carrying the raw ACP `content` JSON
- Parsed tool-call events keep the ACP field structure instead of flattening to text:
  - `toolCallId`
  - optional `title`
  - optional `kind`
  - optional `status`
  - optional raw JSON `content`
  - optional raw JSON `locations`
  - optional raw JSON `rawInput`
  - optional raw JSON `rawOutput`
- Providers bridge those parsed events through one per-turn callback, just like plan/reasoning callbacks.
- HTTP turn handling persists and streams the same event names with:
  - `message_content`: `turnId` plus the raw ACP `content`
  - `tool_call` / `tool_call_update`: `turnId` plus the ACP tool-call fields listed above
- Event persistence remains append-only at the turn-event layer; ngent does not store a second derived tool-call snapshot table.

### 18.2 History Reconstruction

- `GET /v1/threads/{threadId}/history?includeEvents=true` returns the persisted `message_content`, `tool_call`, and `tool_call_update` events unchanged in turn event history.
- Tool-call updates are partial replacements:
  - clients must merge them by `toolCallId`
  - omitted fields mean "leave previous value unchanged"
  - explicitly present empty string / empty array / `null` payloads mean "clear or replace with empty value"
- Session transcript replay stays separate from tool-call history:
  - `session/load` replay collectors still ignore tool-call notifications because transcript replay only reconstructs user/assistant message content.
  - provider-owned transcript replay currently remains text-only, so non-text assistant content blocks are preserved only for hub-created turns whose normal turn events were persisted by ngent.

### 18.3 Web UI

- Assistant rendering is timeline-based:
  - `message_delta` becomes visible assistant content segments
  - `message_content` becomes structured assistant content segments
  - `reasoning_delta` / `thought_delta` becomes thinking segments
  - `tool_call` / `tool_call_update` becomes tool segments
- Visible assistant content segments render as plain answer blocks, not agent chat bubbles.
- The transcript pane is single-column:
  - user prompts render as prompt blocks with a subtle background and border
  - assistant timeline blocks render directly underneath
  - the UI does not use IM-style right-aligned user bubbles or left-aligned agent bubbles
- Stream state keeps one per-scope ordered segment list plus stable tool-call identity keyed by `toolCallId`.
- Live SSE updates append or mutate those segments in place, so the chat pane can render `Thought -> Tool -> Thought -> Answer` instead of one concentrated reasoning/tool section.
- While a turn is still streaming, only the active answer segment stays in plain-text typing mode; older answer segments switch to finalized markdown rendering immediately when the stream moves on.
- While a turn is still streaming, only the active thought segment remains expanded; older thought segments collapse immediately once a later event switches the stream out of reasoning.
- While a turn is still streaming, only the active tool-call segment remains expanded; older/finalized tool-call segments render as collapsed disclosure panels that can be reopened manually.
- Finalized message history rebuilds the same ordered segment list by replaying persisted turn events in sequence order.
- Finalized assistant content segments expose their own copy action and copy only that segment's raw text; the assistant message footer no longer copies the entire aggregated answer string.
- Each finalized answer segment renders its own local meta row with timestamp first and then that segment's copy control; the control still copies only that segment's text.
- Markdown tables in finalized answer/thought content use explicit transcript styles (borders, header background, zebra rows, horizontal overflow) rather than browser-default table rendering.
- Finalized markdown tables are wrapped in a fit-content scroll container so the border hugs the table's natural width instead of stretching across the full message column.
- `tool_call_update` mutates the first segment for the matching `toolCallId`; it does not append a second duplicate tool block.
- Permission-request cards are not rendered as tool-call segments and do not participate in tool/thought collapse state.
- `plan_update` remains a separate replace-style plan card above the ordered assistant segments.
- The Web UI renders:
  - assistant image content cards
  - assistant embedded resource cards with URI/text previews when available
  - title / kind / status badges
  - text content blocks
  - command blocks
  - diff before/after blocks
  - path/location lists
  - raw JSON input/output blocks
- Unsupported non-text ACP tool-call payload shapes are still shown via generic JSON fallback so the information is visible even when no richer renderer exists yet.
- Unsupported ordinary assistant content block shapes are still shown via generic JSON fallback so the information remains visible even before a richer renderer exists.

### 18.4 Session-Driven Config Discovery

- ngent no longer opens probe-only `session/new` calls during startup or while serving `GET /v1/threads/{threadId}/config-options` / `GET /v1/agents/{agentId}/models`.
- Model/reasoning metadata now comes only from real user-triggered session lifecycle events:
  - stdio ACP providers extract `configOptions` from the actual `session/new` / `session/load` response used by `Stream()`.
  - embedded `codex` / `claude` do the same inside their runtime initialization path and emit the same shared callback.
  - session-transcript replay paths (`GET /v1/threads/{threadId}/session-history?sessionId=...`) may also extract `configOptions` from their user-triggered `session/load` response when the provider returns them.
- The HTTP turn handler treats that callback as authoritative and persists the snapshot immediately:
  - thread `agentOptions.modelId` / `agentOptions.configOverrides` are rewritten to match the session's actual current config.
  - the normalized snapshot is stored in `agent_config_catalogs` under the actual current model id.
  - when a `sessionId` is already known, the same normalized snapshot is also stored in `session_config_cache(agent_id, cwd, session_id, config_options_json, updated_at)`.
  - if config arrives before `session_bound`, ngent writes the session cache entry immediately after binding by replaying the just-learned thread/model snapshot onto the new `sessionId`.
- Existing-session selection clears stale thread-local model/reasoning selections before the next user-triggered `session/load`, so the loaded session's real state can repopulate the thread instead of inheriting the previous session's config.
- `GET /v1/threads/{threadId}/config-options` is now stored-only:
  - if the thread already has a concrete `modelId` and sqlite has a matching catalog row, return that row overlaid with the thread's current selections.
  - otherwise, if the thread points at a concrete `sessionId` and sqlite has a matching `session_config_cache` row, return that session snapshot directly.
  - otherwise return an empty `configOptions` list instead of probing upstream.
- `GET /v1/threads/{threadId}/session-history` keeps using cached transcript replay when possible, except:
  - if transcript cache exists but `session_config_cache` is still missing for the requested session, ngent performs one live `session/load` so that the user-triggered session switch can learn config metadata.
- `POST /v1/threads/{threadId}/config-options` now requires the thread to have already learned a concrete config snapshot; before that point it returns `409 CONFLICT` rather than opening a probe session.
- `/v1/agents/{agentId}/models` now returns only models already learned into sqlite; it may be empty for a brand-new agent until at least one real turn reports config metadata.
- The Web UI follows the same lifecycle:
  - model/reasoning controls stay hidden until the thread has a real config snapshot.
  - after a turn completes (or fails/disconnects after session init), the UI reloads stored config options and reveals the controls if metadata is now known.
  - switching to a different session clears the thread-local config cache so stale controls do not linger before the destination session cache or next user-triggered `session/load` repopulates the controls.
  - after session-history replay finishes for the selected session, the UI refreshes stored config options again so controls can appear immediately if that replay taught sqlite a new session snapshot.

### 18.5 Web UI Uploads and ACP `resource_link`

- The turn pipeline now accepts structured user prompt content:
  - text blocks
  - ACP `resource_link` blocks with local `file://` URIs
- HTTP request handling:
  - `POST /v1/threads/{threadId}/turns` continues to accept JSON `{input,stream}` for text-only callers.
  - the same endpoint also accepts `multipart/form-data` with:
    - `input`: optional text
    - `stream=true`
    - one or more `attachments` file parts
  - each uploaded file is persisted into the local temp directory and converted into one normalized prompt item:
    - `type=resource_link`
    - `uri=file:///...`
    - `name=<original base filename>`
    - `mimeType=<parsed/sniffed MIME>`
    - `size=<bytes copied>`
- Prompt assembly:
  - the agent layer now uses a shared structured prompt model instead of forcing every turn through one string.
  - when ngent injects summary/recent-turn context, it rewrites only the text portion of the prompt and preserves resource links as separate items.
  - non-ACP/fallback streamers still receive a plain-text representation that includes an `[Attached Resources]` section.
- Persistence:
  - `turns.request_text` stores the plain-text fallback representation so context-compaction and non-ACP flows still retain attachment references.
  - turns with uploaded resources also persist a `user_prompt` event containing the original structured prompt array for history/UI reconstruction.
- Web UI:
  - the composer footer order is `Attachment -> Model -> Reasoning` on the left, mirrored by `Send` on the right.
  - attachments are held in thread-local in-memory draft state until send, with image previews when possible.
  - the transcript renders sent user attachments as cards and rebuilds them from `user_prompt` history events after reload.

# DECISIONS

## ADR Index

- ADR-001: HTTP/JSON API with SSE streaming transport. (Accepted)
- ADR-002: Client identity via `X-Client-ID` header. (Accepted)
- ADR-003: SQLite append-only events table as interaction source of truth. (Accepted)
- ADR-004: Permission handling defaults to fail-closed. (Accepted)
- ADR-005: Default bind is localhost only. (Superseded)
- ADR-006: M1 API baseline for health/auth/agents. (Accepted)
- ADR-007: M3 thread API tenancy and path policy. (Accepted)
- ADR-008: M4 turn streaming over SSE with persisted event log. (Accepted)
- ADR-009: M5 ACP stdio provider and permission bridge. (Accepted)
- ADR-010: M6 codex-acp-go runtime wiring. (Superseded)
- ADR-011: M7 context window injection and compact policy. (Accepted)
- ADR-012: M8 reliability alignment (TTL, shutdown, error codes). (Accepted)
- ADR-013: Canonical Go module path finalization. (Accepted)
- ADR-014: Codex provider migration from sidecar binary to embedded library. (Accepted)
- ADR-015: First-turn prompt passthrough for slash-command compatibility in embedded codex mode. (Accepted)
- ADR-016: Remove `--allowed-root` runtime parameter and default to absolute-cwd policy. (Accepted)
- ADR-017: Human-readable startup summary and request completion access logs. (Accepted)
- ADR-018: Embedded Web UI via Go embed. (Accepted)
- ADR-019: OpenCode ACP stdio provider. (Accepted)
- ADR-020: Gemini CLI ACP stdio provider. (Accepted)
- ADR-021: Public-by-default bind with local-only opt-out. (Accepted)
- ADR-022: Qwen Code ACP stdio provider integration. (Accepted)
- ADR-023: Shared ACP stdio transport for OpenCode and Qwen providers. (Accepted)
- ADR-024: Claude Code embedded provider via claudeacp runtime. (Accepted)
- ADR-025: Hard-delete thread endpoint with active-turn lock. (Accepted)
- ADR-026: Thread-level model override update API and provider reset. (Accepted)
- ADR-027: ACP-backed agent model catalog endpoint and UI dropdown wiring. (Accepted)
- ADR-028: Persist thread config overrides and surface reasoning control in Web UI. (Accepted)
- ADR-029: Consolidate sidebar thread actions into a drawer and reuse thread patch for rename. (Accepted)
- ADR-030: Pin local acp-adapter hotfix for codex app-server server-request compatibility. (Accepted)
- ADR-031: Kimi CLI ACP stdio provider with dual startup syntax fallback. (Accepted)
- ADR-032: Shared common agent config/state helper without protocol unification. (Accepted)
- ADR-033: Surface ACP plan updates as first-class SSE and Web UI state. (Accepted)
- ADR-034: Source Kimi config catalogs from local config to avoid empty sessions. (Accepted)
- ADR-035: Add opt-in ACP debug tracing behind `--debug`. (Accepted)
- ADR-036: Persist stable Codex session ids and normalize Codex transcript replay. (Superseded)
- ADR-037: Replay Kimi session history from local Kimi session files. (Superseded)
- ADR-038: Replay OpenCode session history from local OpenCode SQLite storage. (Superseded)
- ADR-039: Standardize session-history on ACP `session/load` replay. (Accepted)
- ADR-040: Cache session-history replay snapshots in SQLite. (Accepted)
- ADR-041: Treat Web UI "New session" as provider-cache reset for the empty session scope. (Accepted)
- ADR-042: Treat explicit Web UI "New session" as a fresh turn with no injected thread context. (Accepted)

## ADR-042: Treat Explicit Web UI "New session" as a Fresh Turn with No Injected Thread Context

- Status: Accepted
- Date: 2026-03-13
- Context:
  - after ADR-041, real Codex + Playwright validation on 2026-03-13 still reproduced the user-visible bug: selecting historical session `B`, clicking `New session`, then sending a prompt produced a new ACP `sessionId`, but the first prompt still contained `[Conversation Summary]` / `[Recent Turns]` from thread session `A`.
  - the root cause was `buildInjectedPrompt()`: ngent treats any empty `sessionId` as a local-context continuation and wraps the next prompt with prior thread turns.
  - disabling context injection for all empty-session turns would be too broad because brand-new no-session threads still benefit from local prompt continuation after earlier turns.
- Decision:
  - persist one internal thread option `_ngentFreshSession=true` only when an existing non-empty `sessionId` is explicitly cleared through the Web UI `New session` flow.
  - while `_ngentFreshSession=true` and `sessionId` is empty, bypass local context injection and send the raw user input into ACP `session/new` / `session/prompt`.
  - automatically clear `_ngentFreshSession` when a new session binds or when the user selects an explicit existing `sessionId`.
  - strip `_ngentFreshSession` from public thread responses and from session-only diff checks so the flag remains server-internal and does not change client-visible API semantics.
- Consequences:
  - explicit `New session` now means a blank fresh ACP session from the user's perspective, not merely a new durable `sessionId`.
  - ordinary threads that have no bound session for other reasons still keep existing context-window behavior.
  - stale plain-empty and fresh-empty cached providers are both evicted on explicit reset, so neither scope can silently reuse the wrong runtime.
- Alternatives considered:
  - disable context injection for every empty-session turn.
  - expose a dedicated public `session/new` endpoint and model fresh-session state outside `PATCH /v1/threads/{threadId}`.
  - leave the internal fresh-session marker in API responses.

## ADR-041: Treat Web UI "New session" as Provider-Cache Reset for the Empty Session Scope

- Status: Accepted
- Date: 2026-03-13
- Context:
  - the Web UI uses `PATCH /v1/threads/{threadId}` with `agentOptions.sessionId` cleared to represent "New session", and the next turn is expected to force ACP `session/new`.
  - ngent caches managed providers by `(threadId, normalized agentOptions)` scope.
  - if a stale provider is still cached under the empty-session scope, simply clearing `sessionId` can reuse that provider and route the next prompt into an older ACP session instead of a fresh one.
- Decision:
  - when a session-only thread update changes `sessionId` from a non-empty value to empty, evict any idle cached provider for the target empty-session scope before the next turn starts.
  - keep the current selected-session provider cache intact; only the provisional empty-session scope is force-reset.
  - keep the Web UI composer disabled while a session switch request is in flight so the user cannot submit a turn against stale selection state.
- Consequences:
  - `New session` semantics become deterministic: the next turn must resolve a fresh provider/session for the empty scope.
  - existing cached providers for explicit session ids remain reusable, so historical-session switching and multi-session concurrency are preserved.
  - the empty-session-scope eviction is intentionally narrow and skips active turns.
- Alternatives considered:
  - optimistically trust that no stale empty-scope provider can exist.
  - close every cached provider on any session selection update.
  - add a dedicated `session/new` API endpoint instead of continuing to encode "new session" as `sessionId=""`.

## ADR-040: Cache Session-History Replay Snapshots in SQLite

- Status: Accepted
- Date: 2026-03-13
- Context:
  - the Web UI requests `GET /v1/threads/{threadId}/session-history` whenever the user selects a historical ACP session in the right sidebar.
  - after ADR-039, every selection hit the provider again through ACP `session/load`, even if ngent had already replayed that exact provider session earlier.
  - repeated `session/load` calls add avoidable latency, reopen provider processes/runtimes, and make session browsing depend on live provider availability even for transcript content already observed locally.
  - the product still does not want to import provider-owned replay into hub `turns/events`, because that would blur the boundary between provider-owned history and ngent-created turns.
- Decision:
  - add SQLite table `session_transcript_cache(agent_id, cwd, session_id, messages_json, updated_at)`.
  - key cached transcript snapshots by provider session identity `(agent_id, cwd, session_id)` so the same session replay can be reused across threads and across server restarts.
  - make `GET /v1/threads/{threadId}/session-history` read sqlite first; only call provider `LoadSessionTranscript` on cache miss.
  - write successful replay results, including empty transcript snapshots, back into sqlite after the provider call completes.
  - keep cache write failures non-fatal to the API response; the replay request itself should still succeed when the provider load succeeded.
- Consequences:
  - repeated historical session selection becomes local-first after the first successful replay.
  - session replay browsing survives server restart without requiring a new provider `session/load`.
  - provider-owned replay remains separate from durable hub `turns/events`; `/history` semantics do not change.
  - cache freshness is currently write-on-miss/write-on-success only; ngent does not yet compare provider `updatedAt` metadata before serving the cached snapshot.
- Alternatives considered:
  - keep always hitting provider `session/load` for every selection.
  - import replayed transcript into `turns/events` instead of caching a separate snapshot table.
  - key cache rows by `thread_id` only (rejected because the same provider session should be reusable across threads).
- Follow-up actions:
  - persist `session/list.updatedAt` metadata and refresh cached snapshots when the provider session advances.
  - evaluate whether a merged history view should combine cached provider replay with hub-local turns without duplicating context.

## ADR-039: Standardize Session-History on ACP `session/load` Replay

- Status: Accepted
- Date: 2026-03-12
- Context:
  - the Web UI already uses one generic `GET /v1/threads/{threadId}/session-history` flow after session sidebar selection.
  - ACP `session/load` standard behavior replays prior conversation through `session/update` notifications before the RPC returns.
  - earlier ngent implementations reconstructed replay from provider-local files or databases, which diverged from ACP and created provider-specific behavior that the product no longer wants.
  - real-provider validation showed important runtime nuances:
    - Codex raw ACP `sessionId` values returned by `session/list` are scoped to the same embedded runtime and cannot be safely reused across a second runtime.
    - OpenCode replays transcript correctly over ACP `session/load`.
    - Qwen replays transcript correctly over ACP `session/load` for locally created sessions.
    - Qwen historical replay also includes non-message notifications such as `tool_call_update`, whose `content` payload does not follow the text-chunk schema.
    - Kimi CLI 1.20.0 resumes historical sessions over ACP `session/load`, but currently emits no replay transcript updates for those historical loads.
- Decision:
  - implement `SessionTranscriptLoader` for `codex`, `kimi`, `opencode`, and `qwen` by:
    - resolving the requested session through ACP `session/list`
    - calling ACP `session/load`
    - collecting replayed `user_message_chunk` and `agent_message_chunk` updates into transcript messages
  - add shared ACP update parsing and a shared replay collector for `session/load` transcript reconstruction.
  - keep the shared replay parser tolerant of non-message `session/update` variants so provider-specific tool/metadata updates do not abort transcript replay.
  - for Codex, resolve and load the session within the same embedded runtime so runtime-scoped raw session ids remain valid.
  - do not add provider-local transcript fallbacks behind the standard ACP path.
- Consequences:
  - `session-history` behavior now follows ACP semantics instead of provider-local storage formats.
  - OpenCode, Codex, and Qwen replay their provider-owned transcript through standard ACP `session/load`.
  - providers may interleave replayable text chunks with tool or metadata updates; ngent now ignores those non-message updates during transcript reconstruction instead of treating them as transport errors.
  - Kimi currently remains limited by upstream behavior: historical `session/load` succeeds but yields no replay transcript messages on CLI 1.20.0.
  - Codex replay now reflects raw provider-owned prompt content, including wrapper text that was previously normalized away.
- Alternatives considered:
  - keep provider-local transcript parsing for Kimi/OpenCode/Codex/Qwen.
  - mix ACP `session/load` with local fallback parsing when replay updates are absent.
  - add a non-standard transcript import step into SQLite.
- Follow-up actions:
  - keep validating newer Kimi CLI releases for proper historical replay over standard ACP `session/load`.
  - decide later whether Codex replay text should be normalized again despite the ACP-first policy.

## ADR-038: Replay OpenCode Session History from Local OpenCode SQLite Storage

- Status: Accepted
- Date: 2026-03-12
- Context:
  - the Web UI uses the same generic `GET /v1/threads/{threadId}/session-history` flow after session sidebar selection for all ACP agents.
  - real OpenCode validation showed `session/list` and `session/load` both worked, but `opencode` exposed no `SessionTranscriptLoader`, so `/session-history` always returned `supported=false`.
  - OpenCode stores replayable session content locally in `XDG_DATA_HOME/opencode/opencode.db`, with session metadata in `session` and visible text split across `message` + `part` rows.
- Decision:
  - implement `agents.SessionTranscriptLoader` for `internal/agents/opencode`.
  - read the local OpenCode SQLite database directly in read-only mode instead of shelling out to `opencode export`.
  - reconstruct transcript messages by:
    - selecting `user` / `assistant` rows from `message`.
    - appending only `part.type == "text"` payloads in insertion order.
    - dropping `reasoning`, `tool`, `step-start`, and `step-finish` parts.
  - reject transcript loads whose stored OpenCode `session.directory` does not match the active thread cwd.
- Consequences:
  - selecting an existing OpenCode session now replays provider-owned history in the center chat pane.
  - replay no longer depends on OpenCode CLI subcommands that may mutate state or fail due local DB write behavior.
  - the implementation now depends on OpenCode's local DB schema remaining compatible enough for read-only transcript reconstruction.
- Alternatives considered:
  - keep OpenCode session replay unsupported and rely only on ngent-local history.
  - invoke `opencode export <sessionID>` for transcript reconstruction.
  - parse only `session_diff` JSON files, even though they do not contain full chat transcript.
- Follow-up actions:
  - monitor future OpenCode schema changes and add a CLI-export fallback if the local DB layout becomes incompatible.

## ADR-037: Replay Kimi Session History from Local Kimi Session Files

- Status: Accepted
- Date: 2026-03-12
- Context:
  - the Web UI already requests `GET /v1/threads/{threadId}/session-history` after the user selects a provider-owned session from the right sidebar.
  - real Kimi debugging showed the backend successfully persisted and resumed `sessionId` through ACP `session/load`, but `kimi` exposed no `SessionTranscriptLoader`, so `/session-history` always returned `supported=false`.
  - Kimi stores replayable chat history locally in `KIMI_HOME/sessions/*/<sessionId>/context.jsonl` for listable historical sessions, with assistant `think` blocks interleaved alongside visible text.
- Decision:
  - implement `agents.SessionTranscriptLoader` for `internal/agents/kimi`.
  - resolve transcript files from the local Kimi home directory and parse `context.jsonl` directly instead of trying to reconstruct transcript from hub-local turns.
  - keep only visible `user` / `assistant` text in replay payloads and drop `_checkpoint`, `_usage`, tool messages, and assistant `think` blocks before returning the transcript to the Web UI.
- Consequences:
  - selecting an existing Kimi session now replays provider-owned history in the center chat pane, matching the existing Codex session browsing behavior.
  - the replay remains on-demand only; ngent still does not import provider transcript into persisted SQLite turns/events.
  - fresh ACP-created Kimi sessions may still resume before they become visible in Kimi's own list/export surfaces; that remains an upstream/runtime limitation.
- Alternatives considered:
  - leave Kimi session replay unsupported and rely only on ngent-local history.
  - shell out to `kimi export` for every replay request instead of reading local session files.
  - attempt transcript reconstruction from `session/load` side effects during the next prompt.
- Follow-up actions:
  - monitor future Kimi CLI layout changes and add an export-based fallback if the local session directory format stops being stable enough for direct parsing.

## ADR-036: Persist Stable Codex Session IDs and Normalize Codex Transcript Replay

- Status: Accepted
- Date: 2026-03-11
- Context:
  - embedded Codex `session/new` can initially return only a provisional runtime-scoped id like `session-1`, while the durable session identity arrives later via `session/list` metadata (`_meta.threadId`).
  - persisting the provisional id caused fresh `New session` turns to collapse back onto the same thread session binding.
  - Codex transcript files also include wrapper-generated user messages for bootstrap context (`AGENTS.md`, `environment_context`) and prompt wrappers (`[Current User Input]`, IDE setup metadata), which polluted Web UI session replay.
- Decision:
  - treat the durable Codex session id as the canonical thread binding and defer fresh-session `session_bound` persistence/emission when the only known id still matches the provisional raw runtime id.
  - after the first prompt completes, retry `session/list` briefly to resolve the stable id, then update in-memory client state and persisted thread `agentOptions.sessionId` with that durable id.
  - normalize Codex session transcript replay on the backend before returning it to the Web UI:
    - drop bootstrap wrapper messages injected by the desktop environment.
    - extract the actual user prompt from known wrapper formats such as `[Current User Input]` and `## My request for Codex:`.
- Consequences:
  - fresh `New session` flows now bind to durable Codex session identities and no longer merge unrelated turns under one provisional raw id.
  - Web UI session replay shows user-visible prompts instead of provider/bootstrap scaffolding.
  - session list titles from provider metadata remain raw for now; only replayed message bodies are normalized.
- Alternatives considered:
  - persist the first raw runtime id immediately and rely on later correction.
  - leave transcript normalization to the frontend merge logic.
- Follow-up actions:
  - evaluate normalizing Codex `session/list` titles/previews in the backend so the session sidebar also hides wrapper-generated summary text.

## ADR-018: Embedded Web UI via Go embed

- Status: Accepted
- Date: 2026-02-28
- Context: users need a visual client to interact with the Agent Hub without writing curl commands or building a separate frontend project.
- Decision:
  - add a Vite + TypeScript (no framework) frontend under `web/src/`.
  - build output lands in `web/dist/`, embedded via `//go:embed web/dist` in `internal/webui/webui.go`.
  - register `GET /` and `GET /assets/*` in `httpapi` (lower priority than all `/v1/*` and `/healthz` routes).
  - SPA fallback: any non-API path returns `index.html`.
  - `make build-web` produces the dist; `web/dist` is committed so users without Node.js can still `go build`.
- Consequences: single-binary distribution with no external file dependencies; Go binary size increases by the size of the minified JS/CSS bundle (~200–400 KB estimated). Build pipeline requires Node.js for frontend changes.
- Alternatives considered: separate static file directory (requires deployment of two artifacts); WebSocket-only SPA (rejected: SSE already implemented); React/Vue framework (rejected: adds runtime bundle weight and build complexity).
- Follow-up actions: add `npm run build` to CI pipeline; version-pin Node.js in project tooling docs.

## ADR-025: Hard-delete Thread Endpoint with Active-turn Lock

- Status: Accepted
- Date: 2026-03-03
- Context: users need to clean historical threads from both API and Web UI, while preserving the one-active-turn-per-thread guarantee and avoiding partial deletes.
- Decision:
  - add `DELETE /v1/threads/{threadId}` with ownership enforcement based on `X-Client-ID`.
  - return `409 CONFLICT` when the target thread currently has an active turn.
  - reserve a temporary turn-controller slot during deletion so no new turn can start on that thread while delete is in progress.
  - perform storage deletion in one transaction with explicit dependency order: `events` -> `turns` -> `threads`.
  - close and evict cached per-thread agent provider after successful delete.
- Consequences: deletion is deterministic and race-safe with active turn startup, but remains irreversible (no soft-delete/recover endpoint).
- Alternatives considered: soft-delete tombstone model, relying only on foreign-key cascades, and best-effort delete without turn-controller lock.
- Follow-up actions: add optional audit trail for delete operations if compliance requirements increase.

## ADR-028: Persist Thread Config Overrides and Surface Reasoning Control in Web UI

- Status: Accepted
- Date: 2026-03-06
- Context:
  - thread-scoped `config-options` support already enabled immediate model switching, but non-model settings like reasoning only lived inside the current ACP session.
  - for stdio providers (`opencode`, `qwen`, `gemini`), a reasoning change would otherwise disappear on the next turn because each turn starts a fresh ACP process.
  - Web UI only surfaced model in the composer footer, even though ACP can report model-specific reasoning choices in the same `configOptions` payload.
- Decision:
  - persist non-model current values returned by `POST /v1/threads/{threadId}/config-options` into `agentOptions.configOverrides`.
  - keep `agentOptions.modelId` as the durable model mirror, and store other current config values by config id in `configOverrides`.
  - update all built-in providers to reapply persisted non-model overrides on new ACP sessions:
    - embedded (`codex`, `claude`) reapply overrides after `session/new` on cached runtime initialization.
    - stdio (`opencode`, `qwen`, `gemini`, `kimi`) reapply overrides after `session/new` before `session/prompt`.
  - extend the Web UI composer footer to show both `Model` and `Reasoning` controls sourced from thread `configOptions`, with reasoning options refreshed after model changes.
  - cache config catalogs in the Web UI by agent id so same-agent threads reuse one shared model/reasoning option list without re-querying on every thread switch.
- Consequences:
  - reasoning-style settings now survive across future turns and restart/provider reinitialization boundaries.
  - reasoning remains model-specific because the UI always redraws from the latest ACP-reported thread config options after model changes.
  - switching between threads on the same agent is cheaper because the UI reuses the cached agent catalog and only applies thread-specific current values locally.
  - other non-model config categories are persisted server-side but are not yet surfaced as first-class UI controls beyond reasoning.
- Alternatives considered:
  - UI-only reasoning selector without persistence.
  - provider-specific persistence logic for reasoning rather than generic thread config override storage.
- Follow-up actions:
  - evaluate surfacing additional ACP config categories in the UI when product requirements justify more advanced controls.

## ADR-026: Thread-level Model Override Update API and Provider Reset

- Status: Accepted
- Date: 2026-03-05
- Context: users need to choose model at thread creation time and switch model on existing threads from Web UI/API without breaking one-active-turn-per-thread guarantees.
- Decision:
  - standardize thread-level model override as `agentOptions.modelId`.
  - add `PATCH /v1/threads/{threadId}` to update `agentOptions` for owned threads.
  - reject updates with `409 CONFLICT` while the thread has an active turn.
  - close cached per-thread provider after successful update so next turn re-initializes provider/session with new model config.
  - wire `modelId` into all provider factories; embedded `codex`/`claude` pass it to ACP `session/new` as `model`; `gemini` passes it to `session/new` and `session/prompt`; `opencode` passes `modelId` in `session/prompt`; `qwen` passes `model` in `session/prompt`.
- Consequences: model switching becomes explicit and deterministic at thread boundary; switching takes effect on next turn (not mid-turn) and may incur one provider re-init.
- Alternatives considered: in-session mutable model switching without provider reset, and provider-specific update endpoints.
- Follow-up actions: expose optional model catalogs per agent for richer dropdown UX and validate model ids against runtime-reported config options when available.

## ADR-027: ACP-backed Agent Model Catalog Endpoint and UI Dropdown Wiring

- Status: Accepted
- Date: 2026-03-05
- Context: Web UI hardcoded model lists drift from runtime reality; users need model options sourced directly from each agent's ACP runtime so create/switch flows stay accurate across codex/claude/opencode/gemini/kimi/qwen.
- Decision:
  - add `GET /v1/agents/{agentId}/models` and wire it to a backend `AgentModelsFactory`.
  - implement per-agent ACP discovery handshake (`initialize` + `session/new`) and parse model options from:
    - `session/new.configOptions` (`id=model`) for embedded codex/claude (acp-adapter latest).
    - `session/new.models.availableModels` for opencode/gemini/kimi/qwen.
  - normalize response shape to `[{id,name}]`, de-duplicate by `id`, and return `503 UPSTREAM_UNAVAILABLE` on discovery failure.
  - replace active-thread free-text model control with dropdown powered by the new endpoint:
    - active-thread header switches via dropdown + `PATCH /v1/threads/{threadId}`.
  - keep new-thread modal focused on agent/cwd/title creation and advanced JSON (no dedicated model selector).
- Consequences: model selection UX is runtime-accurate and provider-specific without frontend hardcoding; failure mode is explicit (upstream unavailable) and localized to model discovery.
- Alternatives considered: keep hardcoded frontend catalogs, or validate only at prompt-time without exposing a catalog endpoint.
- Follow-up actions: optionally add short-lived server-side model catalog cache and server-side create/update validation against discovered options.

## ADR Template

Use this template for new decisions.

```text
# ADR-XXX: <title>
- Status: Proposed | Accepted | Superseded
- Date: YYYY-MM-DD
- Context:
- Decision:
- Consequences:
- Alternatives considered:
- Follow-up actions:
```

## ADR-001: HTTP/JSON API with SSE Streaming

- Status: Accepted
- Date: 2026-02-28
- Context: turn output is incremental and long-running; clients need low-latency updates.
- Decision: use HTTP/JSON for request-response operations and SSE for server-to-client event streaming.
- Consequences: simpler client/network compatibility than WebSocket for one-way stream; requires reconnect/resume handling.
- Alternatives considered: WebSocket-only transport, polling.
- Follow-up actions: define event replay semantics and heartbeat policy.

## ADR-002: Client Identity via `X-Client-ID`

- Status: Accepted
- Date: 2026-02-28
- Context: server must isolate resources across multiple clients.
- Decision: require `X-Client-ID` on authenticated endpoints and scope data by that identity.
- Consequences: easy stateless routing and testing; header contract must be documented and validated strictly.
- Alternatives considered: query parameter id, session cookie.
- Follow-up actions: add optional auth token binding for production mode.

## ADR-003: SQLite Events as Source of Truth

- Status: Accepted
- Date: 2026-02-28
- Context: stream continuity and restart recovery require durable event history.
- Decision: persist turn events in append-only `events` table, indexed by thread/turn and sequence.
- Consequences: enables replay and audits; requires careful handling of SQLite contention.
- Alternatives considered: in-memory stream only, external queue.
- Follow-up actions: implement WAL mode, busy timeout, and compaction policy.

## ADR-004: Permission Workflow Fail-Closed

- Status: Accepted
- Date: 2026-02-28
- Context: runtime permissions are security-sensitive.
- Decision: when client decision is missing/invalid/late, default to deny.
- Consequences: safer default posture; may interrupt slow clients.
- Alternatives considered: fail-open with audit warning.
- Follow-up actions: add configurable timeout and clear UX hints.

## ADR-005: Localhost-by-Default Network Policy

- Status: Superseded by ADR-021
- Date: 2026-02-28
- Context: server may expose local filesystem and command capabilities.
- Decision: default bind `127.0.0.1:8686`; require explicit `--allow-public=true` for public interfaces.
- Consequences: secure local default; remote access requires intentional operator action.
- Alternatives considered: public by default.
- Follow-up actions: add warning log when public bind is enabled.

## ADR-006: M1 API Baseline for Health/Auth/Agents

- Status: Accepted
- Date: 2026-02-28
- Context: M1 requires a minimal but stable API contract before thread/turn APIs are implemented.
- Decision:
  - define `GET /healthz` response as `{ "ok": true }`.
  - define `GET /v1/agents` response key as `agents` with `id/name/status` fields.
  - gate only `/v1/*` endpoints behind optional bearer token (`--auth-token`).
  - standardize error envelope as `{ "error": { "code", "message", "details" } }`.
- Consequences: contract is simpler for clients and tests; request-id/hint fields are deferred to later milestones.
- Alternatives considered: keep earlier draft response schemas with extra fields.
- Follow-up actions: extend same error envelope to all future endpoints and document additional error codes as APIs expand.

## ADR-007: M3 Thread API Tenancy and Path Policy

- Status: Superseded by ADR-016
- Date: 2026-02-28
- Context: thread APIs introduce per-client resource ownership and filesystem-scoped execution context.
- Decision:
  - require `X-Client-ID` on all `/v1/*` endpoints and upsert client heartbeat on each request.
  - enforce `cwd` as absolute path under configured allowed roots.
  - return `404` for cross-client thread access to avoid existence leakage.
  - thread creation only persists metadata and does not start any agent process.
- Consequences: stronger tenancy boundaries and safer path policy at API edge; clients must always include identity header.
- Alternatives considered: permissive cross-client errors (`403`) and late validation in turn execution stage.
- Follow-up actions: wire same tenancy/path checks through turns and permission endpoints in M4+.

## ADR-008: M4 Turn Streaming over SSE with Persisted Event Log

- Status: Accepted
- Date: 2026-02-28
- Context: turns must stream incremental output while preserving durable history, cancellation, and per-thread single-active constraints.
- Decision:
  - use `POST /v1/threads/{threadId}/turns` as SSE response endpoint.
  - persist each emitted SSE event (`turn_started`, `message_delta`, `turn_completed`, `error`) into `events` table.
  - enforce one active turn per thread with in-memory controller; concurrent start on same thread returns `409 CONFLICT`.
  - implement `POST /v1/turns/{turnId}/cancel` to cancel active turn promptly.
  - expose `GET /v1/threads/{threadId}/history` with optional `includeEvents` query.
- Consequences: simple, testable streaming pipeline before provider integration; active-turn state is process-local and will require restart-recovery work in later milestones.
- Alternatives considered: separate stream endpoint per turn, websocket transport for M4.
- Follow-up actions: add restart-safe active-turn recovery and provider-backed execution in M5+.

## ADR-009: M5 ACP Stdio Provider and Permission Bridge

- Status: Accepted
- Date: 2026-02-28
- Context: M5 requires talking to ACP agents over stdio JSON-RPC and forwarding permission requests to HTTP clients with fail-closed semantics.
- Decision:
  - add `internal/agents/acp` provider that launches one external ACP agent process per streamed turn and handles newline-delimited JSON-RPC on stdio.
  - support inbound ACP message classes: response (pending id match), notification (`session/update`), request (`session/request_permission`; unknown methods return JSON-RPC method-not-found).
  - add `POST /v1/permissions/{permissionId}` and SSE event `permission_required`; bridge decisions back to ACP request responses.
  - timeout/disconnect default to `declined` (fail-closed); fake ACP flow converges with `stopReason="cancelled"`.
  - persist turn/event writes with `context.WithoutCancel(r.Context())` so terminal state is still durable after stream disconnect.
- Consequences: permission path is secure by default and testable without real codex dependency; pending permission lifecycle is process-local and late decisions can race with auto-close.
- Alternatives considered: fail-open timeout policy, websocket permission callbacks, delaying persistence until stream close.
- Follow-up actions: expose permission timeout metadata and add explicit permission-resolution terminal events.

## ADR-010: M6 Codex-ACP-Go Runtime Wiring

- Status: Superseded by ADR-014
- Date: 2026-02-28
- Context: M6 needs real codex provider enablement while keeping default tests stable in environments without codex binaries.
- Decision:
  - add runtime flags `--codex-acp-go-bin` and `--codex-acp-go-args` for codex ACP process configuration.
  - `GET /v1/agents` reports codex status as `unconfigured` when binary is absent and `available` when configured.
  - resolve turn provider lazily via a per-turn factory; codex turns create `internal/agents/acp` clients on demand.
  - use persisted `thread.cwd` as ACP process working directory for each turn.
  - keep default automated tests codex-independent; add env-gated optional smoke test (`E2E_CODEX=1` + `CODEX_ACP_GO_BIN`).
- Consequences: production path can run real codex providers without starting background processes at server boot; optional integration remains explicit and non-blocking for CI.
- Alternatives considered: eager provider startup on server boot, replacing existing tests with codex-only integration.
- Follow-up actions: add richer codex health diagnostics and startup validation in M8.

## ADR-011: M7 Context Window Injection and Compact Policy

- Status: Accepted
- Date: 2026-02-28
- Context: turns must preserve continuity across long threads and server restarts without relying on provider in-memory session state.
- Decision:
  - build per-turn injected prompt from `threads.summary` + recent non-internal turns + current input.
  - add runtime controls `context-recent-turns`, `context-max-chars`, and `compact-max-chars`.
  - enforce deterministic trimming order: drop oldest recent turns, then shrink summary, then shrink current input only as last resort.
  - add manual compact endpoint `POST /v1/threads/{threadId}/compact` that runs an internal summarization turn and persists updated `threads.summary`.
  - add `turns.is_internal` to mark compact/system turns and hide them from default history (`includeInternal=true` opt-in).
  - rebuild context solely from durable SQLite data after restart.
- Consequences: predictable context budget behavior and restart-safe continuity with auditable compact turns.
- Alternatives considered: provider-only session memory, in-memory context cache without durable summary, token-based approximate truncation only.
- Follow-up actions: add automatic compact trigger heuristics and token-aware budgeting in M8.

## ADR-012: M8 Reliability Alignment (TTL, Shutdown, Error Codes)

- Status: Accepted
- Date: 2026-02-28
- Context: final milestone requires explicit guarantees for concurrency conflicts, idle resource cleanup, shutdown behavior, and consistent API error semantics.
- Decision:
  - keep one-active-turn-per-thread invariant with `409 CONFLICT` and add additional concurrent multi-thread coverage.
  - add thread-agent cache with idle janitor (`agent-idle-ttl`) and JSON logs for idle reclaim/close actions.
  - add graceful shutdown flow: stop accepting requests, wait active turns, then force-cancel on timeout with structured logs.
  - unify API/SSE error code set to: `INVALID_ARGUMENT`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `TIMEOUT`, `INTERNAL`, `UPSTREAM_UNAVAILABLE`.
  - keep SSE disconnect fail-fast/fail-closed behavior to avoid hanging turns.
  - align acceptance checklist to executable `go test` plus `curl` verification commands.
- Consequences: operational behavior is predictable under contention, disconnects, and process lifecycle transitions.
- Alternatives considered: no idle janitor (manual cleanup only), immediate hard shutdown without grace period, preserving non-unified legacy error codes.
- Follow-up actions: optional enhancements after M8 include WebSocket transport, paginated history, RBAC, and audit expansion.

## ADR-013: Canonical Go Module Path Finalization

- Status: Accepted
- Date: 2026-02-28
- Context: repository ownership and canonical GitHub path are now stable (`github.com/beyond5959/ngent`), while source imports still used a placeholder module path.
- Decision:
  - set `go.mod` module path to `github.com/beyond5959/ngent`.
  - update all in-repo imports from `github.com/example/code-agent-hub-server/...` to canonical module path.
- Consequences: local builds/tests and downstream module consumers resolve a single stable import path; placeholder path drift is removed.
- Alternatives considered: keep placeholder path longer and defer until post-release.
- Follow-up actions: ensure any external examples/scripts use canonical import path only.

## ADR-014: Codex Provider Migration to Embedded Library

- Status: Accepted
- Date: 2026-02-28
- Context: sidecar mode required user-facing binary path configuration (`--codex-acp-go-bin`) and made deployment ergonomics/error modes depend on path wiring.
- Decision:
  - replace codex turn execution from external `codex-acp-go` process spawning to in-process `github.com/beyond5959/acp-adapter/pkg/codexacp` embedded runtime.
  - remove user-facing codex binary path flags; server now links acp-adapter library directly.
  - keep lazy startup and per-thread isolation by creating one embedded runtime per thread provider on first turn.
  - keep existing HTTP/SSE/permission/history contracts unchanged; permission round-trip remains fail-closed.
  - set `/v1/agents` codex status by embedded runtime preflight (`available`/`unavailable`) instead of path-config presence.
- Consequences: simpler operator UX and fewer path misconfiguration failures; server binary is now more tightly coupled to acp-adapter module/runtime behavior.
- Alternatives considered: keep sidecar-only mode; dual mode (embedded + sidecar fallback).
- Follow-up actions: define acp-adapter version pin/upgrade policy and add compatibility smoke checks across codex CLI/app-server versions.

## ADR-015: First-Turn Prompt Passthrough for Embedded Slash Commands

- Status: Accepted
- Date: 2026-02-28
- Context: context-window injection always wrapped prompts with `[Conversation Summary]` / `[Recent Turns]` / `[Current User Input]`, which masked first-turn slash commands (for example `/mcp call`) in embedded acp-adapter flows.
- Decision:
  - keep context wrapper for normal multi-turn continuity.
  - when `summary == ""` and there are no visible recent turns, pass through raw `currentInput` (still bounded by `context-max-chars`) instead of wrapping.
- Consequences:
  - first-turn slash commands remain functional in embedded mode, enabling deterministic permission round-trip validation (`approved` / `declined`).
  - first-turn request text persisted in history no longer includes synthetic wrapper headings.
- Alternatives considered:
  - parse wrapped `[Current User Input]` inside acp-adapter slash-command parser.
  - keep always-wrapped behavior and accept slash-command incompatibility.
- Follow-up actions:
  - evaluate an explicit API-level raw-input toggle if future providers need slash-command compatibility beyond first turn.

## ADR-016: Remove `--allowed-root` Runtime Parameter

- Status: Accepted
- Date: 2026-02-28
- Context: operators requested simpler startup without path allowlist configuration and required that `cwd` can be any user-specified absolute directory.
- Decision:
  - remove CLI flag `--allowed-root`.
  - server startup now configures allowed-roots internally as filesystem root.
  - keep `cwd` validation for absolute path only and retain tenancy/ownership rules.
- Consequences:
  - simpler startup and fewer configuration errors.
  - path-boundary restriction is effectively disabled in default runtime behavior.
- Alternatives considered:
  - keep `--allowed-root` and add a separate opt-out flag.
  - preserve strict allowlist-only behavior.
- Follow-up actions:
  - evaluate policy controls (for example opt-in restrictive mode) if deployments need stronger path boundaries.

## ADR-017: Human-Readable Startup Summary and Request Access Logs

- Status: Accepted
- Date: 2026-02-28
- Context: local operators found single-line JSON startup output hard to scan quickly; runtime troubleshooting also needed stable request completion telemetry.
- Decision:
  - print a concise multi-line startup summary to stderr with a QR code; print the service port and a concrete URL under the QR code.
  - keep structured request completion logs via `slog` for all HTTP traffic.
  - include `req_time`, `method`, `path`, `ip`, `status`, `duration_ms`, and `resp_bytes` in completion logs.
  - standardize log `time` and request `req_time` to UTC `time.DateTime` second precision for easier human scanning and stable parsing.
- Consequences:
  - local startup UX is easier to read without parsing JSON.
  - request observability is consistent across normal JSON responses and long-lived SSE requests.
- Alternatives considered:
  - keep startup as JSON only.
  - add ad-hoc per-endpoint logging instead of one centralized completion logger.
- Follow-up actions:
  - add optional request id correlation in completion logs and outbound SSE error events.

## ADR-019: OpenCode ACP stdio provider

- Status: Accepted
- Date: 2026-03-01
- Context: OpenCode supports ACP and is an actively developed coding agent; adding it as a provider gives users an alternative to the embedded Codex runtime.
- Decision: implement `internal/agents/opencode` as a standalone ACP stdio provider. One `opencode acp --cwd <dir>` process is spawned per turn. The package is self-contained with its own JSON-RPC 2.0 transport layer to avoid coupling with the internal `acp` package.
- Protocol differences from codex ACP that drove a separate implementation:
  - `protocolVersion` field is an integer (`1`) not a string.
  - `session/new` does not accept a client-supplied sessionId; the server assigns one and also returns a model list.
  - `session/prompt` uses a `prompt` array of content items instead of a flat `input` string.
  - `session/update` notifications carry delta text under `update.content.text` for `agent_message_chunk` events, not a flat `delta` field.
  - No `session/request_permission` requests from server to client (OpenCode handles tool permissions internally via MCP).
- Consequences:
  - `opencode` binary must be in PATH for the provider to be available; Preflight() is called at startup.
  - Model selection is optional via `agentOptions.modelId` in thread creation; defaults to OpenCode's configured default.
  - Turn cancel sends `session/cancel` and kills the process within 2s if it doesn't exit cleanly.

## ADR-020: Gemini CLI ACP stdio provider

- Status: Accepted
- Date: 2026-03-01
- Context: Gemini CLI (v0.31+) supports ACP via `--experimental-acp` flag; it uses the `@agentclientprotocol/sdk` npm package which speaks standard newline-delimited JSON-RPC 2.0 over stdio.
- Decision: implement `internal/agents/gemini` as a standalone ACP stdio provider. One `gemini --experimental-acp` process is spawned per turn. Protocol flow: `initialize` → `authenticate` → `session/new` → `session/prompt` with streaming `session/update` notifications.
- Key protocol details:
  - `PROTOCOL_VERSION = 1` (integer).
  - An explicit `authenticate({methodId: "gemini-api-key"})` call is required between `initialize` and `session/new` so Gemini reads `GEMINI_API_KEY` from the environment.
  - `GEMINI_CLI_HOME` is set to a fresh temp directory per turn, containing a minimal `settings.json` that selects API key auth; this prevents Gemini CLI from writing OAuth browser prompts to stdout, which would corrupt the JSON-RPC stream.
  - `session/update` notifications carry delta text under `update.content.text` for `agent_message_chunk` events (same structure as OpenCode).
  - Gemini can send `session/request_permission` requests; the provider bridges these through the hub server's `PermissionHandler` context mechanism. Approved maps to `{outcome: {outcome: "selected", optionId: "allow_once"}}`, declined to `reject_once`, cancelled to `{outcome: {outcome: "cancelled"}}`.
  - Turn cancel sends a `session/cancel` notification (no id, no response expected) and kills the process within 2s.
- Consequences:
  - `gemini` binary must be in PATH and `GEMINI_API_KEY` must be set for the provider to be available.
  - No model selection option at thread creation time; model is controlled by Gemini CLI's own configuration.

## ADR-022: Qwen Code ACP stdio provider integration

- Status: Accepted
- Date: 2026-03-03
- Context:
  - Qwen Code is available locally and supports ACP via `qwen --acp`.
  - hub requirements remain strict: one-active-turn-per-thread, fast cancel, fail-closed permissions, and no regressions for existing providers.
  - protocol inspection shows required ACP fields (`clientCapabilities.fs`, `mcpServers`) and provider-specific response variants.
- Decision:
  - implemented `internal/agents/qwen` as a standalone ACP stdio provider (one process per turn).
  - process command is fixed as `qwen --acp` (no user-supplied binary path in server config).
  - protocol flow is `initialize -> session/new -> session/prompt`, with required params:
    - `initialize.protocolVersion = 1`
    - `initialize.clientCapabilities.fs.readTextFile = false`
    - `initialize.clientCapabilities.fs.writeTextFile = false`
    - `session/new` includes `cwd` and `mcpServers: []`
    - `session/prompt` uses ACP prompt blocks (`[{type:"text", text:...}]`)
  - stream output is parsed from `session/update` when `update.sessionUpdate == "agent_message_chunk"` and delta comes from `update.content.text`.
  - handle `session/request_permission` by mapping hub decisions into ACP outcome format:
    - approve/decline: `outcome=selected` with matching `optionId`
    - cancel: `outcome=cancelled`
    - default deny on timeout/errors/no handler (fail-closed)
  - cancellation path sends `session/cancel` with `sessionId` on context cancellation and converges to `stopReason=cancelled`.
  - stderr is drained/discarded to avoid protocol stream corruption; existing providers (`codex`, `opencode`, `gemini`) behavior remains unchanged.
- Consequences:
  - qwen availability is startup-preflight dependent (`qwen` in PATH).
  - real qwen turns depend on local runtime prerequisites (writable qwen home/config + auth/network readiness), so environment misconfiguration can fail before ACP turn execution.
  - provider must tolerate schema drift across qwen versions (new optional fields in `session/new` response).
  - test surface expanded:
    - fake-process ACP tests for initialize/session/new/session/prompt/session/update
    - permission mapping tests (`approved`, `declined`, `cancelled`)
    - optional real smoke test (`E2E_QWEN=1`)
- Alternatives considered:
  - reuse a single generic ACP provider configured by command/args at runtime.
  - force qwen through existing `opencode`/`gemini` adapters.
  - postpone qwen until ACP schema is frozen upstream.
- Follow-up actions:
  - improve qwen preflight diagnostics for filesystem/auth prerequisites (beyond PATH existence).
  - keep validating against newer qwen releases for ACP schema compatibility.

## ADR-023: Shared ACP stdio transport for OpenCode and Qwen providers

- Status: Accepted
- Date: 2026-03-03
- Context:
  - `internal/agents/opencode` and `internal/agents/qwen` had duplicated JSON-RPC stdio transport code (request id/pending map, read loop, inbound request handling, notify/call framing, process termination helpers).
  - duplicated protocol plumbing increased maintenance risk and made bug fixes easy to diverge across providers.
- Decision:
  - extracted shared package `internal/agents/acpstdio` with:
    - newline-delimited JSON-RPC connection (`Conn`) supporting `Call`, `Notify`, notifications, and inbound request handling.
    - shared JSON-RPC message/error types.
    - shared helpers: `ParseSessionID`, `ParseStopReason`, `TerminateProcess`.
  - refactored both providers to use the shared transport while keeping provider-specific ACP behavior unchanged:
    - OpenCode flow and modelId handling unchanged.
    - Qwen permission mapping and fail-closed behavior unchanged.
- Consequences:
  - transport-layer fixes are now centralized and consistent across providers.
  - provider files are shorter and focused on protocol semantics instead of wire plumbing.
  - regression risk from this refactor is controlled by fake-process tests + full `go test ./...` + real E2E smoke tests for both providers.
- Alternatives considered:
  - keep duplicated transport code and only copy fixes manually.
  - extract only tiny helper funcs without shared connection type.
- Follow-up actions:
  - if Gemini migration value is clear, evaluate moving Gemini transport to `acpstdio` in a separate change (to keep current refactor blast radius limited).

## ADR-024: Claude Code embedded provider via claudeacp runtime

- Status: Accepted
- Date: 2026-03-03
- Context:
  - Claude Code is the primary Anthropic coding agent; it was listed as a planned provider (`🔜`) since project inception.
  - `github.com/beyond5959/acp-adapter` already contained a complete parallel `pkg/claudeacp` package with identical API surface to `pkg/codexacp`; no new library dependency was needed.
  - Preflight for Claude does not require a binary path check — availability is determined entirely by the presence of `ANTHROPIC_AUTH_TOKEN` in the environment.
- Decision:
  - implement `internal/agents/claude` as an embedded provider package mirroring `internal/agents/codex`.
  - replace `codexacp` references with `claudeacp`; `Preflight()` checks `ANTHROPIC_AUTH_TOKEN != ""` (no binary lookup).
  - `DefaultRuntimeConfig()` delegates to `claudeacp.DefaultRuntimeConfig()`, which reads `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_BASE_URL` from environment.
  - wire into server startup: preflight, `/v1/agents` status, `AllowedAgentIDs`, and `TurnAgentFactory`.
  - for local development, add `replace github.com/beyond5959/acp-adapter => /path/to/local/acp-adapter` in `go.mod`; remove or publish before production release.
- Consequences:
  - claude availability is purely environment-variable dependent; no binary installation required beyond valid API credentials.
  - `ANTHROPIC_BASE_URL` allows pointing at a compatible proxy or local endpoint (e.g., for testing or corporate gateways).
  - local `go.mod` replace directive must be removed or updated to a published version before CI/release builds.
- Alternatives considered:
  - implement as an ACP stdio provider wrapping the `claude` CLI binary (rejected: CLI spawns its own runtime per invocation with higher latency and no direct permission bridge).
  - share implementation with codex via generics/interface (rejected: would couple two independently-versioned runtimes).
- Follow-up actions:
  - publish `acp-adapter` with `pkg/claudeacp` to a versioned tag and remove the `replace` directive from `go.mod`.
  - add permission round-trip E2E test for Claude (approved/declined/cancelled paths).

## ADR-025: Thread-level model switching via ACP session config options

- Status: Accepted
- Date: 2026-03-05
- Context:
  - model switching previously used thread metadata patch (`PATCH /v1/threads/{threadId}` with `agentOptions.modelId`) and recreated provider state, while ACP now standardizes runtime config through `session/new.configOptions` + `session/set_config_option`.
  - Web UI requirement is immediate switch on model select (no extra apply action), and model list/selected value must come from ACP session config data (including per-model descriptions).
- Decision:
  - add thread-scoped config option endpoints:
    - `GET /v1/threads/{threadId}/config-options`
    - `POST /v1/threads/{threadId}/config-options` (`configId`, `value`)
  - `POST` applies changes through provider `SetConfigOption`, backed by ACP `session/set_config_option`.
  - support `ConfigOptionManager` on all built-in providers:
    - embedded (`codex`, `claude`): mutate cached session in-place.
    - stdio (`opencode`, `qwen`, `gemini`, `kimi`): perform ACP handshake/apply flow and persist resulting model id for subsequent turns.
  - keep `agentOptions.modelId` as durable thread metadata mirror when `configId=model` succeeds.
  - Web UI model selector is bound to thread-level `configOptions` model option and applies immediately on `change`.
- Consequences:
  - model UX is consistent with ACP protocol semantics and no longer depends on separate apply state.
  - per-option descriptions are available to UI and rendered beneath selector.
  - active-turn safety remains strict (`409` on config mutation while turn is active).
- Alternatives considered:
  - continue using thread patch only and skip ACP `session/set_config_option`.
  - expose only agent-level model catalog and infer current model from local metadata.
- Follow-up actions:
  - optionally add richer Web UI rendering for non-model config categories (e.g. reasoning level) using the same API.

## ADR-026: Persist agent config catalogs in SQLite and refresh them asynchronously on startup

- Status: Accepted
- Date: 2026-03-06
- Context:
  - thread config UX now depends on ACP `configOptions`, especially model-specific reasoning choices.
  - querying live providers on every thread switch is unnecessary and becomes user-visible after server restart because model/reasoning catalogs are metadata, not per-turn output.
  - different threads on the same agent can keep different selected model/reasoning values, while still reusing the same underlying catalog data.
- Decision:
  - add sqlite table `agent_config_catalogs(agent_id, model_id, config_options_json, updated_at)` and persist normalized ACP config-options snapshots there.
  - keep per-thread selected values in `threads.agent_options_json`:
    - `modelId`
    - `configOverrides`
  - read path:
    - `GET /v1/threads/{threadId}/config-options` first loads the stored catalog row for the thread's selected model (or a reserved default snapshot when no model is selected yet), then overlays the thread's own selected current values.
    - `GET /v1/agents/{agentId}/models` derives model list from stored catalogs before falling back to live discovery.
  - write path:
    - `POST /v1/threads/{threadId}/config-options` still mutates the live provider/session, then persists both thread selection state and the current model's returned config-options snapshot.
  - startup behavior:
    - launch a background refresher goroutine after server initialization.
    - refresher queries default + per-model config catalogs for built-in agents and updates sqlite without delaying HTTP startup or blocking frontend requests.
    - on partial refresh failure, keep previously stored rows for models that could not be refreshed instead of deleting them.
- Consequences:
  - restart no longer forces frontend-visible catalog discovery in the common case; stored model/reasoning metadata remains immediately available.
  - reasoning lists remain model-specific while thread-selected current values stay isolated per thread.
  - partial refresh strategy trades perfect freshness for availability and catalog continuity.
- Alternatives considered:
  - keep catalogs only in frontend memory and re-query on restart.
  - store only an agent-level flat reasoning list (rejected because reasoning is model-dependent).
  - block startup until all agents refresh live catalogs (rejected because it would directly impact frontend responsiveness).

## ADR-029: Consolidate sidebar thread actions into a drawer and reuse thread patch for rename

- Status: Accepted
- Date: 2026-03-06
- Context:
  - sidebar thread rows already show dense metadata and activity state; a direct delete affordance makes the list visually noisy and increases the chance of accidental destructive clicks.
  - thread rename is metadata-only and should share the existing thread ownership/conflict semantics instead of adding a dedicated endpoint.
- Decision:
  - replace the direct sidebar delete button with a per-thread drawer trigger.
  - render drawer actions as text-only controls in this order:
    - `Rename`
    - `Delete` (danger styling)
  - extend `PATCH /v1/threads/{threadId}` to accept optional `title` in addition to `agentOptions`.
  - keep rename under the same active-turn conflict guard as other thread mutations.
- Consequences:
  - sidebar actions are less visually noisy and destructive actions are one click deeper.
  - rename and thread metadata/config mutations now share one API contract.
  - users must wait for a running turn to finish, or cancel it, before renaming the thread.

## ADR-030: Handle codex `item/tool/requestUserInput` and `item/tool/call` without hard RPC failure

- Status: Accepted
- Date: 2026-03-06
- Context:
  - codex app-server may emit server requests `item/tool/requestUserInput` and `item/tool/call` during MCP-related operations.
  - previous bridge behavior returned `-32000 ... is not supported`, which caused app-server-side hard errors and interrupted command flow.
- Decision:
  - implement a compatibility fallback in embedded adapter path:
    - for `item/tool/requestUserInput`: return schema-compatible `answers` payload by auto-selecting the first option label for each question.
    - for `item/tool/call`: return schema-compatible `DynamicToolCallResponse` with `success=false` and one text content item, instead of throwing RPC method error.
- Consequences:
  - removes immediate `-32000` hard-fail class for these methods and allows app-server to continue handling tool flow.
  - behavior is still a fallback (not full interactive user-input / dynamic-tool execution support).
- Alternatives considered:
  - keep fail-closed `-32000` hard error (rejected: user-facing breakage for MCP flows).
  - fully implement interactive user-input/dynamic tool execution end-to-end in hub UI + protocol bridge in one step (rejected for hotfix scope).

## ADR-031: Kimi CLI ACP stdio provider with dual startup syntax fallback

- Status: Accepted
- Date: 2026-03-09
- Context:
  - Kimi CLI now exposes ACP mode in upstream docs, but current official pages show both `kimi acp` and `kimi --acp` as startup forms.
  - hub requirements remain unchanged: one active turn per thread, fast cancel, fail-closed permissions, and no user-supplied binary-path flags.
- Decision:
  - implement `internal/agents/kimi` as a standalone ACP stdio provider (one process per turn).
  - startup tries `kimi acp` first, then retries with `kimi --acp` if the first form fails before ACP initialize completes.
  - keep protocol flow aligned with existing stdio providers:
    - `initialize -> session/new -> session/prompt`
    - stream deltas from `session/update` / `agent_message_chunk`
    - handle `session/request_permission` with fail-closed mapping
    - send `session/cancel` on context cancellation
  - wire `kimi` into startup preflight, `/v1/agents`, thread allowlist, turn factory, model discovery, and startup catalog refresh.
- Consequences:
  - Kimi becomes a built-in provider without adding new runtime flags or config surface.
  - startup is resilient to the current upstream command-form drift across Kimi CLI docs/releases.
  - real Kimi turns still depend on local Kimi authentication and network readiness.
- Alternatives considered:
  - hardcode only `kimi acp` (rejected: conflicts with current upstream `--acp` docs).
  - hardcode only `kimi --acp` (rejected: conflicts with IDE integration docs and user expectation).
  - add a user-facing command override flag (rejected: unnecessary config surface and contrary to current built-in provider policy).

## ADR-032: Shared common agent config/state helper without protocol unification

- Status: Accepted
- Date: 2026-03-09
- Context:
  - built-in agents duplicated the same common fields and methods repeatedly:
    - `Dir`
    - `ModelID`
    - `ConfigOverrides`
    - model/config override normalization, cloning, and state updates after `SetConfigOption`
  - the duplicated code existed across both stdio providers (`gemini`, `opencode`, `qwen`, `kimi`) and embedded providers (`codex`, `claude`).
  - previous experience in this repo shows that protocol flows diverge materially between providers, so a single generic provider abstraction would be riskier than the duplication it removes.
- Decision:
  - extract shared common config/state into `internal/agents/agentutil`:
    - `agentutil.Config`
    - `agentutil.State`
  - move only common constructor validation and mutable model/config-override state management into the shared helper.
  - keep each provider's transport/runtime/session logic independent.
- Consequences:
  - repeated per-provider bookkeeping is reduced without coupling unrelated ACP/runtime flows.
  - future providers can reuse the same common state helper while still implementing provider-specific protocol behavior.
  - embedded and stdio providers remain free to evolve independently where their protocol/runtime requirements differ.
- Alternatives considered:
  - create one generic ACP provider with pluggable commands and hooks (rejected: protocol differences are too large and already visible across current providers).
  - leave duplicated `Config`/`Client` state in place (rejected: ongoing maintenance cost and drift risk).

## ADR-033: Surface ACP plan updates as first-class SSE and Web UI state

- Status: Accepted
- Date: 2026-03-09
- Context:
  - ACP agents can emit `session/update` notifications with `sessionUpdate == "plan"` and a full `entries[]` list describing the current execution plan.
  - the hub previously only mapped `agent_message_chunk` into `message_delta`, so users could not see plan progress in the Web UI and history reloads lost that context entirely.
- Decision:
  - normalize ACP `session/update` payloads in shared agent code, recognizing both `agent_message_chunk` and `plan`.
  - route plan replacements through a new per-turn `PlanHandler` context callback, parallel to the existing permission callback pattern.
  - emit and persist a dedicated SSE/history event `plan_update` with payload `{"turnId","entries":[]}`.
  - render `plan_update` in the Web UI as a live plan card above the streaming agent bubble, and rebuild the final plan state from persisted turn events when loading history.
- Consequences:
  - ACP plan state is now visible during live execution without overloading `message_delta`.
  - history replay preserves the last known plan instead of dropping it on refresh.
  - empty `entries[]` remains meaningful as "clear the current plan", so the hub must preserve replacement semantics instead of merging incrementally.
- Alternatives considered:
  - fold plan text into `message_delta` (rejected: mixes distinct ACP concepts and loses replacement semantics).
  - keep plan rendering purely transient in the browser (rejected: history reload would still discard plan state).

## ADR-034: Source Kimi config catalogs from local config to avoid empty sessions

- Status: Accepted
- Date: 2026-03-09
- Context:
  - Kimi CLI persists ACP `session/new` handshakes as local session history, even when the hub only wants model/config metadata and never sends a prompt.
  - the previous Kimi provider queried models and thread config options through `session/new`, which polluted `~/.kimi/sessions` with empty sessions during startup catalog refresh, thread config loads, and model changes.
  - local Kimi config already exposes the needed metadata: `default_model`, `default_thinking`, and model capabilities.
- Decision:
  - read Kimi model catalog and default thinking state from local `config.toml` when available.
  - synthesize thread `ConfigOptions` and `DiscoverModels()` results from that local config instead of ACP `session/new`.
  - apply reasoning overrides for real prompt turns through Kimi startup flags (`--thinking` / `--no-thinking`) and keep model selection on `--model`.
  - retain ACP fallback only when local config cannot be read or parsed.
- Consequences:
  - Kimi thread config queries and startup catalog refresh no longer create empty session history entries in normal local setups.
  - Kimi keeps real ACP turns for actual prompts, permissions, streaming, and cancellation behavior.
  - local config structure becomes part of the provider compatibility surface, so future Kimi config schema drift must be monitored.
- Alternatives considered:
  - keep ACP-only config discovery (rejected: side effect creates noisy empty sessions).
  - disable Kimi config/model catalog refresh entirely (rejected: would regress model picker accuracy).

## ADR-035: Add opt-in ACP debug tracing behind `--debug`

- Status: Accepted
- Date: 2026-03-11
- Context:
  - operators need a direct way to inspect the exact ACP methods and payloads exchanged with built-in agents when debugging protocol/runtime issues.
  - normal production logs must remain concise, protocol-only, and continue redacting sensitive information.
  - ACP traffic currently flows through three different boundaries:
    - stdio JSON-RPC transport (`acpstdio`)
    - legacy stdio ACP client (`internal/agents/acp`)
    - embedded runtimes (`codex`, `claude`)
- Decision:
  - add a startup flag `--debug` that raises the shared `slog` logger to debug level.
  - when enabled, emit structured stderr log entries `acp.message` for inbound/outbound ACP JSON-RPC messages across all supported transport paths.
  - include `component`, `direction`, `rpcType`, `method`, `id`, and the sanitized `rpc` payload in each trace log.
  - keep a redaction pass in front of debug logging so sensitive keys and common token formats are masked before serialization.
- Consequences:
  - ACP handshakes, prompts, updates, permission requests, and permission responses are inspectable without changing HTTP/stdout protocol behavior.
  - debug mode can produce high log volume and should be used only when investigating runtime issues.
  - the tracing mechanism remains transport-local and does not require changing provider/public API contracts.
- Alternatives considered:
  - always log ACP payloads at info level (rejected: too noisy and unsafe for normal operation).
  - add per-provider bespoke debug flags (rejected: fragmented UX and duplicated plumbing).
  - expose raw unredacted ACP dumps (rejected: conflicts with repository logging/redaction requirements).

## ADR-036: Persist thread-level ACP session selection and resume through provider sessions

- Status: Accepted
- Date: 2026-03-11
- Context:
  - users need to browse an agent's historical ACP sessions and continue a conversation from an existing provider-owned session.
  - hub threads already persist local turn history, but blindly injecting that history into prompts duplicates context once ACP `session/load` has restored the provider's own transcript.
  - the frontend needs paginated session discovery and a lightweight way to switch between "new session" and "existing session" without changing the SQLite schema.
- Decision:
  - persist the selected ACP session id in `threads.agent_options_json` as `sessionId`.
  - expose `GET /v1/threads/{threadId}/sessions` backed by ACP `session/list`, using a fresh provider instance so sidebar discovery does not disturb cached turn runtimes.
  - extend built-in providers to:
    - load `sessionId` through ACP `session/load` when present.
    - create a fresh session through `session/new` otherwise.
    - report the effective session id back during turn setup so the server can persist it and emit SSE `session_bound`.
  - once `sessionId` is present on a thread, skip local recent-turn prompt injection and rely on ACP session state for continuation.
  - keep Web UI session selection as a thread metadata mutation (`PATCH /v1/threads/{threadId}`) and model the "New session" action as clearing `sessionId`.
- Consequences:
  - session continuation survives provider restart/server restart when the agent supports ACP `session/load`.
  - right-sidebar session browsing stays paginated (`nextCursor`/`Show more`) and does not require schema changes.
  - local SQLite history remains a hub-local view and is no longer the source of truth for resumed ACP context on bound threads.
  - historical ACP transcript import is deferred and tracked separately as a known limitation.
- Alternatives considered:
  - import the full ACP transcript from `session/load` into SQLite immediately (rejected: larger behavioral change, requires reliable transcript reconstruction).
  - keep relying on hub prompt injection even after binding to an ACP session (rejected: duplicates already-restored conversation context).
  - add a dedicated sessions table instead of reusing `agentOptions` JSON (rejected: unnecessary schema churn for a single thread-scoped selection value).

## ADR-037: Scope Web UI chat playback to the selected ACP session

- Status: Accepted
- Date: 2026-03-11
- Context:
  - the Web UI session sidebar switches `agentOptions.sessionId` on the active thread without changing `threadId`.
  - local history remains stored per thread, and each turn's effective ACP session is persisted through the `session_bound` event stream.
  - refreshing the chat area only on thread changes leaves stale messages visible after choosing a different session from the sidebar.
- Decision:
  - treat `(threadId, sessionId)` as the client-side chat render scope.
  - when the active thread's selected session changes outside an active turn, rebuild the chat area and reload history for that scope.
  - when the selected scope changes to one that is still streaming in the background, rebuild the chat area unless that same scope's streaming bubble is already mounted in the current DOM.
  - filter locally persisted turns by their `session_bound` event so the center chat panel renders only turns recorded for the selected session; an empty `sessionId` shows only unbound turns.
- Consequences:
  - clicking a session in the sidebar replays that session's ngent-recorded turns instead of keeping the previously rendered session on screen.
  - revisiting a background-streaming session restores its live typing/loading state from scope-local client buffers instead of dropping back to the persisted history snapshot.
  - session changes reported during a live turn do not wipe the streaming bubble; the full refresh is deferred until the turn finishes.
  - transcript content that predates ngent participation is still not imported from the provider and remains covered by KI-021.
- Alternatives considered:
  - add a session-scoped history endpoint immediately (rejected: larger server contract change while turn events already contain the session discriminator).
  - keep all thread turns visible regardless of selected session (rejected: does not meet the expected session playback behavior).

## ADR-038: Allow concurrent turns across different sessions on the same thread

- Status: Accepted
- Date: 2026-03-13
- Context:
  - once ACP session switching shipped, users needed to leave one session streaming while switching the same thread to another session and continuing work there.
  - the existing runtime/controller and provider cache were keyed only by `threadId`, so a running turn blocked `PATCH /v1/threads/{threadId}` session changes and forced all sessions on that thread into a single execution lane.
  - provider instances keep mutable session/model/config state, so simply removing the conflict check would have let different sessions reuse the wrong cached provider.
- Decision:
  - change turn concurrency from thread-wide to `(threadId, sessionId)` scope, with empty `sessionId` representing the provisional "new session" scope until `session_bound` arrives.
  - keep delete/compact and shared thread-state mutations guarded at whole-thread scope.
  - rebind active turn scope when `session_bound` reports the effective session id so same-session re-entry remains blocked after a new session is created.
  - key cached providers by thread/session/shared-option scope instead of raw thread id, and evict all cached providers for a thread after shared config changes.
  - update the Web UI to store messages and live stream state per chat scope `(threadId, sessionId)` so background turns in one session do not overwrite another session's visible transcript.
- Consequences:
  - users can switch the right-side session picker and start work in session B while session A is still streaming on the same thread.
  - same-session re-entry protection, cancellation, and permission handling remain unchanged.
  - thread-wide config changes remain serialized and are documented as KI-021.
- Alternatives considered:
  - keep thread-wide turn serialization and force users to create separate threads per ACP session (rejected: poor UX and redundant thread duplication).
  - remove the conflict check without changing provider cache scope (rejected: would mix session-bound provider state and route turns to the wrong ACP session).

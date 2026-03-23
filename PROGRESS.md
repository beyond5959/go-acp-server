# Progress

## Project Overview

Ngent Server is a Go service that exposes HTTP/JSON APIs and SSE streaming for multi-client, multi-thread agent turns.
The system targets ACP-compatible agent providers, lazily starts per-thread agents, persists interaction history in SQLite, and bridges runtime permission requests back to clients.
Current built-in providers are `codex`, `claude`, `cursor`, `opencode`, `gemini`, `kimi`, `qwen`, and `blackbox`.
This file is the source of milestone progress, validation commands, and next actions.

## Current Milestone

- `Post-M8` ACP multi-agent readiness and maintenance.

## Latest Update (2026-03-23)

- `Post-M8` human-readable stderr logger rollout completed:
  - replaced the previous `slog` JSON logger with a repo-local leveled logger in `internal/observability`, keeping the existing `Debug/Info/Warn/Error` call shape while switching output to readable text lines on `stderr`.
  - changed HTTP request completion logs to a compact nginx-style access-log format such as `INFO: 2026-03-23 15:30:45 127.0.0.1 - "GET /v1/threads HTTP/1.1" 200 OK 12.4ms`.
  - kept ACP `--debug` tracing and secret redaction intact; debug traces now render as readable text with sanitized embedded JSON payloads instead of JSON log envelopes.
  - enabled ANSI color for level/status segments only when `stderr` is a TTY, so redirected output remains plain text.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-M8` Cursor CLI ACP integration completed:
  - added `internal/agents/cursor` on top of the shared `acpcli` driver, with startup fallback across `agent acp` and `cursor-agent acp`.
  - wired `cursor` into startup preflight, `/v1/agents`, thread allowlist, turn factory, and agent-model discovery flow.
  - verified against the local installed Cursor CLI and the official ACP docs that Cursor requires an ACP `authenticate` call with `methodId="cursor_login"` after `initialize`; without that step, real `session/new` does not respond.
  - confirmed by local probing that Cursor model selection is exposed through ACP `configOptions` and must be applied via `session/set_config_option("model", ...)`; `session/new.model` and `session/new.modelId` are ignored by the real CLI.
  - normalized generic ACP permission-option matching so providers advertising hyphenated kinds such as `allow-once` / `reject-once` still flow through ngent's fail-closed default decision path.
  - added fake-process coverage for:
    - authenticated ACP stream startup.
    - model selection through `session/set_config_option`.
    - model discovery after ACP authentication.
    - session transcript replay after ACP authentication.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./internal/agents/cursor ./internal/agents/acpcli ./cmd/ngent`
    - pass: `go test ./...`

## Previous Update (2026-03-22)

- `Post-M8` ACP assistant image/resource content rendering completed:
  - extended shared ACP `agent_message_chunk` parsing so non-text assistant payloads are preserved as structured content blocks instead of being dropped when they are not `{type:"text",text:...}` deltas.
  - added a first-class assistant message-content callback in the agent layer and bridged those blocks through HTTP/SSE/history as `message_content` events alongside the existing `message_delta` text stream.
  - updated the Web UI assistant segment timeline to render `message_content` blocks during streaming and after reload/history reconstruction, with dedicated cards for image content and embedded resource content plus JSON fallback for unknown block shapes.
  - kept `responseText` as the visible-text aggregate only, so existing API consumers remain compatible while structured content survives in turn events.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-M8` BLACKBOX Web UI tool-call rendering normalization completed:
  - dropped standalone whitespace-only assistant chunks when they arrive as isolated separators around tool calls, preventing BLACKBOX turns from rendering large blank answer blocks or effectively empty assistant messages.
  - widened Web UI tool-call payload handling so `content` / `locations` no longer require array-only shapes; single-object payloads are normalized and still rendered.
  - improved tool-call titles for generic BLACKBOX payloads such as search calls titled only `.` by falling back to kind/path-derived labels.
  - validation:
    - pending: `cd internal/webui/web && npm run build`
    - pending: `go test ./...`

- `Post-M8` BLACKBOX AI ACP integration completed:
  - added `internal/agents/blackbox` on top of the shared `acpcli` driver, using `blackbox --experimental-acp` with stdout-noise tolerance because the CLI can emit non-JSON process/telemetry lines before or between ACP frames.
  - wired `blackbox` into startup preflight, `/v1/agents`, thread allowlist, turn factory, and agent-model discovery flow.
  - confirmed against local `blackbox 1.2.47` ACP probing that:
    - `initialize` and `session/new` succeed with the standard ACP handshake.
    - current initialize capabilities advertise `loadSession=false`.
    - real `session/load` returns `-32601 method not found`, so existing-session replay and session sidebar resume remain unavailable for now.
    - ACP startup itself does not require explicitly passing `BLACKBOX_API_KEY` as long as the local BLACKBOX CLI already has a usable auth method configured; actual prompt execution still depends on valid upstream auth/network state.
  - added fake-process coverage for:
    - streaming with noisy stdout.
    - model forwarding through startup args plus ACP `session/new` / `session/prompt`.
    - fail-closed structured permission bridging.
    - unsupported `session/list` behavior when `loadSession=false`.
  - validation:
    - pending: `cd internal/webui/web && npm run build`
    - pending: `go test ./...`

- `Post-M8` Web UI agent-list overflow trigger hover-only reveal completed:
  - changed the agent-list three-dot trigger to stay hidden by default and reveal only on row hover, keyboard focus, or while its menu is open.
  - increased the trigger size slightly while keeping its hover surface compact, so the control is easier to hit once revealed without dominating the action column.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./...`

## Previous Update (2026-03-19)

- `Post-M8` Web UI left-rail layout alignment completed:
  - kept the agent/thread rail permanently expanded after follow-up product review; only the session panel now supports collapse/expand.
  - removed the agent-list search box and its keyboard shortcut/filtering path; the left rail is now a straight thread list with actions only.
  - moved `New agent` below the agent list and updated the expanded session header to show the active agent name, project path, and a full-width `New session` entry above the session list.
  - removed the redundant uppercase `SESSIONS` section label above the session list after the new header/new-session block, keeping the panel header visually quieter.
  - restored the left-top brand label from `Agents` to the product name `Ngent`.
  - hid the session panel entirely until an agent/thread is selected, so first load no longer reserves a blank middle column.
  - fixed the CSS visibility rule so the hidden session panel truly leaves the flex layout instead of still occupying width via `.session-sidebar { display:flex }`.
  - flattened the expanded session panel surface so its header and list share the same background plane instead of reading as two differently colored sections.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

## Earlier Update (2026-03-16)

- `Post-M8` ACP tool-call streaming completed:
  - extended shared ACP `session/update` parsing to preserve structured `tool_call` and `tool_call_update` payloads, including `toolCallId`, status/title/kind, content blocks, locations, and raw input/output payloads.
  - added first-class turn callbacks plus HTTP/SSE/history persistence for those tool-call events instead of dropping them at the provider boundary.
  - updated the Web UI stream state and history reconstruction to merge tool-call events by `toolCallId` and render live/persisted tool-call cards alongside plan/reasoning/message output.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-M8` Web UI fresh-session reset fix completed:
  - explicit `New session` now allocates a client-side fresh-session scope even when the active thread already has no persisted `sessionId`, so repeated `New session` clicks no longer reuse the same anonymous chat buffer.
  - empty-session history replay now drops cancelled turns that never emitted `session_bound` and never produced visible response text, preventing stale cancelled placeholders from reappearing after reload.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-M8` deferred thread config apply completed:
  - changed `POST /v1/threads/{threadId}/config-options` to validate against available config options and persist thread `agentOptions.modelId` / `agentOptions.configOverrides` without mutating the live provider.
  - narrowed cached provider scope from full `agentOptions` to thread + session/fresh-session identity, so picker edits no longer evict the current session provider by themselves.
  - added turn-start config sync: right before streaming a new turn, ngent compares persisted thread selections against the cached provider's current model/reasoning state and only then applies changed options.
  - updated acceptance/spec/ADR docs to describe the new "persist now, apply on next turn" behavior.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

## Previous Update (2026-03-14)

- `Post-M8` Web UI thinking tense alignment completed:
  - kept the live reasoning toggle label as `Thinking` while deltas are still streaming.
  - switched finalized reasoning labels to `Thought` so completed content reads in the past tense.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-M8` Web UI thinking markdown rendering completed:
  - switched finalized `Thinking` content from escaped plain text to the same sanitized markdown renderer used by finalized assistant replies.
  - kept streaming reasoning as plain text so partial markdown does not reflow while deltas are still arriving.
  - extended markdown typography/code styles to apply inside expanded `Thinking` content as well as normal assistant message bubbles.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-M8` Web UI thinking fold UX completed:
  - changed the `Thinking` section to a native collapsible panel in the Web UI.
  - keep in-flight reasoning panels expanded while `reasoning_delta` is still streaming.
  - once the turn settles into persisted history, render the same `Thinking` content collapsed by default so final replies stay compact.
  - preserve manual expand/collapse state for finalized messages across in-page list re-renders.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-M8` Web UI thinking/reasoning visibility completed:
  - added a per-turn reasoning callback bridge in `internal/agents` so ACP `thought_message_chunk` / `agent_thought_chunk` updates no longer stop at provider parsing.
  - persisted hidden reasoning as first-class `reasoning_delta` turn events in the HTTP/SSE layer, alongside existing `message_delta` and `plan_update` events.
  - updated the Web UI stream state and history reconstruction so agent messages render a visible `Thinking` section during streaming and after reload/history fetch.
  - kept assistant answer text as `responseText`; reasoning remains event-backed instead of being merged into the visible answer body.
  - added regression coverage for:
    - ACP notification routing of thought chunks into reasoning callbacks.
    - SSE/history persistence of `reasoning_delta` events.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

## Previous Update (2026-03-13)

- `Post-M8` shared ACP CLI provider driver completed:
  - extracted `internal/agents/acpcli` as the shared ACP CLI lifecycle driver for `qwen`, `opencode`, `gemini`, and `kimi`, covering shared `initialize/session/new/session/load/session/list/session/prompt/session/set_config_option` flow plus model discovery and transcript replay.
  - migrated the four ACP CLI providers to provider-hook configuration instead of maintaining separate copies of the same stdio/session orchestration logic.
  - extended `internal/agents/acpstdio` with opt-in stdout-noise tolerance so Gemini can reuse the same transport instead of a provider-local JSON-RPC implementation.
  - preserved provider-specific behavior behind hooks:
    - `gemini` keeps temporary `GEMINI_CLI_HOME` bootstrapping and stdout noise filtering.
    - `qwen` / `kimi` keep selectable permission-option mapping and fail-closed timeout handling.
    - `opencode` keeps synchronous `session/cancel` behavior.
  - validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`
    - pass: real host smoke `E2E_QWEN=1 go test ./internal/agents/qwen -run TestQwenE2ESmoke -count=1 -v -timeout 180s`
    - pass: real host replay `E2E_QWEN=1 go test ./internal/agents/qwen -run TestQwenE2ESessionTranscriptReplay -count=1 -v -timeout 240s`
    - pass: real host config probe `E2E_KIMI=1 go test ./internal/agents/kimi -run TestKimiConfigOptionsE2EDoesNotCreateSession -count=1 -v -timeout 240s`
    - pass: real host smoke `E2E_KIMI=1 go test ./internal/agents/kimi -run TestKimiE2ESmoke -count=1 -v -timeout 180s`
    - observed failure: real host smoke `E2E_OPENCODE=1 go test ./internal/agents/opencode -run TestOpenCodeE2ESmoke -count=1 -v -timeout 180s` returned `opencode: session/new: context deadline exceeded`; tracked in `docs/KNOWN_ISSUES.md`.

- `Post-M8` Web UI/session reset regression fixed:
  - completed the `New session` fix after switching to a historical ACP session: clearing `thread.agentOptions.sessionId` still evicts stale empty-scope providers, and now also marks the next turn as an explicit fresh-session request so prompt building skips `[Conversation Summary]` / `[Recent Turns]` injection for that first turn.
  - kept the fresh-session marker server-internal: it is persisted in `thread.agent_options_json` only until the next `session_bound`, but stripped from public thread responses so the API contract stays stable.
  - added a backend regression test that reproduces the real `A -> historical B -> New session -> send` path and verifies the new session transcript contains only the new user/assistant exchange instead of wrapped prior thread history.
  - disabled composer send/input while the Web UI is switching thread sessions, preventing users from submitting a turn against a session selection that is still in flight.
  - validation:
    - pass: `go test ./internal/httpapi -run 'Test(NewSessionResetSkipsContextInjection|TurnSessionBoundPersistsSessionIDAndSkipsContextInjection|UpdateThreadClearingSessionDropsStaleUnboundProvider)' -count=1`
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-M8` session-history sqlite cache completed:
  - added SQLite table `session_transcript_cache` keyed by `(agent_id, cwd, session_id)` to persist provider-owned session replay snapshots separately from hub `turns/events`.
  - `GET /v1/threads/{threadId}/session-history` now reads sqlite first; on cache miss it still calls provider `LoadSessionTranscript`, then persists the normalized replay snapshot for later reuse.
  - cached session-history snapshots survive server restart, so revisiting the same historical session no longer requires a fresh provider `session/load` every time.
  - known tradeoff: cache freshness is not yet invalidated by provider `updatedAt` metadata; stale replay risk is tracked in `docs/KNOWN_ISSUES.md`.
  - validation:
    - pass: `go test ./internal/storage ./internal/httpapi -run 'Test(SessionTranscriptCacheCRUD|ThreadSessionHistoryEndpoint|ThreadSessionHistoryEndpointUsesSQLiteCacheAcrossRestart)$' -count=1`
    - pass: `go test ./...`

- `Post-M8` ACP `session/load` transcript replay standardization completed:
  - removed provider-specific transcript reconstruction for `kimi`, `opencode`, `codex`, and `qwen`; `GET /v1/threads/{threadId}/session-history` now uses the same ACP path for all four providers:
    - resolve the selected session from ACP `session/list`
    - call ACP `session/load`
    - collect replayed `session/update` notifications into `user` / `assistant` messages
  - extended shared ACP update parsing to support standard `user_message_chunk` and `agent_message_chunk` replay events, and added a shared transcript collector for `session/load` history replay.
  - fixed Codex session replay to list and load within the same embedded runtime because Codex raw ACP `sessionId` values are runtime-scoped; cross-runtime reuse of `session-1`-style ids returns `unknown session`.
  - real regression on rebuilt ngent:
    - `kimi`: ACP `session/load` succeeds for historical sessions but Kimi CLI 1.20.0 emits no replay `session/update` notifications, so `/session-history` currently returns `supported=true` with an empty message list under the standard ACP-only implementation.
    - `qwen`: a real locally-created Qwen session now replays transcript messages through the same ACP `session/list` + `session/load` path; env-gated provider E2E confirms the replay contains the unique prompt marker from the created session.
  - validation:
    - pass: `go test ./internal/agents/qwen -count=1`
    - pass: `E2E_QWEN=1 go test ./internal/agents/qwen -run 'TestQwenE2E(Smoke|SessionTranscriptReplay)$' -count=1 -v -timeout 180s`
    - pass: `go test ./internal/agents/...`
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`
    - pass: real `opencode` `/session-history` regression on rebuilt ngent
    - pass: real `codex` `/session-history` regression on rebuilt ngent
    - observed: real `kimi` `session/load` returns no replay updates on Kimi CLI 1.20.0
- `Post-M8` Qwen session-history replay fix completed:
    - direct ACP `session/load` for that session replayed `user_message_chunk` and `agent_message_chunk`, proving Qwen itself was returning transcript content.
    - ngent `GET /v1/threads/{threadId}/session-history?sessionId=...` initially failed with `503 UPSTREAM_UNAVAILABLE` and `qwen: session/load: qwen: connection closed`.
  - root cause:
    - ngent transcript replay parsing treated Qwen `tool_call_update` notifications as fatal because their `content` payload is an array/object, not the `{type,text}` shape used by text chunks.
    - the ACP stdio transport closes the whole connection when a notification handler returns an error, so one unexpected Qwen update aborted the replay before the RPC response arrived.
  - fix:
    - relaxed shared ACP update parsing so only text-bearing `user_message_chunk` / `agent_message_chunk` notifications are decoded strictly; non-text update payloads are ignored instead of aborting replay.
    - extended Qwen transcript fake-process coverage to include a `tool_call_update` regression payload.
  - validation:
    - pass: `go test ./internal/agents -run 'TestParseACPUpdate' -count=1`
    - pass: `go test ./internal/agents/qwen -run 'SessionTranscript' -count=1`
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

## Previous Update (2026-03-11)

- `Post-M8` codex session identity and replay normalization completed:
  - fixed fresh Codex `New session` persistence so ngent no longer stores provisional runtime ids like `session-1` as the thread session binding when a durable `_meta.threadId` is not yet available.
  - deferred initial `session_bound` persistence/emission for fresh Codex sessions until a stable session id can be resolved after the first prompt, then updated in-memory and persisted thread `agentOptions.sessionId` with the durable id.
  - validation:
    - pass: `go test ./internal/agents/codex -run 'Test(CodexShouldDeferInitialSessionBinding|NormalizeCodexSessionListResultUsesStableThreadID|CodexSessionMatchesIDAcceptsStableAndRawIDs|CodexStableSessionIDFallsBackToRawSessionID)$' -count=1`

## Previous Update (2026-03-09)

- Kimi CLI ACP integration completed:
  - implemented `internal/agents/kimi` with one-turn ACP stdio lifecycle and fail-closed permission handling.
  - wired `kimi` into startup preflight, `/v1/agents`, thread allowlist, turn factory, model discovery, and startup config-catalog refresh.
  - added dual startup syntax fallback for current upstream docs drift: try `kimi acp`, then `kimi --acp` if ACP initialize closes immediately.
  - added fake-process provider tests, fallback coverage, and server/httpapi allowlist coverage.
  - fixed Kimi thread model switching: `POST /v1/threads/{threadId}/config-options` with `configId=model` now selects the target model via Kimi process startup `--model`, instead of assuming ACP `session/set_config_option(model)` is implemented.
  - Kimi stream/config discovery paths now also pass the selected model through both process startup args and `session/new` hints for compatibility.
- Shared agent config/state refactor completed:
  - extracted common built-in agent fields `Dir`, `ModelID`, and `ConfigOverrides` into shared `internal/agents/agentutil.Config`.
  - extracted shared thread-safe mutable agent state into `internal/agents/agentutil.State`.
  - migrated `gemini`, `opencode`, `qwen`, `kimi`, `codex`, and `claude` to reuse the shared state helper instead of keeping duplicated per-provider copies of model/config override logic.
  - kept protocol/runtime behavior provider-specific; only constructor validation and common mutable state handling were unified.
- Web UI Kimi icon completed:
  - wired `kimi` avatar rendering in the Web UI to use the new asset with the existing `--contain` image treatment.
  - fixed the remaining New Thread modal agent-card icon map so Kimi now renders consistently there as well.
  - removed the forced white background from all `--contain` agent icons in message/thread views and from the modal's Kimi/OpenCode icon markup.
- validation:
  - pass: `cd internal/webui/web && npm run build`
  - pass: `go test ./...`

## Status

### Done

- `M0` completed: repository memory files, architecture/spec docs, API/DB/context strategy, and compilable skeleton.
- `M1` completed:
  - implemented `GET /healthz` and `GET /v1/agents`.
  - added optional bearer-token auth for `/v1/*` via `--auth-token`.
  - enforced localhost default listen policy with explicit `--allow-public=true` for public interfaces.
  - added startup JSON log fields and unified JSON error envelope.
- `M2` completed:
  - implemented SQLite storage with `database/sql` + `modernc.org/sqlite`.
  - added idempotent migration runner with `schema_migrations` version tracking.
  - created tables/indexes for clients/threads/turns/events.
- `M3` completed:
  - implemented thread create/list/get APIs.
  - enforced `X-Client-ID`, client upsert, allowlisted agents, and absolute `cwd` validation.
  - enforced cross-client thread access as `404`.
- `M4` completed:
  - implemented turn streaming endpoint: `POST /v1/threads/{threadId}/turns` returning SSE.
  - introduced `internal/agents` streaming interface and `FakeAgent` (10-50ms delta cadence).
  - added in-memory turn controller to enforce one active turn per thread.
  - implemented cancel endpoint: `POST /v1/turns/{turnId}/cancel`.
  - persisted every SSE event into `events` and finalized turn with aggregated `response_text`.
  - implemented history endpoint: `GET /v1/threads/{threadId}/history` with optional `includeEvents=true`.
  - added tests for SSE event sequence, history consistency, cancel behavior, and same-thread conflict (`409`).
- `M5` completed:
  - added `internal/agents/acp` stdio JSON-RPC provider with lifecycle `initialize -> session/new -> session/prompt`.
  - added ACP inbound request handling for `session/request_permission` and method-not-found fallback for unknown requests.
  - added `testdata/fake_acp_agent` deterministic executable for permission bridge testing.
  - added permission bridge endpoint `POST /v1/permissions/{permissionId}` and SSE event `permission_required`.
  - enforced fail-closed permission behavior on timeout/disconnect with consistent cancelled convergence in fake ACP flow.
  - added M5 tests for permission_required event, approved continuation, timeout auto-deny, and SSE disconnect convergence.
- `M6` completed:
  - added runtime codex configuration flags `--codex-acp-go-bin` and `--codex-acp-go-args`.
  - updated `/v1/agents` codex status: `unconfigured` when bin is absent, `available` when configured.
  - switched turn execution to per-thread lazy provider resolution (`TurnAgentFactory`), enabling codex ACP startup only when turn begins.
  - wired codex turns to `internal/agents/acp` with process working dir set to `thread.cwd`.
  - kept default test suite codex-independent and added optional env-gated codex smoke test (`E2E_CODEX=1`, `CODEX_ACP_GO_BIN`).
  - added lazy startup test to verify provider factory is not called during thread creation.
- `M7` completed:
  - added context-window prompt injection from `threads.summary + recent non-internal turns + current input`.
  - added runtime controls: `--context-recent-turns`, `--context-max-chars`, `--compact-max-chars`.
  - added `turns.is_internal` migration and storage support for internal compact turns.
  - added `POST /v1/threads/{threadId}/compact` to generate and persist rolling summaries.
  - updated history API default behavior to hide internal turns (`includeInternal=true` opt-in).
  - added tests for injected prompt visibility, compact summary effect, and restart recovery using shared SQLite file.
- `M8` completed:
  - preserved one-active-turn-per-thread behavior (`409`) and added parallel multi-thread test coverage.
  - added thread-agent idle TTL reclaim (`--agent-idle-ttl`) with structured reclaim/close logs.
  - added graceful shutdown workflow with active-turn drain + timeout force-cancel logging.
  - aligned API/SSE error code set to include `TIMEOUT` and `UPSTREAM_UNAVAILABLE` while removing non-standard codes from baseline.
  - validated SSE disconnect fail-closed behavior and non-hanging turn convergence.
  - updated acceptance checklist with executable `go test` and `curl` verification commands.
- `Post-M8` maintenance completed:
  - finalized canonical Go module path as `github.com/beyond5959/ngent`.
  - replaced placeholder import path references across in-repo Go sources/tests.
- `Post-M8` embedded codex migration completed:
  - switched codex provider from external `codex-acp-go` path-based process spawning to embedded `github.com/beyond5959/acp-adapter/pkg/codexacp`.
  - removed user-facing codex binary path flags; codex runtime is now linked into server and lazily created per thread on first turn.
  - kept HTTP API semantics unchanged (`threads/turns/sse/permissions/history`) and preserved permission fail-closed round-trip.
  - updated `/v1/agents` codex status contract to runtime preflight-based `available|unavailable`.
  - updated codex smoke test gate to `E2E_CODEX=1` without `CODEX_ACP_GO_BIN` path dependency.
- `Post-M8` real local codex regression completed:
  - fixed embedded runtime lifecycle bug in `internal/agents/codex/embedded.go` (`runtime.Start(context.Background())` instead of timeout-bound context) and kept retry-on-turn-start-failure guard.
  - updated context composer so first turn with empty summary/history passes raw input, preserving slash-command semantics for embedded acp-adapter flows.
  - executed real HTTP/SSE regression with required prompts plus same-thread conflict (`409`), cancel convergence, and permission round-trip (`approved` + `declined`) using `/mcp call` on fresh threads.
- `Post-M8` docs refresh completed:
  - added root `README.md` in English with project goal, `go install` instructions, and startup examples (local/public/auth) using default DB home `$HOME/.ngent`.
  - removed manual `mkdir` steps from README startup examples and documented that server auto-creates `$HOME/.ngent` for default db path.
- `Post-M8` db-path default improvement completed:
  - changed default `--db-path` from relative `./ngent.db` to `$HOME/.ngent/ngent.db`.
  - added startup auto-create for db parent directory so users can run without explicitly passing `--db-path`.
  - added unit tests for default path resolution and db parent directory creation.
- `Post-M8` cwd policy simplification completed:
  - removed runtime CLI parameter `--allowed-root`.
  - server now allows user-specified absolute `cwd` paths by default.
  - updated docs and tests to reflect absolute-cwd policy.
- `Post-M8` docs framing update completed:
  - adjusted README/SPEC/API/ARCHITECTURE wording to emphasize ACP-compatible multi-agent goal.
  - kept current-state note explicit: built-in providers are `codex`, `claude`, `opencode`, `gemini`, `kimi`, and `qwen`.
  - simplified README startup path to `ngent` with explicit `ngent --help` guidance.
- `Post-M8` startup log UX simplification completed:
  - replaced startup JSON line with multi-line human-readable stderr summary (QR code + port and URL hint).
  - added per-request completion logs containing `req_time`, `method`, `path`, `ip`, `status`, `duration_ms`, and `resp_bytes`.
  - normalized structured log `time` and request log `req_time` to UTC `time.DateTime` at second precision.
  - added unit test coverage for startup summary rendering and request completion log fields.
- `Post-M8` LAN-friendly default bind completed:
  - changed default bind to `0.0.0.0:8686` and `--allow-public` default to `true` so other devices can connect via the startup QR code.
- `Post-M8` qwen ACP integration completed:
  - implemented `internal/agents/qwen` with one-turn process lifecycle (`qwen --acp`) and ACP flow `initialize -> session/new -> session/prompt`.
  - implemented `session/update` delta extraction (`agent_message_chunk` + `content.text`) and `session/request_permission` fail-closed mapping (`approved/declined/cancelled`).
  - wired qwen into main server startup preflight, `/v1/agents` supported list, thread agent allowlist, and turn agent factory.
  - added/updated tests for qwen provider and server wiring (`main` + `httpapi` qwen allowlist coverage).
  - executed validation:
    - pass: `qwen --version` (`0.11.0`)
    - pass: `go test ./internal/agents/qwen -count=1`
    - pass: `go test ./cmd/ngent ./internal/httpapi -count=1`
    - pass: `E2E_QWEN=1 go test ./internal/agents/qwen -run TestQwenE2ESmoke -v -timeout 120s`
- `Post-M8` ACP stdio transport refactor completed:
  - extracted shared transport package `internal/agents/acpstdio` (JSON-RPC stdio call/notify, inbound request handling, parse helpers, process termination helper).
  - refactored `internal/agents/opencode` and `internal/agents/qwen` to reuse `acpstdio` while preserving provider-specific behavior.
  - executed regression:
    - pass: `go test ./internal/agents/opencode ./internal/agents/qwen -count=1`
    - pass: `go test ./...`
    - pass: `E2E_OPENCODE=1 go test ./internal/agents/opencode -run TestOpenCodeE2ESmoke -v -timeout 90s`
    - pass: `E2E_QWEN=1 go test ./internal/agents/qwen -run TestQwenE2ESmoke -v -timeout 120s`
- `F2` completed:
  - created `src/types.ts`: full TypeScript interface set (Thread, Turn, Message, PermissionRequest, StreamState, AppState, etc.).
  - created `src/utils.ts`: generateUUID (crypto.randomUUID + fallback), formatTimestamp, formatRelativeTime, isAbsolutePath, escHtml, debounce.
  - created `src/store.ts`: singleton AppStore with pub/sub (subscribe → unsubscribe fn), localStorage persistence for clientId/authToken/serverUrl/theme, resetClientId() helper.
  - created `src/components/settings-panel.ts`: slide-in drawer with Client ID display+copy+reset, Bearer Token input, 3-way theme toggle (Light/System/Dark), Server URL input; all changes persist via store immediately.
  - updated `src/main.ts`: imports store for theme init, wires settings button → settingsPanel.open(), system-theme change listener.
  - added settings drawer CSS to style.css (overlay, slide-in animation, theme button group).
  - implemented complete CSS design system with CSS custom properties for light/dark themes (`[data-theme]`).
  - built two-column IM layout: 260px sidebar (brand, search, thread list, settings footer) + flex main chat area.
  - thread list items with avatar, title, preview, agent badge, relative time, and unread dot.
  - chat header with thread title, agent badge, cwd, compact/cancel action buttons.
  - scrollable message list with user bubbles (right, blue) and agent bubbles (left, neutral), typing indicator, permission card, day divider.
  - input area with auto-resize textarea, send button, and keyboard hint.
  - empty state for no-thread-selected view.
  - basic responsive: ≤768px sidebar hides, mobile menu toggle shows.
  - system-theme detection on boot + `prefers-color-scheme` listener for live updates.
  - initialized Vite + TypeScript frontend under `internal/webui/web/`.
  - created `internal/webui/webui.go` with `//go:embed web/dist` and SPA fallback handler.
  - registered `FrontendHandler` in `httpapi.Config`; non-API paths served by frontend, API routes unaffected.
  - updated `cmd/ngent/main.go` to pass `webui.Handler()` and print a startup QR code for opening the UI.
  - updated `Makefile` with `build-web`, `build` targets; updated `.gitignore` for `node_modules`.
  - `go test ./...` all green; end-to-end: `GET /` → 200 HTML, `/threads` SPA fallback → 200, `/v1/agents` → JSON, `/healthz` → JSON.
  - standardized `/v1/agents` display names to `Codex` and `Claude Code`.
  - synchronized test fixtures and API documentation examples with the same canonical names.
  - `/v1/agents` output uses display names (not lowercase ids): `Codex`, `Claude Code`.
- `F3` completed:
  - created `src/api.ts`: ApiClient with ApiError class; getAgents(), getThreads(), createThread(); reads serverUrl/clientId/authToken from store on every request.
  - created `src/components/new-thread-modal.ts`: centered modal with agent card grid (radio, disabled for unavailable), CWD absolute-path validation, optional title, collapsible JSON agent-options textarea, submit with spinner, error banner; targeted DOM updates avoid full re-render during typing.
  - appended modal/form/agent-grid/skeleton/spinner CSS to style.css.
  - rewrote `src/main.ts`: init() loads agents+threads in parallel, store.subscribe drives updateThreadList()+updateChatArea(); skeleton loading state; search filter; thread click sets activeThreadId; new-thread button opens modal; error banner on API failure.
  - `tsc -b && vite build` passes; `go test ./...` all green.
- `F4` completed:
  - created `src/sse.ts`: `TurnStream` class using `fetch` + `ReadableStream` (not `EventSource`, which lacks POST/header support); parses `event:/data:` SSE lines delimited by `\n\n`; dispatches `onTurnStarted`, `onDelta`, `onCompleted`, `onError`, `onPermissionRequired`, `onDisconnect` callbacks; supports `abort()` for clean cancellation.
  - added `startTurn(threadId, input, callbacks)` and `cancelTurn(turnId)` to `ApiClient` in `src/api.ts`.
  - rewrote `src/main.ts` with full message rendering and smart subscribe handler:
    - `handleSend()`: adds user message → sets `activeStreamMsgId` sentinel → sets store streamState → appends streaming bubble to DOM → starts SSE stream.
    - `onDelta`: targeted DOM update on `#bubble-<id>` (no re-render, no flicker); removes typing indicator on first delta.
    - `onCompleted`/`onError`/`onDisconnect`: clears stream tracking → adds finalized message to store → clears streamState; subscribe re-renders list with final message.
    - subscribe handler: full `updateChatArea()` only on thread switch; skips `updateMessageList()` while `activeStreamMsgId` is set (streaming bubble live in DOM); `updateInputState()` always.
    - `handleCancel()`: sets store streamState to `cancelling`, calls `api.cancelTurn()`.
    - Cancel button in chat header: hidden by default, shown during streaming via `updateInputState()`.
  - added `.chat-header-meta` style to `style.css`.
  - `tsc -b && vite build` passes; `go test ./...` all green.

- `F5` completed:
  - added `api.getHistory(threadId)` calling `GET /v1/threads/{threadId}/history`, returns `Turn[]`.
  - added `turnsToMessages(turns)`: converts server `Turn[]` to client `Message[]`; skips `isInternal` turns; uses stable IDs `${turnId}-u` / `${turnId}-a`; maps `cancelled`/`error`/`completed` turn statuses to `MessageStatus`; falls back `completedAt → createdAt` for timestamp.
  - added `loadHistory(threadId)`: async, loads history on thread switch; guards against stale response (navigated away / streaming in progress); updates `store.messages[threadId]`.
  - updated `updateChatArea()`: shows cached messages immediately on revisit (no spinner flash); shows centered loading spinner for first visit; always fires `loadHistory()` in background to keep view fresh.
  - added `.message-list-loading` + `.loading-spinner` CSS (centered spinner, reuses `@keyframes spin`).
  - `tsc -b && vite build` passes; `go test ./...` all green.

- `F6` completed:
  - created `src/components/permission-card.ts`: `mountPermissionCard(listEl, event)` appends an inline permission card to the message list; 15s countdown timer with `setInterval` at 100ms ticks; Allow/Deny buttons call `api.resolvePermission()`; 409 (already resolved) treated as silent success; `showResolved()` snaps progress bar width, recolors card header/border, replaces buttons with resolved label; timeout auto-transitions to `permission-card--timeout` state.
  - added `api.resolvePermission(permissionId, outcome)` — `POST /v1/permissions/{permissionId}`.
  - wired `onPermissionRequired` callback in `handleSend()`: calls `mountPermissionCard(listEl, event)`.
  - added CSS: `.permission-progress` + `.permission-progress-bar` (width-animated, `transition: width .1s linear`); `.permission-card--approved|declined|timeout` outcome states (border + header recolor); `.permission-resolved--approved|declined|timeout` label colors; `.perm-avatar` warning-color avatar.
  - `tsc -b && vite build` passes; `go test ./...` all green.

- `F7` completed:
  - created `src/markdown.ts`: configured `marked@^9` with custom renderer; `html()` override returns `escHtml(text)` (XSS protection); `code()` renderer emits `.code-block` HTML with hljs syntax highlighting, header (lang label + Copy button), `<pre>` with `<code class="hljs">`, and optional "Show all N lines" expand button for blocks >20 lines; registered go/typescript/javascript/python/bash/json/yaml language subset.
  - exported `renderMarkdown(text)` — calls `marked.parse` synchronously.
  - exported `bindMarkdownControls(container)` — binds `.code-copy-btn` (clipboard + 2s "Copied ✓" feedback), `.code-expand-btn` (toggle `code-pre--collapsed`), `.msg-copy-btn` (full bubble text copy); idempotent via `data-bound` guard.
  - updated `src/main.ts`: imported `renderMarkdown` + `bindMarkdownControls`; `renderMessage()` uses `renderMarkdown(bodyText)` + `message-bubble--md` class for agent `done` messages; adds `<button class="msg-copy-btn">⎘</button>` in message-meta for done messages; `updateMessageList()` calls `bindMarkdownControls(listEl)` after HTML injection.
  - added CSS to `style.css`: `.message-bubble--md` (white-space: normal; nested p/h1-h3/ul/ol/blockquote/inline-code/a styles); `.message--agent .message-group { max-width: min(660px, 90%) }` for code block width; `.code-block`, `.code-block-header`, `.code-lang`, `.code-copy-btn` (hover-visible, 2s "Copied ✓" variant); `.code-pre`, `.code-pre--collapsed` (340px max-height + gradient fade `::after`); `.code-expand-btn` (full-width, text-secondary); `.msg-copy-btn` (hover-visible on agent message hover); hljs CSS-variable token colour system with light/dark variants.
  - `tsc -b && vite build` passes; `go test ./...` all green.

- `F8` completed:
  - added `isNearBottom(el)` helper (100px threshold).
  - smart auto-scroll in `onDelta`: only scrolls when user is already at/near bottom; lets user read history uninterrupted during streaming.
  - added scroll-to-bottom button (`.scroll-bottom-btn`) positioned absolute in new `.message-list-wrap` container: shown when user scrolls away from bottom, hidden on `updateMessageList` and smooth-scroll on click.
  - added `bindScrollBottom()`: scroll-event listener syncs button visibility; button `click` calls `listEl.scrollTo({ behavior: 'smooth' })`.
  - added `bindGlobalShortcuts()` (called once from `init()`):
    - `/` key: focuses `#search-input` when not already in an input (preventDefault).
    - `Cmd+N` / `Ctrl+N`: opens new thread modal.
    - `Escape` (contextual, most-specific first): (1) closes mobile sidebar if open, (2) clears + blurs search if search is focused, (3) cancels active stream via `handleCancel()`.
  - mobile UX: thread click in `updateThreadList()` now removes `sidebar--open` class so sidebar closes automatically on thread select.
  - input hint updated to reflect shortcuts: `⌘ Enter to send · Esc to cancel · / to search`.
  - CSS: `.message-list` wrapped in `.message-list-wrap` (`flex:1; position:relative; min-height:0; overflow:hidden`); `.message-list` changed from `flex:1` to `height:100%`; `.scroll-bottom-btn` circular, shadow, hover translate.
  - `tsc -b && vite build` passes; `go test ./...` all green.

- `F9` completed:
  - updated startup output to include a QR code with port hint (for LAN access) and updated `TestPrintStartupSummary`.
  - updated `docs/API.md`: added Frontend Web UI endpoint section (`GET /` returns `text/html`, `GET /assets/*` serves static assets, SPA fallback for non-API paths).
  - updated `docs/ACCEPTANCE.md`: added Requirement 13 (Embedded Web UI) with `go test ./internal/webui` verification command.
  - updated `README.md`: added "Web UI" section with startup summary example, feature list (threads/streaming/permissions/history/themes), no-Node-at-runtime note, and `make build-web && go build ./...` rebuild instructions.
  - verified ADR-018 (Embedded Web UI via Go embed) already present in `docs/DECISIONS.md`.
  - final verification: `go test ./...` all green; `tsc -b && vite build` passes; `go build ./...` succeeds.

### Done (all frontend milestones)
- Optional enhancement 1: add embedded-runtime preflight diagnostics endpoint (auth, app-server reachability, version compatibility).
- Optional enhancement 2: WebSocket streaming transport in addition to SSE.
- Optional enhancement 3: History/event pagination and cursor-based replay.
- Optional enhancement 4: RBAC and finer-grained authorization policies.
- Optional enhancement 5: Expanded audit logs and retention tooling.
- Optional enhancement 6: expose environment diagnostics for codex local state DB/schema mismatches (for example `~/.codex/state_5.sqlite` migration drift) and app-server method compatibility.

- `Post-M8` Claude Code embedded provider completed:
  - implemented `internal/agents/claude/embedded.go` backed by `github.com/beyond5959/acp-adapter/pkg/claudeacp.EmbeddedRuntime`.
  - preflight checks `ANTHROPIC_AUTH_TOKEN` environment variable; status reports `available` when set, `unavailable` otherwise.
  - `ANTHROPIC_AUTH_TOKEN` and `ANTHROPIC_BASE_URL` are read from environment via `claudeacp.DefaultRuntimeConfig()` at startup.
- `Post-M8` thread delete lifecycle completed:
  - added `DELETE /v1/threads/{threadId}` with same-client ownership check and `409 CONFLICT` when a thread has an active turn.
  - guarded deletion with a temporary turn-controller lock to prevent new turns from starting during delete.
  - implemented transactional storage deletion in `internal/storage` with dependent cleanup (`events` -> `turns` -> `threads`).
  - wired Web UI thread-list delete action with confirmation and local state cleanup (threads/messages/active selection/stream state).
  - executed validation:
  - added unit tests (`TestPreflight_*`, `TestNew_*`, `TestClose_*`, `TestDefaultRuntimeConfig_ReadsEnv`) covering token presence/absence, default/custom timeouts, and idempotent close.
  - added optional real smoke test (`E2E_CLAUDE=1 go test ./internal/agents/claude/ -run TestClaudeE2ESmoke -v -timeout 120s`); confirmed `PONG` response and `stopReason=end_turn` (16.68s).
  - wired claude into `cmd/ngent/main.go`: preflight call, `"claude"` in `AllowedAgentIDs`, `case "claude"` in `TurnAgentFactory`, real status in `supportedAgents`.
  - updated `main_test.go`: `supportedAgents` signature extended with `claudeAvailable bool`; added claude id/status assertions.
  - executed validation:
    - pass: `go build ./...`
    - pass: `go test ./...` (all packages green)
    - pass: `E2E_CLAUDE=1 go test ./internal/agents/claude -run TestClaudeE2ESmoke -v -timeout 120s` (response: `PONG`, stopReason: `end_turn`)

- `Post-F9` CI and release pipeline completed:
  - removed `internal/webui/web/dist/` and `tsconfig.tsbuildinfo` from git tracking (`git rm --cached`); added both to `.gitignore`.
  - created `.github/workflows/ci.yml`: triggers on every push/PR (non-tag); steps: Go + Node.js 20 setup, `make build-web`, gofmt check, `go test ./...`.
  - created `.github/workflows/release.yml`: triggers on `v*.*.*` tags; runs `goreleaser release --clean` with `GITHUB_TOKEN`.
  - created `.goreleaser.yml` (version 2): `before.hooks: make build-web`; cross-compiles linux/darwin/windows × amd64/arm64 (windows arm64 excluded) with `CGO_ENABLED=0`; produces `ngent_VERSION_OS_ARCH.tar.gz` archives + `checksums.txt`.
  - updated `Makefile`: `make build` now outputs to `bin/ngent` (was `go build ./...`).
  - updated `README.md`: replaced `go install` with "Download pre-built binary" table + "Build from source" (`make build`) instructions.
  - updated `AGENTS.md`: replaced "MUST keep web/dist committed" rule with "MUST NOT commit web/dist"; added CI/Release pipeline section.



## Latest Verification

- Date: `2026-02-28`
- Commands executed:
  - `gofmt -w $(find . -name '*.go' -type f)`
  - `go test ./...`
  - `tsc -b && vite build` (frontend)
  - `go build ./...`
- Result:
  - formatting: pass
  - tests: pass (all packages green)
  - frontend build: pass (121 kB JS, 27 kB CSS)
  - full binary build: pass

## Latest Verification (ACP Transport Refactor Update)

- Date: `2026-03-03`
- Commands executed:
  - `go test ./internal/agents/opencode ./internal/agents/qwen -count=1`
  - `E2E_OPENCODE=1 go test ./internal/agents/opencode -run TestOpenCodeE2ESmoke -v -timeout 90s`
  - `qwen --version`
  - `go test ./internal/agents/qwen -count=1`
  - `E2E_QWEN=1 go test ./internal/agents/qwen -run TestQwenE2ESmoke -v -timeout 120s`
  - `go test ./cmd/ngent ./internal/httpapi -count=1`
  - `go test ./...`
- Result:
  - opencode/qwen package tests: pass
  - opencode real smoke: pass (`PONG`, `stopReason=end_turn`)
  - qwen version check: pass (`0.11.0`)
  - qwen package tests: pass
  - qwen real smoke: pass (`PONG`, `stopReason=end_turn`)
  - server/httpapi regression tests: pass
  - full repo tests: pass

## Milestone Plan (M0-M8)

### M0: Documentation and Skeleton

- Scope: write mandatory memory documents and create compilable package layout.
- DoD:
  - required root/docs files exist and are coherent.
  - `go test ./...` passes.
  - `make run` starts the placeholder server.

### M1: Minimal HTTP Server

- Scope: `/healthz`, `/v1/agents`, auth toggle, startup logs.
- DoD:
  - endpoints return stable JSON.
  - startup log is concise and includes listen endpoint, db path, and supported agent statuses.
  - tests cover happy path and invalid config.

### M2: SQLite Storage and Migrations

- Scope: storage layer, schema migration runner, storage unit tests.
- DoD:
  - clients/threads/turns/events tables created by migrations.
  - CRUD coverage for core entities.
  - restart can read persisted records.

### M3: Threads API

- Scope: create/list/get thread APIs and validation.
- DoD:
  - strict request validation (agent allowlist, cwd absolute path).
  - API tests cover valid/invalid requests and multi-client isolation.

### M4: Turns SSE with Fake Agent

- Scope: create turns, stream SSE events, query history, cancel turn.
- DoD:
  - one active turn per thread enforced.
  - cancel path is observable quickly in stream and persisted state.
  - tests cover stream ordering and conflict handling.

### M5: ACP Stdio Provider and Permission Bridge

- Scope: ACP provider integration with fake acp agent and permission forwarding.
- DoD:
  - permission requests are forwarded to client and block until decision.
  - timeout or missing decision fails closed.
  - tests cover allow/deny/timeout/cancel races.

### M6: Codex Provider

- Scope: codex-acp-go provider wiring and optional integration tests.
- DoD:
  - provider can be enabled by config.
  - integration test is optional and skipped by default without env setup.

### M7: Context Window Management

- Scope: summary plus recent turns policy, compact trigger, restart recovery.
- DoD:
  - prompt construction follows documented budget policy.
  - compaction updates durable summary.
  - restart resumes with consistent context state.

### M8: Reliability Finish

- Scope: conflict strategy, TTL cleanup, graceful shutdown, acceptance alignment.
- DoD:
  - clear behavior for concurrent operations and stale sessions.
  - background cleanup does not break active threads.
  - acceptance checklist fully green.

## Notes

- Canonical module path is now finalized as `github.com/beyond5959/ngent`.
- All in-repo Go import paths were updated from placeholder path to canonical path.

- `Post-F9` Web UI multi-thread streaming behavior fixed:
  - removed UI behavior that aborted an in-flight SSE stream when switching threads.
  - changed client stream tracking from single global `streamState` to per-thread `streamStates`.
  - maintained per-thread stream runtime maps (stream handle, delta buffer, start time), so background threads can keep streaming and finalize correctly.
  - wired send/cancel/input disable logic to current thread only, enabling concurrent in-flight turns across different threads.
  - executed validation:

- `Post-F9` permission countdown updated to 2 hours:
  - changed server default permission timeout to `2 * time.Hour`.
  - changed Web UI Permission Required countdown to 2 hours and display format to `H:MM:SS`.

- `Post-F9` permission card persistence across thread switch fixed:
  - stored pending permission requests per thread in Web UI runtime state.
  - when switching back to a thread, pending Permission Required cards are re-mounted with original deadline.
  - resolved/timeout outcomes remove pending records to avoid stale prompts.
  - executed validation:

- `Post-F9` thread model selection and switching completed:
  - added thread update API `PATCH /v1/threads/{threadId}` (agentOptions-only payload) with ownership checks and active-turn conflict (`409`).
  - added model discovery API `GET /v1/agents/{agentId}/models` (ACP handshake-backed).
  - storage layer now supports `UpdateThreadAgentOptions` and updates thread `updated_at`.
  - successful thread option update closes cached per-thread provider, so next turn re-initializes with new model config.
  - wired runtime model discovery into all providers:
    - `codex`/`claude`: ACP embedded `session/new.configOptions`.
    - `gemini`/`kimi`/`opencode`/`qwen`: ACP `session/new.models.availableModels`.
  - wired `agentOptions.modelId` forwarding into all providers:
    - embedded `codex`/`claude` now pass `model` in ACP `session/new`.
    - `gemini` now passes `model` in `session/new` and `session/prompt`.
    - `kimi` passes `model` in `session/prompt`, and uses `model` + `modelId` during config/model discovery handshakes.
    - `opencode` passes `modelId` in `session/prompt`; `qwen` passes `model` in `session/prompt`.
  - Web UI updates:
    - new-thread modal model selector removed (create flow keeps agent/cwd/title/advanced JSON only).
    - active thread header switched from free-text model input to ACP-backed model dropdown + Apply action.
    - model controls are disabled while model lists load and during streaming turns.
  - executed validation:

- `Post-F9` thread session model config switched to ACP `configOptions`:
  - added thread-scoped config options APIs:
    - `GET /v1/threads/{threadId}/config-options`
    - `POST /v1/threads/{threadId}/config-options`
  - `POST` persists selected model/config state directly into sqlite thread metadata (no separate apply endpoint/action).
  - provider-side config option support added across all built-in agents so persisted thread selections can be synchronized at turn boundaries:
    - embedded: `codex`, `claude` (cached runtime session sync).
    - stdio: `opencode`, `qwen`, `gemini`, `kimi` (per-turn ACP handshake plus persisted selection forwarding).
  - Web UI changes:
    - removed thread header `Apply` button.
    - model dropdown persists immediately on selection.
    - model source switched from agent-level model catalog to thread-level `configOptions` (`category=model`).
    - model option descriptions are rendered under the selector in the chat header.
  - thread metadata sync:
    - successful model switch persists `agentOptions.modelId` for thread continuity and restart recovery.
  - executed validation:

- `Post-F9` thread reasoning selector and config override persistence completed:
  - backend now persists non-model session config selections under `agentOptions.configOverrides` when `POST /v1/threads/{threadId}/config-options` succeeds.
  - provider factories now restore persisted config overrides on fresh embedded sessions and per-turn stdio handshakes, so reasoning-style settings survive across future turns and restarts.
  - Web UI composer footer now renders both `Model` and `Reasoning` controls; reasoning options refresh from the latest thread `configOptions` after model changes.
  - reasoning control remains model-specific and is disabled/updated in the same active-turn safety envelope as model switching.
  - added coverage for config override persistence and thread agent-option parsing.
  - executed validation:

- `Post-F9` shared agent config catalog caching completed:
  - Web UI no longer re-fetches thread config options when switching between threads that use the same agent and already have a cached agent config catalog.
  - frontend now keeps:
    - thread-scoped current config state cache.
    - agent-scoped shared config catalog cache for model/reasoning option lists.
  - same-agent threads reuse the same model/reasoning lists while keeping independent selected current values derived from each thread's persisted `agentOptions`.
  - composer footer pills now show only the selected model/reasoning names, without leading `MODEL` / `REASONING` labels.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`

- `Post-F9` codex model-discovery stability improvement:
  - replaced per-request codex model discovery runtime startup/shutdown with a shared discovery client in `internal/agents/codex/models.go`.
  - behavior changes:
    - repeated `GET /v1/agents/codex/models` now reuses initialized embedded runtime instead of spawning and closing app-server each time.
    - one automatic retry with fresh discovery client when cached client becomes unhealthy.
    - explicit cleanup hook added in server shutdown (`codexagent.CloseDiscoveryClient()`).
  - local validation:
    - first call `GET /v1/agents/codex/models` took ~16s (initial discovery).
    - second call returned in ~0ms from shared client (no repeated startup/shutdown).
  - executed validation:

- `Post-F9` persisted agent config catalog completed:
  - added sqlite-backed `agent_config_catalogs` storage keyed by `agent_id + model_id`, with a reserved default snapshot row used when a thread has no explicit model selection yet.
  - `GET /v1/threads/{threadId}/config-options` and `GET /v1/agents/{agentId}/models` now read persisted catalog data first, so service restart can keep serving model/reasoning metadata without blocking on live provider queries.
  - `POST /v1/threads/{threadId}/config-options` now writes through both:
    - thread-local selected values into `threads.agent_options_json`
    - current model config catalog into sqlite for reuse across threads/restarts
  - service startup now launches a background catalog refresher that silently re-queries built-in agents and refreshes stored model/reasoning catalogs without delaying frontend availability.
  - Web UI config cache is now keyed by `agent + selected model`, so different threads on the same agent no longer accidentally reuse the wrong reasoning list for another model.
  - executed validation:

- `Post-F9` streaming bubble typing-indicator persistence completed:
  - Web UI streaming agent bubble now keeps the three animated dots rendered at the bottom of the bubble after the first delta arrives, until the turn finishes.
  - refactored transient streaming bubble DOM to use separate text and indicator regions so incremental `onDelta` updates no longer replace the indicator.

- `Post-F9` Web UI clipboard fallback and message-copy fix completed:
  - unified copy actions behind a best-effort clipboard helper that falls back to `document.execCommand('copy')` when `navigator.clipboard` is unavailable or blocked on LAN HTTP origins.
  - message copy buttons now copy the original message text payload instead of scraping rendered DOM text, so agent markdown replies no longer pull in code-block chrome such as `Copy` labels.
  - applied the same fallback path to code-block copy and settings-panel client-id copy to keep behavior consistent across the UI.

- `Post-F9` streaming bubble empty-line removal:
  - removed the blank spacer above the three animated dots before the first token arrives by hiding the empty text container in streaming bubbles.
  - typing indicator now sits directly under the top padding until real content starts streaming.
  - executed validation:

- `Post-F9` sidebar thread activity indicators completed:
  - thread list now shows a live spinner for any thread with an in-flight turn, so background work stays visible after switching to another thread.
  - when a background turn finishes, the spinner flips to a green check badge that stays on that thread until the user opens it again.
  - slowed the sidebar thread spinner slightly so the activity indicator reads as background work instead of a high-frequency busy loop.
  - executed validation:

- `Post-F9` sidebar thread drawer actions and rename completed:
  - replaced the direct delete icon in sidebar thread rows with a drawer trigger.
  - added drawer actions for inline rename and delete, with rename ordered before delete and delete styled as the only dangerous text action.
  - extended `PATCH /v1/threads/{threadId}` so thread title updates reuse the existing ownership and active-turn conflict model.
  - executed validation:

- `Post-F9` sidebar thread action popover refinement completed:
  - replaced the expanding inline drawer with a floating popover anchored to the three-dot trigger, so opening thread actions no longer changes sidebar row height.
  - kept rename and delete in the same order, with rename editing rendered in a floating panel and delete remaining the red dangerous action.
  - executed validation:

- `Post-F9` ACP plan streaming in Web UI completed:
  - added shared ACP `session/update` parsing for both `agent_message_chunk` and `plan`, with plan routed through a new per-turn `PlanHandler` context callback.
  - introduced SSE/history event `plan_update` so ACP plans are persisted alongside other turn events instead of being dropped at provider boundaries.
  - updated the Web UI to render live plan cards during streaming and restore the latest plan from turn history on reload.
  - executed validation:

 
- 2026-03-06: Removed the thread action trigger's per-thread actionLabel/title tooltip in the Web UI popover menu; the three-dot button now uses a neutral `aria-label` only, without hover text tied to the thread title.
- 2026-03-06: Moved the sidebar thread action menu and rename form into a sidebar-level floating layer instead of rendering them inside each thread row, so the rename UI is no longer clipped by the thread list or sidebar overflow.
- 2026-03-06: fixed embedded codex permission bridge timeout mismatch by aligning adapter-side `session/request_permission` wait window to 2h (was 30s), matching hub default timeout and avoiding premature fail-closed during manual approval.
- 2026-03-06: fixed embedded codex server-request compatibility for tool interaction:
  - `item/tool/requestUserInput` now returns schema-compatible answers (auto-select first option label per question) instead of `-32000 not supported`.
  - `item/tool/call` now returns structured tool failure payload (`success=false`) instead of JSON-RPC method error, so app-server no longer aborts the whole flow on this request type.
- 2026-03-09: unified ACP message-chunk constant usage across stdio providers by removing per-provider `updateTypeMessageChunk` definitions and reusing `agents.ACPUpdateTypeMessageChunk`.
- 2026-03-09: hid the Web UI Reasoning switch when the active agent exposes fewer than two reasoning choices, so agents without switchable reasoning no longer show a dead control.
- 2026-03-11: added opt-in `--debug` startup flag; when enabled, stderr now emits sanitized `acp.message` traces for ACP stdio and embedded-runtime request/response traffic, including session prompts, updates, and permission flows.
- 2026-03-11: added ACP session browsing/resume support across built-in agents:
  - introduced shared agent session abstractions for `session/list`, bound-session reporting, and initialize capability parsing.
  - built-in providers now:
    - list sessions through ACP `session/list` when supported.
    - load persisted `agentOptions.sessionId` through ACP `session/load` before prompting.
    - report the effective session id back to HTTP turns so the server can persist it.
  - added `GET /v1/threads/{threadId}/sessions` with cursor passthrough and graceful `supported=false` fallback for agents without ACP session-history support.
  - turn SSE now emits `session_bound`, and the server persists the thread session id without closing the active provider.
  - once a thread is bound to an ACP session, prompt building skips local recent-turn injection to avoid duplicating already-loaded ACP context.
  - Web UI now renders a right-side session sidebar with first-page load, `Show more`, active-session highlighting, and `New session` reset.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./internal/httpapi -run 'TestThreadSessionsListEndpoint|TestTurnSessionBoundPersistsSessionIDAndSkipsContextInjection' -count=1`
    - pass: `go test ./...`

- 2026-03-11: fixed Web UI session playback when selecting an existing ACP session from the right sidebar.
  - the active chat view now treats `(threadId, sessionId)` as its render scope instead of refreshing only on `threadId` changes.
  - `loadHistory()` now filters locally persisted turns by each turn's `session_bound` event so the center chat panel replays the selected session's ngent-recorded turns instead of leaving the previous session on screen.
  - session changes reported mid-stream by `session_bound` defer the full chat refresh until the active turn completes, so the live streaming bubble is not destroyed.

- 2026-03-11: fixed Web UI history replay for legacy session threads whose `/history` data lacks per-turn `session_bound` events.
  - session-scoped history filtering now falls back to showing all turns when a thread has no annotated session markers at all, instead of rendering an empty chat pane despite non-empty `/history`.
  - when a thread has exactly one annotated session, the selected session view also keeps older unannotated turns so pre-annotation history is still visible for that same session.

- 2026-03-13: allowed concurrent turns across different sessions on the same thread.
  - changed the runtime turn controller from thread-wide locking to `(threadId, sessionId)` scoping, while keeping delete/compact/shared thread mutations guarded at whole-thread scope.
  - changed cached provider reuse from thread scope to session/config scope so switching sessions mid-stream no longer reuses the wrong provider instance.
  - updated the Web UI to key cached messages, live stream state, and permission cards by `(threadId, sessionId)`, which keeps background session output from overwriting the currently visible session.
  - session-only `PATCH /v1/threads/{threadId}` updates now succeed while another session on the same thread is active, so the right-side session switcher can move to a new session before starting the next turn.

- 2026-03-13: fixed Web UI session switching while another session on the same thread is still streaming.
  - the chat subscribe loop now forces a full chat-area rebuild when the selected `(threadId, sessionId)` changes to a scope with an active stream whose bubble is not mounted in the current DOM.
  - returning to a background-streaming session now restores the loading/typing bubble, live partial text, and pending permission UI instead of rendering only the persisted message list.

- 2026-03-13: moved working-directory details into a chat-header Session Info popover in the Web UI.
  - removed the visible working-directory line from the chat header and added an info icon that appears only when the selected session already has a persisted `sessionId`.
  - clicking the icon now opens a `Session Info` popover showing `Session ID` and `Working Directory`, each with a dedicated copy action; clicking outside or pressing `Esc` closes it.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-13: added per-session loading indicators to the Web UI session sidebar.
  - the right-side `Sessions` list now shows the same spinner used by the left agent list when a specific `sessionId` on the active thread is still streaming.
  - session items derive their loading state from scope-local `streamStates`, so background activity is shown only on the matching session row.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-13: added persisted ACP slash-command support across built-in agents and the Web UI composer.
  - extended shared ACP `session/update` parsing with `available_commands_update`, and wired all built-in ACP providers to forward the latest slash-command snapshot through a shared per-turn callback.
  - added SQLite-backed `agent_slash_commands` caching plus `GET /v1/threads/{threadId}/slash-commands`, so every observed slash-command update is persisted and survives server restart.
  - updated the Web UI composer so typing `/` into an otherwise empty chat input opens a codex-style slash-command picker with keyboard navigation and command insertion for the active agent.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-13: fixed Web UI freezes when an agent has no cached slash commands.
  - changed the composer to fetch `GET /v1/threads/{threadId}/slash-commands` lazily when the user types `/`, instead of preloading on thread selection.
  - empty slash-command snapshots now close the picker immediately and leave `/` as ordinary message text, which avoids the previous empty-result refresh loop.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-13: fixed Kimi slash-command loss in the ACP turn pipeline.
  - root cause: real Kimi `kimi acp` emits `available_commands_update` immediately after `session/new` and before `session/prompt`, while the ngent Kimi provider had been installing its `session/update` handler too late and silently dropped that notification.
  - fixed the Kimi provider to start observing `session/update` before `session/new/session/load`, while still suppressing pre-prompt message chunks so transcript replay cannot leak into the live response stream.
  - added a regression test that forces `available_commands_update` to arrive during `session/new`.
  - executed validation:
    - pass: `go test ./internal/agents/kimi -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|WithFakeProcess|WithFakeProcessModelID)$' -count=1`
    - pass: `go test ./...`
    - pass: real local ngent + Kimi test on `http://127.0.0.1:8788` confirmed `session/update.available_commands_update` was logged between `session/new` and `session/prompt`
    - pass: `GET /v1/threads/{threadId}/slash-commands` returned 8 persisted Kimi commands after the first turn

- 2026-03-13: forced a backend slash-command refresh on each new `/` interaction in the Web UI.
  - root cause: once a thread had already populated the client-side slash-command cache, typing `/` reused that cache and did not issue another `GET /v1/threads/{threadId}/slash-commands`, which made the real network behavior diverge from the expected "query sqlite on slash entry" flow.
  - changed the composer so the first bare `/` in each slash interaction triggers a forced refresh for the active thread, while subsequent filtering inside the same interaction still reuses the in-memory snapshot.
  - the refresh guard resets when the user leaves slash mode, selects a command, sends the message, presses `Esc`, or clicks outside the composer.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`
    - pass: real local ngent + Kimi test on `http://127.0.0.1:8789`
    
- 2026-03-13: fixed codex embedded slash-command loss when `session/new` / `session/load` emitted updates before the first prompt.
  - root cause: the codex embedded provider subscribed to runtime updates only inside `streamOnce()`, but `acp-adapter` emits `available_commands_update` immediately after `session/new` / `session/load`, so the initial slash-command snapshot was dropped before ngent had any subscriber.
  - installed a runtime-level update monitor before `session/new` / `session/load`, cached the latest slash-command snapshot on the provider, and replayed that cached snapshot into each turn context before `session/prompt` starts.
  - kept prompt-time `available_commands_update` handling in the live stream path so later slash-command changes still propagate with the existing cancellation/error behavior.
  - added codex regression coverage for both the direct first-turn path and the "config options initialized the runtime before the first turn" path.
  - executed validation:
    - pass: `go test ./internal/agents/codex -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|ReplaysCachedSlashCommandsAfterConfigOptionsInit)$' -count=1`
    - pass: real local ngent + codex test on `http://127.0.0.1:8793`
    - pass: `GET /v1/threads/{threadId}/slash-commands` returned the persisted 7-command codex snapshot after the first turn
    - pass: sqlite `/tmp/ngent-codex-fix.db` stored `agent_id=codex` with 7 slash commands

- 2026-03-13: fixed the same pre-prompt slash-command timing bug in Qwen and OpenCode stdio providers.
  - root cause: both stdio providers called `session/new` / `session/load` before registering a `session/update` notification handler, and `acpstdio.Conn` drops notifications when no handler is installed.
  - moved notification handler registration to immediately after `initialize`, allowed `available_commands_update` through before prompt start, and continued suppressing pre-prompt transcript chunks/plan updates so session replay cannot leak into live output.
  - added one fake-process regression test per provider to verify `available_commands_update` emitted during `session/new` is captured.
  - executed validation:
    - pass: `go test ./internal/agents/qwen ./internal/agents/opencode -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|WithFakeProcess|WithFakeProcessModelID)?$' -count=1`
    - pass: real local ngent + qwen/opencode test on `http://127.0.0.1:8794`

- 2026-03-13: deduplicated ACP stdio stream notification handling across Kimi, Qwen, and OpenCode.
  - extracted one shared `agents.InstallACPStdioNotificationHandler(...)` helper that owns the common `session/update` parsing path for pre-prompt slash-command snapshots plus post-prompt message/plan streaming.
  - removed the previous copy-pasted handler bodies from the three providers so future slash-command or plan-stream changes only need one stdio implementation update.
  - executed validation:
    - pass: `go test ./internal/agents/kimi ./internal/agents/qwen ./internal/agents/opencode -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|WithFakeProcess|WithFakeProcessModelID)?$' -count=1`
    - pass: `go test ./...`

- 2026-03-13: applied the same pre-prompt slash-command handling and shared notification helper to Gemini.
  - Gemini had the same timing bug as the other stdio-backed providers: it registered `session/update` handling only after `session/new` / `session/load`, so any early `available_commands_update` would have been dropped by its custom `rpcConn`.
  - generalized the shared stdio logic into `agents.NewACPNotificationHandler(...)`, reused it from the existing stdio helper, and wired Gemini's custom `rpcConn` through the same handler builder.
  - added Gemini fake-process regression coverage for `available_commands_update` emitted during `session/new`.
  - executed validation:
    - pass: `go test ./internal/agents/gemini -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|WithFakeProcess|WithFakeProcessModelID)$' -count=1`
    - pass: real local ngent + Gemini test on `http://127.0.0.1:8795`
    - note: `GET /v1/threads/{threadId}/slash-commands` still returned `[]` in that real run, which indicates Gemini CLI did not emit `available_commands_update` on that session despite the handler now being installed early enough

- 2026-03-13: backfilled Codex slash commands during thread config initialization so `/` works before the first turn.
  - root cause: the Web UI opens Codex threads by loading `config-options`, which initializes the embedded runtime and caches `available_commands_update` in provider memory, but sqlite remained empty until a later turn replayed the snapshot through the normal turn-scoped slash-command handler.
  - added a shared `agents.SlashCommandsProvider` interface, implemented it in the embedded Codex provider, and taught the HTTP `config-options` path to persist a missing slash-command snapshot from the live provider as a best-effort backfill.
  - added regression coverage for both the provider method and the HTTP old-thread case where config options are already stored but slash commands are still missing from sqlite.
  - executed validation:
    - pass: `go test ./internal/agents/codex -run 'Test(StreamCapturesSlashCommandsEmittedBeforePrompt|StreamReplaysCachedSlashCommandsAfterConfigOptionsInit|SlashCommandsAfterConfigOptionsInit)$' -count=1`
    - pass: `go test ./internal/httpapi -run 'Test(ThreadSlashCommandsPersistAndLoad|ThreadSlashCommandsPersistAcrossRestart|ThreadConfigOptionsBackfillsSlashCommandsWhenCatalogAlreadyStored)$' -count=1`
    - pass: real local ngent + codex test on `http://127.0.0.1:8796`
    - pass: fresh Codex thread returned the 7-command slash snapshot from `GET /v1/threads/{threadId}/slash-commands` immediately after `GET /v1/threads/{threadId}/config-options`, before any turn was sent

- 2026-03-13: fixed fresh-thread Qwen slash commands by probing providers on `/slash-commands` cache miss.
  - root cause: a user could type `/` before the thread-opening `config-options` request finished; for Qwen that meant `GET /v1/threads/{threadId}/slash-commands` read sqlite too early and returned `[]` even though the provider emitted `available_commands_update` a moment later.
  - implemented `SlashCommandsProvider` on the Qwen stdio provider, cached slash-command snapshots inside the provider from both turn and config-session ACP notifications, and taught `GET /v1/threads/{threadId}/slash-commands` to best-effort backfill sqlite directly from the live provider when the row is missing.
  - added regression coverage for Qwen `ConfigOptions()` followed by `SlashCommands()`, plus one HTTP test that verifies the slash-commands endpoint itself backfills a missing snapshot.
  - executed validation:
    - pass: `go test ./internal/agents/qwen ./internal/httpapi -run 'Test(StreamCapturesSlashCommandsEmittedBeforePrompt|SlashCommandsAfterConfigOptionsInit|ThreadConfigOptionsBackfillsSlashCommandsWhenCatalogAlreadyStored|ThreadSlashCommandsEndpointBackfillsMissingSnapshot)$' -count=1`
    - pass: real local ngent + qwen test on `http://127.0.0.1:8798`
    - pass: fresh Qwen thread returned `/bug`, `/compress`, `/init`, and `/summary` from the very first `GET /v1/threads/{threadId}/slash-commands` before any turn was sent

- 2026-03-13: unified provider-local ACP slash-command caching across the direct stdio agents.
  - Kimi, Qwen, OpenCode, and Gemini all share the same underlying ACP behavior: `available_commands_update` can arrive during `session/new` in both turn streaming and config-session probes, so probing slash commands only from sqlite is not enough for fresh threads.
  - introduced `agents.SlashCommandsCache` and wired all four direct ACP providers to feed it from both `Stream()` and `runConfigSession()` by wrapping the request context's slash-command handler instead of duplicating provider-local cache plumbing.
  - each provider now implements `SlashCommandsProvider`, so thread initialization and `GET /v1/threads/{threadId}/slash-commands` can backfill sqlite from the live provider snapshot even before the first turn persists anything.
  - added config-session regression coverage for Kimi, OpenCode, and Gemini to lock in the `ConfigOptions() -> SlashCommands()` path alongside the earlier Qwen test.
  - executed validation:
    - pass: `go test ./internal/agents/kimi ./internal/agents/opencode ./internal/agents/gemini ./internal/agents/qwen -run 'Test(StreamCapturesSlashCommandsEmittedBeforePrompt|SlashCommandsAfterConfigOptionsInit|WithFakeProcess|WithFakeProcessModelID)$' -count=1`
    - pass: `go test ./...`

- 2026-03-14: fixed real Kimi/Qwen ACP permission requests that carried structured `toolCall` previews instead of flat strings.
  - root cause: the direct ACP provider adapters decoded `session/request_permission.toolCall` as `map[string]string`, but real Kimi 1.22.0 sends rich payloads with `content` arrays and diff metadata, so JSON decode failed and ngent immediately returned a fail-closed reject without emitting `permission_required`.
  - updated Kimi and Qwen to use the shared parser, added regression coverage for real-style rich payloads, and normalized `agent_thought_chunk` as the same hidden-thought update family used by the rest of ngent's ACP parser.
  - updated the Web UI streaming bubble to show `Thinking...` immediately before the first visible delta so long Kimi reasoning phases no longer look like a dead blank bubble.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-14: fixed real OpenCode ACP permission requests so Web UI permission cards appear instead of silently dropping the tool call.
  - root cause: after the shared ACP CLI refactor, the OpenCode adapter never installed a `HandlePermissionRequest` hook, so any real `session/request_permission` call still hit the default `method not found` path. In addition, OpenCode can encode file-like permissions via `toolCall.locations[]` or `toolCall.rawInput.filepath` with generic titles such as `external_directory`.
  - extended the shared ACP permission parser to extract path-like previews from `locations[]` and `rawInput`, classify directory/path requests as `file`, and fall back to the resolved path when the provider title is too generic for the Web UI.
  - wired OpenCode into the same permission bridge as Kimi/Qwen and added regression coverage for real-style OpenCode `external_directory` payloads.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-14: converged direct ACP structured-permission bridging into one shared helper in `internal/agents/acpcli`.
  - Kimi, Qwen, and OpenCode previously carried identical provider-local `handlePermissionRequest` implementations even after sharing the same normalized permission-request parser.
  - moved that common bridge logic into `acpcli.StructuredPermissionRequestHandler(timeout)` and reduced each provider to a one-line hook binding plus its local timeout constant.
  - kept Gemini unchanged because it still uses a different provider-specific permission payload/response shape rather than the shared normalized `PermissionRequestPayload`.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-19: changed Web UI assistant rendering from one aggregated bubble into an event-ordered segment timeline.
  - root cause: the Web UI already persisted `reasoning_delta`, `tool_call`, and `tool_call_update`, but both live streaming and finalized history still rendered them as one concentrated reasoning/tool section plus one final assistant bubble, so the actual turn sequence was lost visually.
  - extended the frontend message model with ordered `segments`, rebuilt finalized assistant messages from persisted turn events (`message_delta`, `reasoning_delta`, `tool_call`, `tool_call_update`), and tracked the same segment timeline during SSE streaming.
  - tool-call updates now keep their original timeline position by merging later `tool_call_update` payloads into the first segment for that `toolCallId`, while message/reasoning deltas append new segments only when the stream actually switches modes.
  - kept plan cards as a separate block, but assistant content / thought / tool activity now render in the same chronological order the agent emitted them.
  - removed the IM-style left/right chat alignment for the transcript pane; user prompts now render as the top line of a single-column turn flow and agent output follows directly underneath in the same reading column.
  - during an in-flight turn, only the currently growing thought segment stays expanded; once a later answer/tool/plan event arrives, the completed thought segment immediately switches to the collapsed finalized-panel state instead of waiting for full turn completion.
  - completed answer segments now switch to finalized markdown rendering as soon as the stream moves on, so tables and other block markdown render immediately instead of staying as raw text until the whole turn finishes.
  - styled markdown tables in answer/thought blocks with borders, header background, zebra rows, and horizontal overflow so rendered tables are visually distinct instead of looking like loosely aligned text.
  - moved markdown tables onto a fit-content wrapper with independent horizontal scrolling, so the outer border now hugs the actual table content instead of stretching to the full transcript width.
  - changed tool-call segments from always-open cards into collapsible panels that follow the same interaction model as thought blocks: the currently updating tool call stays open during streaming, finalized tool calls default closed, and users can manually expand closed panels on demand.
  - kept permission-request cards outside that new fold/unfold treatment so approval prompts still remain independently visible and actionable while a turn is running.
  - delayed hidden tool-call detail `Show all` binding until the panel is actually opened, so nested command/JSON previews still compute their collapsed height correctly after the outer tool-call panel starts closed.
  - moved assistant copy actions from the whole-message footer down to each finalized answer segment, so clicking copy now copies only that visible answer block instead of concatenating every answer segment in the turn.
  - kept per-answer copy buttons under their own answer segments, but merged each segment's timestamp and copy control onto one small meta row so time appears first and copy follows on the same line.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-20: limited runtime agent handling to providers that actually pass startup preflight in the current environment.
  - root cause: ngent still used the full static provider list for `/v1/agents`, create-thread allowlist validation, and startup config-catalog refresh, so hosts that only installed a subset of agent CLIs emitted noisy `config_catalog.refresh_failed` warnings for missing binaries and exposed unusable agents in the frontend.
  - changed startup wiring to derive one active agent set from successful provider preflight checks and reuse that same set for frontend agent discovery, request validation, and config-catalog background refresh.
  - `GET /v1/agents` now omits unavailable providers instead of returning `status:"unavailable"`, and startup no longer attempts config/model refresh for agents whose binaries are absent in the running environment.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./...`

- 2026-03-21: deduplicated the built-in ACP provider `DiscoverModels` entrypoints.
  - `gemini`, `qwen`, `kimi`, and `opencode` all carried the same package-level wrapper that only called `New(cfg)` and then `client.DiscoverModels(ctx)`.
  - added `internal/agents/acpcli.DiscoverModelsWithClient` so that constructor-plus-delegate path lives in one shared helper.
  - updated `internal/agents/gemini/models.go`, `internal/agents/qwen/models.go`, `internal/agents/opencode/models.go`, and the ACP fallback in `internal/agents/kimi/models.go` to reuse that helper while preserving Kimi's local-config override behavior.
  - continued the same cleanup by moving the shared ACP `session/new`, `session/load`, `session/list`, and `sessionCWD` parameter builders into `internal/agents/acpcli`, then rewired `gemini`, `qwen`, `opencode`, and `kimi` to use those common helpers instead of carrying identical local functions.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-22: switched model/reasoning discovery from probe sessions to real session snapshots.
  - root cause: startup refresh plus `GET /v1/threads/{threadId}/config-options` / `GET /v1/agents/{agentId}/models` live fallbacks were still opening probe-only `session/new` calls, which created provider-owned empty sessions and could leak stale default config between unrelated threads.
  - removed startup config-catalog refresh from `cmd/ngent`, and changed the HTTP config/model read paths to return stored-only sqlite data instead of probing upstream runtimes.
  - added a shared config snapshot callback in `internal/agents`; user-initiated `session/new` / `session/load` responses from ACP stdio providers and embedded `codex` / `claude` now report their latest `configOptions` during `Stream()`, and session-history `session/load` can also feed the same persistence path.
  - the HTTP turn handler now persists those real snapshots immediately into sqlite:
    - `threads.agent_options_json` is updated with the session's actual current `modelId` / `configOverrides`
    - `agent_config_catalogs` is updated under the actual current model id for later reuse
  - switching a thread onto an existing session now clears stale thread-local model/reasoning selections first, so the next `session/load` becomes the source of truth for that session's current config.
  - the Web UI now hides model/reasoning controls until a real config snapshot exists, reloads config options after turns finish, and clears stale thread-local config cache on session switch.
  - updated HTTP tests to reflect the new lifecycle:
    - fresh threads expose empty `configOptions` until a real turn runs
    - stored config rows are only used when the thread already has a concrete `modelId`
    - agent model lists now come from stored sqlite catalogs only
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- 2026-03-22: persisted learned config snapshots per provider session so switching back to a known session restores model controls immediately.
  - root cause: after the earlier session-driven discovery change, real turns persisted config into thread state plus `agent_config_catalogs`, but a later session switch intentionally cleared thread-local `modelId` / `configOverrides`; switching back to that session then had no session-scoped snapshot to restore from.
  - added sqlite-backed `session_config_cache(agent_id, cwd, session_id, config_options_json, updated_at)` alongside transcript caching.
  - real turn config snapshots now write through both:
    - thread/agent-model state as before
    - session-scoped config cache keyed by `agent + cwd + sessionId`
  - when a fresh session reports config before `session_bound`, ngent now backfills the same snapshot into `session_config_cache` as soon as the session id is bound.
  - `GET /v1/threads/{threadId}/config-options` now restores from the current session cache when the thread points at a known `sessionId` but has no thread-local `modelId`.
  - user-triggered session switching now also learns config from `GET /v1/threads/{threadId}/session-history`:
    - if transcript cache exists but session config cache is still missing, ngent performs one live `session/load` instead of stopping at cached transcript data
    - if that `session/load` returns `configOptions`, ngent persists them into thread state (when the thread is currently bound to that session), `agent_config_catalogs`, and `session_config_cache`
    - the Web UI refreshes config controls again after session-history replay finishes, so model/reasoning buttons can appear immediately after switching sessions without needing a new turn
  - additionally verified the session-load-only path with a fresh Codex thread on a fresh sqlite database:
    - created the thread without sending any turn
    - switched directly to an existing Codex session
    - confirmed `Model` and `Reasoning` buttons appeared immediately from that session's `session/load`
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./... -count=1`

- 2026-03-23: added Web UI attachment upload support that flows through ACP `resource_link` prompt content.
  - introduced a shared structured prompt model in the agent layer so HTTP turns can carry text plus ACP `resource_link` items instead of being limited to one plain string.
  - `POST /v1/threads/{threadId}/turns` now accepts `multipart/form-data`; uploaded files are persisted into the local temp directory, converted into `file://` resource links, and preserved in history via a new `user_prompt` turn event.
  - ACP CLI and embedded providers now send structured `session/prompt` content arrays, while non-ACP/fallback streamers still receive a readable plain-text representation.
  - updated the Web UI composer to support attachment picking/removal, attachment-only sends, text+attachment sends, left-footer ordering `Attachment -> Model -> Reasoning`, and user-message attachment cards reconstructed from history.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./internal/httpapi ./internal/agents/...`
    - pass: `go test ./...`

- 2026-03-22: fixed the composer model/reasoning dropdowns so they remain clickable after session-history refresh updates them in place.
  - root cause: `bindThreadConfigSwitches()` can run more than once on the same composer DOM after a session switch; repeated `addEventListener('click', ...)` bindings caused one listener to open the menu and the next listener to close it immediately in the same click.
  - made config-switch binding idempotent per `.thread-model-switch` node, while still allowing later refresh passes to update labels, menu contents, and hidden/disabled state.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./... -count=1`

- 2026-03-22: fixed active-turn session switching when the current session had already persisted model/config state.
  - root cause: `PATCH /v1/threads/{threadId}` intentionally allows session-only selection changes during an active turn, but the session-only detector compared the current thread options against the target options after the target session change had already stripped `modelId` / `configOverrides`; once the active session had learned config, that comparison incorrectly treated a pure session switch as a broader thread update and returned `thread has an active turn`.
  - `isSessionOnlyAgentOptionsUpdate()` now ignores thread-local config snapshot fields only when checking whether the change is a pure session/fresh-session selection change, while still rejecting unrelated active-turn thread edits such as direct model changes.
  - added HTTP regression coverage for switching away from an active session after its config snapshot has already been persisted.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./... -count=1`
    - pass: real browser validation with Kimi on `go run ./cmd/ngent -db-path /tmp/ngent-session-switch-active.db -port 8690 -debug`:
    - started a new Kimi session
    - triggered a long streaming response
    - switched to an older session while the turn was still active
    - confirmed the UI switched without the previous `thread has an active turn` alert

- 2026-03-22: kept active streaming sessions visible in the session sidebar after switching away.
  - root cause: the Web UI only inserted the currently selected session as an ephemeral row when it was missing from `session/list`; a newly created session that was still streaming but not yet present in the fetched session list disappeared from the sidebar as soon as the user switched to another session.
  - `renderSessionPanel()` now also prepends any thread-local session ids that are still active in `streamStates`, so running sessions stay visible until the real session list catches up.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./... -count=1`

- 2026-03-22: preserved exact provider permission options end-to-end instead of collapsing every request into `Allow` / `Deny`.
  - root cause: the Web UI hard-coded two buttons and `POST /v1/permissions/{permissionId}` only carried a generic outcome, so multi-option ACP permission requests from providers such as Kimi could not round-trip `allow once` / `allow always` / `reject once` / `reject always` exactly.
  - extended the shared permission model to keep provider `options[]` and an exact `SelectedOptionID`, and taught the ACP permission bridge to prefer that exact option id when replying to option-aware providers.
  - `permission_required` SSE payloads now include advertised permission options, `POST /v1/permissions/{permissionId}` now accepts `optionId` alongside the existing `outcome`, and the HTTP permission bridge infers the generic fallback outcome from the selected option kind when possible.
  - updated the Web UI permission card to render all advertised options dynamically, fall back to `Allow` / `Deny` only when no options are provided, and show the selected option label in the resolved state.
  - added regression coverage for exact option pass-through at the HTTP layer and for Kimi's structured permission handler preserving non-default selected option ids.
  - updated API / spec / frontend docs and acceptance criteria to describe dynamic permission options.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./internal/agents/kimi -run 'TestHandlePermissionRequest(ParsesRichToolCallPayload|HonorsSelectedOptionID)' -count=1`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./internal/httpapi -run 'TestTurnPermission(RequiredSSEEvent|ApprovedContinuesAndCompletes|SelectedOptionFlowsThroughExactAgentChoice|TimeoutFailClosed|SSEDisconnectFailClosed)' -count=1`
    - pass: `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 /usr/local/go/bin/go test ./... -count=1`

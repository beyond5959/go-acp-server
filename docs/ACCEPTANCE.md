# ACCEPTANCE

This checklist defines executable acceptance checks for requirements 1-16.

## Requirement 1: HTTP/JSON plus SSE

- Operation: call JSON endpoint and one SSE turn endpoint.
- Expected: JSON response is `application/json`; turn endpoint is `text/event-stream`; turn streams can persist auxiliary event types such as `reasoning_delta` and `plan_update` alongside `message_delta`.
- Verification command:
  - `curl -sS -i http://127.0.0.1:8686/healthz`
  - `curl -sS -N -H 'X-Client-ID: demo' -H 'Content-Type: application/json' -d '{"input":"hello","stream":true}' http://127.0.0.1:8686/v1/threads/<threadId>/turns`
  - `go test ./internal/httpapi -run TestTurnsSSEAndHistory -count=1`
  - `go test ./internal/httpapi -run TestTurnsSSEIncludesReasoningAndPersistsHistory -count=1`

## Requirement 2: Multi-client and multi-thread support

- Operation: create threads under different `X-Client-ID` headers and verify isolation.
- Expected: no cross-client leakage.
- Verification command:
  - `go test ./internal/httpapi -run TestThreadAccessAcrossClientsReturnsNotFound -count=1`

## Requirement 3: Per-thread independent agent instance

- Operation: run turns on multiple threads concurrently.
- Expected: each thread resolves/uses its own thread-level agent path.
- Verification command:
  - `go test ./internal/httpapi -run TestMultiThreadParallelTurns -count=1`

## Requirement 4: One active turn per thread/session scope plus cancel

- Operation: start first turn, submit second turn on same session, verify conflict; then switch to another session on the same thread and verify that second session can run concurrently; finally cancel.
- Expected: same-session second turn gets `409 CONFLICT`; different session on the same thread is allowed; cancel converges quickly.
- Verification command:
  - `go test ./internal/httpapi -run 'TestTurnConflictSingleActiveTurnPerSession|TestTurnAllowsConcurrentSessionsOnSameThread|TestUpdateThreadClearingSessionDropsStaleUnboundProvider' -count=1`
  - `go test ./internal/httpapi -run TestTurnCancel -count=1`

## Requirement 5: Lazy startup

- Operation: create thread first, then start first turn.
- Expected: provider factory is not called at thread creation; called at first turn only.
- Verification command:
  - `go test ./internal/httpapi -run TestTurnAgentFactoryIsLazy -count=1`

## Requirement 6: Durable SQLite history and restart continuity

- Operation: run turn, recreate server instance with same DB, run next turn.
- Expected: next turn still injects prior history/summary and continues.
- Verification command:
  - `go test ./internal/httpapi -run TestRestartRecoveryWithInjectedContext -count=1`

## Requirement 7: Permission forwarding and fail-closed

- Operation: trigger permission-required flow; test approved, timeout, and disconnect cases.
- Expected: `permission_required` emitted; when the provider advertises permission `options[]`, the SSE payload preserves them and `/v1/permissions/{permissionId}` can submit an exact `optionId`; timeout/disconnect still fail closed.
  - embedded codex command-approval flow should not fail with adapter-side `-32601 method not found` when using updated app-server request methods.
- Verification command:
  - `go test ./internal/httpapi -run TestTurnPermissionRequiredSSEEvent -count=1`
  - `go test ./internal/httpapi -run TestTurnPermissionApprovedContinuesAndCompletes -count=1`
  - `go test ./internal/httpapi -run TestTurnPermissionSelectedOptionFlowsThroughExactAgentChoice -count=1`
  - `go test ./internal/httpapi -run TestTurnPermissionTimeoutFailClosed -count=1`
  - `go test ./internal/httpapi -run TestTurnPermissionSSEDisconnectFailClosed -count=1`

## Requirement 8: Localhost-by-default bind with public opt-in

- Operation: validate listen address policy with/without allow-public.
- Expected: only loopback bind is allowed by default; `--allow-public=true` allows non-loopback binds.
- Verification command:
  - `go test ./cmd/ngent -run TestResolveListenAddr -count=1`

## Requirement 9: Startup logging contract

- Operation: start server and inspect startup output on stderr.
- Expected: startup output is multi-line, human-readable, includes a QR code (when public bind is enabled), and prints the service port + a concrete URL under the QR code.
- Verification command:
  - `go test ./cmd/ngent -count=1`
  - manual run: `go run ./cmd/ngent`

## Requirement 10: Unified errors and readable access logs

- Operation: trigger auth failure/path policy failure and inspect request completion logs.
- Expected: `UNAUTHORIZED` and `FORBIDDEN` error envelopes are stable; request logs emit one readable line with local-time request timestamp, remote address, request line, HTTP status text, and elapsed duration.
- Verification command:
  - `go test ./internal/httpapi -run TestV1AuthToggle -count=1`
  - `go test ./internal/httpapi -run TestCreateThreadValidationCWDAllowedRoots -count=1`
  - `go test ./internal/httpapi -run TestRequestCompletionLogIncludesPathIPAndStatus -count=1`

## Requirement 11: Context window and compact

- Operation: run multiple turns, compact once, verify summary update and injection impact.
- Expected: summary/recent/current-input injection works; compact updates `threads.summary`; internal turns hidden by default.
- Verification command:
  - `go test ./internal/httpapi -run TestInjectedPromptIncludesSummaryAndRecent -count=1`
  - `go test ./internal/httpapi -run TestCompactUpdatesSummaryAndAffectsNextTurn -count=1`

## Requirement 12: Idle TTL reclaim and graceful shutdown

- Operation: configure short idle TTL; verify reclaim; simulate shutdown with active turn.
- Expected: idle thread agent is reclaimed and closed; shutdown force-cancels active turns on timeout.
- Verification command:
  - `go test ./internal/httpapi -run TestAgentIdleTTLReclaimsThreadAgent -count=1`
  - `go test ./cmd/ngent -run TestGracefulShutdownForceCancelsTurns -count=1`

## Requirement 13: Embedded Web UI

- Operation: start server; open browser at `http://127.0.0.1:8686/`.
- Expected: UI loads, threads can be created, turns stream in real time, ACP plan/reasoning updates render as live agent-side sections, live reasoning shows `Thinking`, finalized reasoning shows `Thought`, finalized reasoning uses a lightweight inline toggle, renders markdown, and collapses by default, permissions can be resolved, history is browsable, and the shell/composer/modals render with the refreshed premium workbench styling on both desktop and narrow/mobile widths; on desktop the session panel fully retracts without leaving a strip, and its collapse/expand affordance is revealed from the chat panel's left edge.
- Verification command:
  - `go test ./internal/webui -count=1` (checks `GET /` returns 200 with `text/html` content-type and SPA fallback)
  - `go test ./internal/httpapi -run TestTurnsSSEIncludesReasoningAndPersistsHistory -count=1`
  - `cd internal/webui/web && npm run build`
  - manual: `make run` → open `http://127.0.0.1:8686/` or scan the startup QR code from another device, confirm the refreshed glass-panel shell/sidebars/chat composer render cleanly, live `Thinking` stays expanded while streaming, finalized reasoning label changes to `Thought`, markdown inside expanded `Thought` renders correctly, the section collapses after the turn completes, the session panel fully retracts and reopens from the chat-left hover handle on desktop, and settings/new-agent overlays remain polished and usable

## Global Gate

- Operation: run repository checks.
- Expected: formatting and tests are green.
- Verification command:
  - `gofmt -w $(find . -name '*.go' -type f)`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`

## Requirement 14: OpenCode Agent

- Operation: verify opencode provider is listed and can complete a turn.
- Expected: `GET /v1/agents` includes `{"id":"opencode","name":"OpenCode","status":"available"}` when `opencode` is in PATH, and omits `opencode` entirely when the binary is unavailable; a full turn over SSE returns `message_delta` events.
- Verification commands:
  - `go test ./internal/agents/opencode -run TestStreamWithFakeProcess -count=1`
  - `E2E_OPENCODE=1 go test ./internal/agents/opencode -run TestOpenCodeE2ESmoke -v -timeout 60s`
- Latest observed validation (2026-03-13):
  - unit/fake-process path: pass
  - real host smoke: fail with `opencode: session/new: context deadline exceeded`; tracked as `KI-028`

## Requirement 15: Gemini CLI Agent

- Operation: verify Gemini CLI provider is listed and can complete a turn.
- Expected: `GET /v1/agents` includes `{"id":"gemini","name":"Gemini CLI","status":"available"}` when `gemini` is in PATH and `GEMINI_API_KEY` is set, and omits `gemini` entirely when startup preflight fails; a full turn over SSE returns `message_delta` events.
- Verification commands:
  - `go test ./internal/agents/gemini -run TestStreamWithFakeProcess -count=1`
  - `E2E_GEMINI=1 go test ./internal/agents/gemini -run TestGeminiE2ESmoke -v -timeout 60s`

## Requirement 16: Qwen Code Agent

- Operation: verify qwen provider is listed and can complete a turn over ACP.
- Expected:
  - `GET /v1/agents` includes `{"id":"qwen","name":"Qwen Code","status":"available"}` when `qwen` is in PATH, and omits `qwen` entirely when the binary is unavailable.
  - thread creation accepts `agent=qwen`.
  - turn streaming emits `message_delta` and finishes with `turn_completed` (or explicit upstream error envelope).
  - permission flow remains fail-closed.
- Verification commands (executed 2026-03-03):
  - `qwen --version` (pass, `0.11.0`)
  - `go test ./internal/agents/qwen -count=1` (pass)
  - `E2E_QWEN=1 go test ./internal/agents/qwen -run TestQwenE2ESmoke -v -timeout 120s` (pass, real prompt returns `PONG`)
  - `go test ./cmd/ngent ./internal/httpapi -count=1` (pass)
- Additional validation (executed 2026-03-13):
  - `E2E_QWEN=1 go test ./internal/agents/qwen -run TestQwenE2ESmoke -count=1 -v -timeout 180s` (pass)
  - `E2E_QWEN=1 go test ./internal/agents/qwen -run TestQwenE2ESessionTranscriptReplay -count=1 -v -timeout 240s` (pass)

## Requirement 16A: Kimi CLI Agent

- Operation: verify kimi provider is listed and can complete a turn over ACP.
- Expected:
  - `GET /v1/agents` includes `{"id":"kimi","name":"Kimi CLI","status":"available"}` when `kimi` is in PATH, and omits `kimi` entirely when the binary is unavailable.
  - thread creation accepts `agent=kimi`.
  - turn streaming emits `message_delta` and finishes with `turn_completed` (or explicit upstream error envelope).
  - provider tolerates current upstream ACP startup variants `kimi acp` and `kimi --acp`.
  - Kimi model/config discovery is sourced from ACP `session/new`, matching the other direct ACP CLI providers.
- Verification commands:
  - `go test ./internal/agents/kimi -count=1`
  - `E2E_KIMI=1 go test ./internal/agents/kimi -run TestKimiConfigOptionsE2E -v -timeout 120s`
  - `E2E_KIMI=1 go test ./internal/agents/kimi -run TestKimiE2ESmoke -v -timeout 120s`
  - `go test ./cmd/ngent ./internal/httpapi -count=1`
- Additional validation (executed 2026-03-13):
  - `E2E_KIMI=1 go test ./internal/agents/kimi -run TestKimiConfigOptionsE2E -count=1 -v -timeout 240s` (pass)
  - `E2E_KIMI=1 go test ./internal/agents/kimi -run TestKimiE2ESmoke -count=1 -v -timeout 180s` (pass)

## Requirement 16B: BLACKBOX AI Agent

- Operation: verify blackbox provider is listed and can complete a turn over ACP.
- Expected:
  - `GET /v1/agents` includes `{"id":"blackbox","name":"BLACKBOX AI","status":"available"}` when `blackbox` is in PATH, and omits `blackbox` entirely when the binary is unavailable.
  - thread creation accepts `agent=blackbox`.
  - turn streaming emits `message_delta` and finishes with `turn_completed` (or explicit upstream error envelope).
  - current upstream BLACKBOX ACP capability limits are reflected accurately: no `session/load` / session sidebar replay and no ACP model catalog until upstream exposes those surfaces.
- Verification commands:
  - `blackbox --version`
  - `go test ./internal/agents/blackbox -count=1`
  - `E2E_BLACKBOX=1 go test ./internal/agents/blackbox -run TestBlackboxE2ESmoke -v -timeout 120s`
  - `go test ./cmd/ngent ./internal/httpapi -count=1`
- Latest observed validation (2026-03-22):
  - local CLI probe: `blackbox --version` returned `1.2.47`
  - local ACP probe: `initialize` and `session/new` passed; `session/load` returned `-32601 method not found`
  - fake-process/unit path: pending this change set's validation run
  - real smoke: not run in the restricted sandbox environment used for this implementation pass

## Requirement 16C: Cursor CLI Agent

- Operation: verify Cursor provider is listed and can complete ACP handshakes/turn lifecycle through ngent.
- Expected:
  - `GET /v1/agents` includes `{"id":"cursor","name":"Cursor CLI","status":"available"}` when either `agent` or `cursor-agent` is in PATH, and omits `cursor` when neither binary is available.
  - thread creation accepts `agent=cursor`.
  - provider performs ACP `authenticate` with `methodId="cursor_login"` before `session/new` / `session/load`.
  - selected `agentOptions.modelId` is applied via ACP `session/set_config_option("model", ...)`.
  - turn streaming emits standard `message_delta` events and finishes with `turn_completed` (or explicit upstream error envelope).
- Verification commands:
  - `go test ./internal/agents/cursor -count=1`
  - `go test ./internal/agents/acpcli -run TestPickPermissionOptionIDNormalizesKinds -count=1`
  - `go test ./cmd/ngent -run TestSupportedAgentsOnlyIncludesAvailableAgents -count=1`
- Latest observed validation (2026-03-23):
  - official docs + local ACP probe: `initialize -> authenticate(cursor_login) -> session/new` confirmed against the installed Cursor CLI
  - local probe: `session/new.model` / `session/new.modelId` were ignored, while `session/set_config_option("model", ...)` updated the active model
  - fake-process/unit path: pass
  - full repository gate: pass (`cd internal/webui/web && npm run build`, `go test ./...`)
  - real prompt smoke: not recorded as a stable acceptance gate because the local Cursor account can return plan/quota gating text unrelated to ngent's transport integration

## Requirement 17: Thread Delete Lifecycle

- Operation: delete an existing thread from API/UI, verify ownership behavior, conflict behavior, and provider cleanup.
- Expected:
  - `DELETE /v1/threads/{threadId}` returns `200` with `status=deleted` for same-client thread.
  - deleting a thread with an active turn returns `409 CONFLICT`.
  - deleted thread is no longer visible in list/get/history endpoints.
  - cached thread agent provider is closed when the thread is deleted.
- Verification commands (executed 2026-03-03):
  - `go test ./internal/storage -run TestDeleteThread -count=1`
  - `go test ./internal/httpapi -run TestDeleteThread -count=1`
  - `cd internal/webui/web && npm run build`

## Requirement 18: Thread Model Selection and Switching

- Operation:
  - query agent model catalog via `GET /v1/agents/{agentId}/models`.
  - create thread with `agentOptions.modelId`.
  - update existing thread model via `PATCH /v1/threads/{threadId}`.
  - verify active-turn conflict and provider cache refresh behavior.
- Expected:
  - model catalog endpoint returns ACP-reported provider model options for each built-in agent.
  - thread create/list/get payload includes persisted `agentOptions.modelId`.
  - thread update persists model override and returns updated thread payload.
  - updating while turn is active returns `409 CONFLICT`.
  - successful update closes cached thread provider so next turn uses new model config.
- Verification commands (executed 2026-03-05):
  - `go test ./internal/httpapi -run TestV1AgentModels -count=1`
  - `go test ./internal/agents/acpmodel -count=1`
  - `go test ./internal/storage -run TestUpdateThreadAgentOptions -count=1`
  - `go test ./internal/httpapi -run TestUpdateThreadAgentOptions -count=1`
  - `go test ./internal/httpapi -run TestUpdateThreadConflictWhenActiveTurn -count=1`
  - `go test ./internal/httpapi -run TestUpdateThreadClosesCachedAgent -count=1`
  - `cd internal/webui/web && npm run build`

## Requirement 19: Thread Session Config Options (Model + Reasoning)

- Operation:
  - open/create a thread, query `GET /v1/threads/{threadId}/config-options`.
  - confirm response includes `configOptions` and model/reasoning-style options use ACP `currentValue` + `options`.
  - switch model through `POST /v1/threads/{threadId}/config-options` with `{configId:"model", value:"..."}`.
  - switch a non-model config option (for example reasoning) through the same endpoint.
  - verify persistence of `agentOptions.modelId` and `agentOptions.configOverrides` for subsequent turns/restarts.
  - verify Web UI composer footer shows both `Model` and `Reasoning`, applies on selection (no Apply button), and refreshes reasoning choices after model changes.
- Expected:
  - model selector data source is thread-level ACP `configOptions` (`category=model` / `id=model`).
  - reasoning selector data source is thread-level ACP `configOptions` (`category=reasoning`).
  - selected model/reasoning changes are persisted immediately into sqlite thread state without an extra Apply button.
  - if the cached session/provider is still using older model/reasoning selections, ngent applies the diff only on the next turn, before `session/prompt` is sent.
  - returned and persisted current values stay consistent:
    - `configOptions.model.currentValue` == thread `agentOptions.modelId`
    - non-model current values are mirrored into `thread.agentOptions.configOverrides`
  - normalized model/reasoning catalogs are persisted in sqlite and reused after service restart.
  - startup refresh of persisted catalogs happens asynchronously in the background and does not block frontend/API availability.
  - same-agent threads do not share selected current values, but can reuse the same stored catalog data for the same selected model.
  - title/model/config mutations are rejected with `409 CONFLICT` while any session on the thread is active.
  - session-only selection updates remain allowed when they switch to a different session.
  - clearing `sessionId` from the Web UI `New session` action invalidates any stale empty-session provider cache so the next turn does not fall back into an older ACP session.
  - if that clear happens after an explicit historical-session selection, the first turn of the fresh session is sent without `[Conversation Summary]` / `[Recent Turns]` injection, so the new ACP session transcript contains only the new exchange.
- Verification commands (executed 2026-03-06):
  - `go test ./internal/httpapi -run TestThreadConfigOptions -count=1`
  - `go test ./internal/httpapi -run TestThreadConfigOptionsPersistConfigOverrides -count=1`
  - `go test ./internal/httpapi -run TestV1AgentModelsUsesStoredCatalog -count=1`
  - `go test ./internal/agents/acpmodel -count=1`
  - `go test ./cmd/ngent -run TestExtractConfigOverrides -count=1`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`

## Requirement 20: Thread Drawer Actions and Rename

- Operation:
  - open sidebar thread actions from a thread-row drawer trigger.
  - rename a thread inline from the drawer.
  - delete a thread from the same drawer.
- Expected:
  - thread row exposes a drawer trigger instead of a direct delete icon button.
  - drawer lists `Rename` before `Delete`.
  - delete is text-only and styled as the dangerous action.
  - rename persists `thread.title` through `PATCH /v1/threads/{threadId}` and returns the updated thread payload.
  - rename/delete continue to respect active-turn safety (`409 CONFLICT` while the thread is running).
- Verification commands (executed 2026-03-06):
  - `go test ./internal/storage -run TestUpdateThreadTitle -count=1`
  - `go test ./internal/httpapi -run TestUpdateThreadTitle -count=1`
  - `cd internal/webui/web && npm run build`

## Current Acceptance Result (Integration Update, 2026-03-03)

- Scope: qwen provider implementation + server wiring + test coverage.
- Result:
  - implementation verification passed:
    - qwen provider unit/fake-process tests passed.
    - server/httpapi wiring tests passed (includes qwen allowlist coverage).
  - real qwen smoke in host environment: `Passed`.
  - Requirement 16 status: `Accepted`.

## Requirement 21: Embedded Codex Tool User-Input Request Compatibility

- Operation:
  - trigger codex app-server request path that emits `item/tool/requestUserInput` (for example MCP tool interaction requiring follow-up selection).
  - observe adapter response and downstream behavior.
- Expected:
  - adapter no longer returns JSON-RPC hard error `-32000 ... requestUserInput is not supported`.
  - request receives schema-compatible `answers` payload.
  - `item/tool/call` fallback returns structured response (`success=false`) instead of hard method error.
- Verification commands (executed 2026-03-06):
  - `go test ./...`
  - `cd internal/webui/web && npm run build`

## Requirement 22: ACP Debug Trace Logging

- Operation:
  - start server with `--debug=true`.
  - execute an ACP-backed request path.
  - inspect stderr logs.
- Expected:
  - logger runs at debug level.
  - stderr includes `acp.message` entries for outbound and inbound ACP JSON-RPC traffic.
  - entries include `component`, `direction`, `rpcType`, `method` when present, and sanitized `rpc` payload.
  - sensitive fields/tokens are redacted before logging.
- Verification commands (executed 2026-03-11):
  - `go test ./internal/observability ./internal/agents/acpstdio -count=1`
  - `go test ./cmd/ngent -count=1`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`

## Requirement 23: ACP Session Sidebar and Resume

- Operation:
  - create a thread/agent in the Web UI or API.
  - query `GET /v1/threads/{threadId}/sessions` and verify the first page of ACP sessions plus `nextCursor`.
  - request the next page through the returned cursor.
  - start a turn on a thread without `sessionId` and observe session binding.
  - start a follow-up turn on the now-bound thread.
- Expected:
  - the backend proxies ACP `session/list` through `GET /v1/threads/{threadId}/sessions`.
  - response includes `supported`, `sessions`, and `nextCursor`.
  - for providers that replay transcript over ACP `session/load`, the first `GET /v1/threads/{threadId}/session-history?sessionId=...` warms sqlite `session_transcript_cache`, and later requests can return the same replayed `user` / `assistant` messages without calling the provider again.
  - the Web UI renders a left-side collapsible session panel beside a permanently expanded agent rail.
  - when no agent/thread is selected yet, the session panel stays hidden and does not reserve layout width.
  - when collapsed, the session panel fully retracts and does not leave behind a visible strip.
  - on desktop, the session panel collapse/expand affordance is exposed from a hover-revealed control on the chat panel's left edge instead of from the panel header.
  - the expanded session panel shows the active thread title, provider badge, project path, and a `New session` entry above the session list.
  - the agent rail exposes the thread list plus a `New agent` button below it.
  - first-page session load happens when an active thread is selected and the session panel is expanded.
  - `Show more` pagination appears when `nextCursor` is present.
  - `New session` action that clears the selected `sessionId`.
  - repeated `New session` clicks while the thread is still unbound must still open a blank fresh-session view instead of reusing the prior anonymous buffer.
  - selecting an existing session requests provider-owned transcript replay before the next turn.
  - turn SSE emits `session_bound`, and the thread persists `agentOptions.sessionId`.
  - when an ACP agent emits `session/update` with `sessionUpdate="session_info_update"` and a non-null `title`, turn SSE emits `session_info_update` and the Web UI uses that title for the matching `sessionId` in the session sidebar.
  - once a thread is session-bound, subsequent prompt building no longer injects prior local turns into the provider prompt.
  - cancelled turns that never emitted `session_bound` and never produced visible response text do not reappear when the user opens a newer fresh session or reloads the thread.
- Verification commands (executed 2026-03-13):
  - `go test ./internal/httpapi -run 'TestThreadSessionsListEndpoint|TestTurnSessionBoundPersistsSessionIDAndSkipsContextInjection|TestNewSessionResetSkipsContextInjection' -count=1`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`
- Additional verification commands (executed 2026-03-12):
  - `go test ./internal/agents/kimi -run 'SessionTranscript' -count=1`
  - `go test ./internal/agents/opencode -run 'SessionTranscript' -count=1`
  - `go test ./internal/agents/codex -run 'Test(ConsumeCodexReplayUpdate|DrainCodexReplayUpdates)$' -count=1`
  - `go test ./internal/agents/qwen -run 'SessionTranscript' -count=1`
  - `E2E_QWEN=1 go test ./internal/agents/qwen -run 'TestQwenE2E(Smoke|SessionTranscriptReplay)$' -count=1 -v -timeout 180s`
  - real Qwen provider repro: confirm a locally created Qwen session reappears in `session/list` and `LoadSessionTranscript` replays the unique prompt marker through ACP `session/load`.
- Additional verification commands (executed 2026-03-13):
  - `go test ./internal/storage ./internal/httpapi -run 'Test(SessionTranscriptCacheCRUD|ThreadSessionHistoryEndpoint|ThreadSessionHistoryEndpointUsesSQLiteCacheAcrossRestart)$' -count=1`
  - `go test ./...`
- Additional verification commands (executed 2026-03-16 after fresh-session scope reset fix):
  - `cd internal/webui/web && npm run build`
  - `go test ./...`
  - `go run ./cmd/ngent --port 8798 --data-path /tmp/ngent-session-bug --debug`
  - reload the page, reopen the same thread, and confirm the empty cancelled placeholder still does not reappear

## Requirement 24: ACP Slash Commands Cache and Composer Picker

- Operation:
  - run a turn against an ACP-backed agent that emits `available_commands_update`.
  - query `GET /v1/threads/{threadId}/slash-commands`.
  - restart the server and query the same endpoint again.
  - open the Web UI composer for that thread and type `/` into an otherwise empty chat input.
- Expected:
  - built-in ACP providers normalize and accept `available_commands_update`.
  - the server persists the latest slash-command snapshot in SQLite and updates it every time a new snapshot arrives.
  - `GET /v1/threads/{threadId}/slash-commands` returns the cached commands for the thread's agent.
  - the cached slash commands survive server restart.
  - the Web UI shows a selectable slash-command list only when the current composer value starts with `/`.
  - when `GET /v1/threads/{threadId}/slash-commands` returns an empty list, typing `/` leaves the composer responsive and treats `/` as ordinary message input.
  - keyboard navigation and click selection insert the chosen slash command into the composer.
- Verification commands (executed 2026-03-13):
  - `go test ./internal/agents -run TestParseACPUpdateAvailableCommands -count=1`
  - `go test ./internal/storage -run TestAgentSlashCommandsCRUD -count=1`
  - `go test ./internal/httpapi -run 'TestThreadSlashCommandsPersistAndLoad|TestThreadSlashCommandsPersistAcrossRestart' -count=1`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`
- Additional verification commands (executed 2026-03-13):
  - `go run ./cmd/ngent --port 8787 --data-path /tmp/ngent-kimi-real-3 --debug`
- Additional verification commands (executed 2026-03-13 after Kimi timing fix):
  - `go test ./internal/agents/kimi -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|WithFakeProcess|WithFakeProcessModelID)$' -count=1`
  - `go run ./cmd/ngent --port 8788 --data-path /tmp/ngent-kimi-acp-trace --debug`
  - real local Kimi thread: confirmed `GET /v1/threads/{threadId}/slash-commands` returned the 8 persisted Kimi commands after the first turn
- Additional verification commands (executed 2026-03-13 after slash-entry refresh fix):
  - `cd internal/webui/web && npm run build`
  - `go test ./...`
  - `go run ./cmd/ngent --port 8789 --data-path /tmp/ngent-slash-refresh --debug`
- Additional verification commands (executed 2026-03-13 after codex embedded timing fix):
  - `go test ./internal/agents/codex -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|ReplaysCachedSlashCommandsAfterConfigOptionsInit)$' -count=1`
  - `go run ./cmd/ngent --port 8793 --data-path /tmp/ngent-codex-fix --debug`
  - real local codex thread: confirmed `GET /v1/threads/{threadId}/slash-commands` returned the 7-command codex snapshot after the first turn
  - sqlite check: `select agent_id, json_array_length(commands_json) from agent_slash_commands where agent_id = 'codex';` returned `codex|7`
- Additional verification commands (executed 2026-03-13 after Qwen/OpenCode stdio timing fix):
  - `go test ./internal/agents/qwen ./internal/agents/opencode -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|WithFakeProcess|WithFakeProcessModelID)?$' -count=1`
  - `go run ./cmd/ngent --port 8794 --data-path /tmp/ngent-qwen-opencode-fix --debug`
  - real local Qwen thread: confirmed `GET /v1/threads/{threadId}/slash-commands` returned `/bug`, `/compress`, `/init`, `/summary`
  - real local OpenCode thread: confirmed `GET /v1/threads/{threadId}/slash-commands` returned `/init`, `/review`, `/go-style-core`, `/remotion-best-practices`, `/find-skills`, `/compact`
- Additional verification commands (executed 2026-03-13 after stdio notification helper refactor):
  - `go test ./internal/agents/kimi ./internal/agents/qwen ./internal/agents/opencode -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|WithFakeProcess|WithFakeProcessModelID)?$' -count=1`
  - `go test ./...`
- Additional verification commands (executed 2026-03-13 after Gemini ACP notification fix):
  - `go test ./internal/agents/gemini -run 'TestStream(CapturesSlashCommandsEmittedBeforePrompt|WithFakeProcess|WithFakeProcessModelID)$' -count=1`
  - `go run ./cmd/ngent --port 8795 --data-path /tmp/ngent-gemini-fix --debug`
  - real local Gemini thread: confirmed `GET /v1/threads/{threadId}/slash-commands` still returned `[]`, indicating no provider `available_commands_update` was observed in that run
- Additional verification commands (executed 2026-03-13 after Codex config-init slash-command backfill):
  - `go test ./internal/agents/codex -run 'Test(StreamCapturesSlashCommandsEmittedBeforePrompt|StreamReplaysCachedSlashCommandsAfterConfigOptionsInit|SlashCommandsAfterConfigOptionsInit)$' -count=1`
  - `go test ./internal/httpapi -run 'Test(ThreadSlashCommandsPersistAndLoad|ThreadSlashCommandsPersistAcrossRestart|ThreadConfigOptionsBackfillsSlashCommandsWhenCatalogAlreadyStored)$' -count=1`
  - `go run ./cmd/ngent --port 8796 --data-path /tmp/ngent-codex-slash-fix --debug`
  - real local Codex thread: confirmed `GET /v1/threads/{threadId}/config-options` initialized the embedded provider and `GET /v1/threads/{threadId}/slash-commands` then returned the 7-command snapshot before any turn was sent
  - sqlite check: `select agent_id, commands_json from agent_slash_commands where agent_id = 'codex';` returned the persisted codex command list
- Additional verification commands (executed 2026-03-13 after Qwen slash-command probe fallback):
  - `go test ./internal/agents/qwen ./internal/httpapi -run 'Test(StreamCapturesSlashCommandsEmittedBeforePrompt|SlashCommandsAfterConfigOptionsInit|ThreadConfigOptionsBackfillsSlashCommandsWhenCatalogAlreadyStored|ThreadSlashCommandsEndpointBackfillsMissingSnapshot)$' -count=1`
  - real local Qwen thread: confirmed the very first `GET /v1/threads/{threadId}/slash-commands` returned `/bug`, `/compress`, `/init`, and `/summary` before any turn was sent
  - sqlite check: `select agent_id, commands_json from agent_slash_commands where agent_id = 'qwen';` returned the persisted qwen command list
- Additional verification commands (executed 2026-03-13 after unifying direct ACP provider slash-command caches):
  - `go test ./internal/agents/kimi ./internal/agents/opencode ./internal/agents/gemini ./internal/agents/qwen -run 'Test(StreamCapturesSlashCommandsEmittedBeforePrompt|SlashCommandsAfterConfigOptionsInit|WithFakeProcess|WithFakeProcessModelID)$' -count=1`
  - `go test ./...`
  - Kimi, OpenCode, Gemini, and Qwen now all keep the latest `available_commands_update` snapshot in the same provider-local cache across both `Stream()` and `ConfigOptions()` probes, so `/slash-commands` backfill uses one consistent source for these direct ACP agents

## Requirement 25: ACP Tool-Call Streaming and History

- Operation:
  - run a turn against an ACP-backed agent that emits `tool_call` followed by `tool_call_update` for the same `toolCallId`.
  - observe the SSE stream from `POST /v1/threads/{threadId}/turns`.
  - query `GET /v1/threads/{threadId}/history?includeEvents=true`.
  - open the same thread in the Web UI during streaming and again after reload/history fetch.
- Expected:
  - shared ACP parsing accepts `tool_call` and `tool_call_update` without flattening them into plain text or dropping their structured payload.
  - SSE emits `tool_call` / `tool_call_update` events with `turnId`, `toolCallId`, and the corresponding structured ACP fields (`status`, `content`, `locations`, `rawInput`, `rawOutput`) when present.
  - turn history persists those same event types and payloads.
  - the Web UI merges updates by `toolCallId`, so the same tool-call card progresses from its initial state to its updated/final state both live and after reload.
  - assistant content, thought blocks, and tool-call cards render in the order the events were emitted instead of being collapsed into one aggregated assistant bubble.
  - tool-call cards remain separate from assistant text segments even when the assistant later emits more visible content.
  - while the turn is still streaming, completed answer blocks already render through the markdown pipeline, so tables and similar block markdown no longer remain raw text until final completion.
  - while the turn is still streaming, only the currently active thought block stays expanded; once a later answer/tool/plan event arrives, the completed thought block collapses immediately without waiting for final completion.
  - while the turn is still streaming, only the currently active tool-call block stays expanded; once later answer/thought/plan activity takes over, the completed tool-call block collapses immediately but can still be reopened manually.
  - each finalized answer block exposes its own copy action, and using it copies only that block's answer text rather than the full assistant turn content.
  - each finalized answer block keeps its own timestamp and copy control on the same local row, with time first and copy second, rather than moving every copy control into one shared footer area.
  - finalized markdown tables render with visible tabular styling rather than unstyled browser-default text alignment.
  - finalized markdown table borders hug the table content width instead of leaving a large empty right gutter inside the border.
  - permission-request cards still render independently and remain visible/actionable instead of being hidden inside tool-call disclosure panels.
- Verification commands (executed 2026-03-16):
  - `go test ./internal/agents -run 'TestParseACPUpdateToolCall|TestParseACPUpdateToolCallUpdateKeepsExplicitClears' -count=1`
  - `go test ./internal/agents -run 'TestNewACPNotificationHandlerRoutesToolCallsToToolCallHandler' -count=1`
  - `go test ./internal/httpapi -run 'TestTurnsSSEIncludesToolCallUpdatesAndPersistsHistory' -count=1`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`

## Requirement 26: Session-Driven Model and Reasoning Discovery

- Operation:
  - create a fresh thread for a config-capable agent.
  - call `GET /v1/threads/{threadId}/config-options` before any turn.
  - run one real turn on that thread, or switch the thread to an existing session so a user-triggered `session/load` executes.
  - call `GET /v1/threads/{threadId}/config-options`, `GET /v1/threads/{threadId}`, and optionally `GET /v1/agents/{agentId}/models` after the turn.
  - switch the same thread onto a different existing session and observe the config UI before and after the next turn.
- Expected:
  - before any real session lifecycle call, `GET /v1/threads/{threadId}/config-options` returns an empty `configOptions` list instead of probing upstream.
  - ngent does not need startup refresh or read-only config/model endpoints to create provider sessions just to discover metadata.
  - once a real `session/new` or `session/load` returns `configOptions`, sqlite is updated immediately:
    - the thread row reflects the actual current `modelId` / `configOverrides`
    - the agent config catalog stores the snapshot under the actual current model id
    - the session-scoped snapshot is stored under the actual `sessionId` when one is known
  - after that real turn, `GET /v1/threads/{threadId}/config-options` returns the stored snapshot and `POST /v1/threads/{threadId}/config-options` can update selections without opening a probe session.
  - switching to an existing session clears stale thread-local model/reasoning selections until the next `session/load` reports the destination session's real config.
  - switching back to a previously learned session restores its stored model/reasoning snapshot immediately, without requiring another turn.
  - switching directly onto an existing session can also reveal model/reasoning controls immediately when that session's user-triggered `session/load` returns config metadata, even if the thread itself has not sent a new turn yet.
  - the Web UI hides model/reasoning controls before metadata exists and reveals them only after a real session snapshot has been learned.
  - `/v1/agents/{agentId}/models` returns only sqlite-backed learned models and may legitimately be empty on a brand-new agent.
- Verification commands (executed 2026-03-22):
  - `go test ./internal/httpapi -run 'Test(V1AgentModels|V1AgentModelsUsesStoredCatalog|V1AgentModelsEmptyWhenNoStoredCatalog|ThreadConfigOptionsGetAndSetModel|ThreadConfigOptionsGetUsesStoredCatalog|ThreadConfigOptionsPersistConfigOverrides|ThreadConfigOptionsRestoreFromSessionCacheAfterSessionSwitch|ThreadSessionHistoryEndpointPersistsConfigOptionsForSelectedSession|ThreadSessionHistoryEndpointReloadsLiveWhenTranscriptCachedButConfigMissing|ThreadConfigOptionsUnsupportedManager)$' -count=1`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`
  - real Codex Web UI validation:
    - run `go run ./cmd/ngent --data-path /tmp/ngent-session-load-config -port 8687 --debug`
    - without sending any turn, click an existing Codex session from the sidebar
    - confirm `Model` and `Reasoning` buttons appear immediately after the user-triggered session switch

## Requirement 27: ACP Assistant Image and Embedded Resource Content

- Operation:
  - run a turn against an ACP-backed agent that emits visible assistant text interleaved with non-text `agent_message_chunk` payloads such as image content and embedded resource content.
  - observe the SSE stream from `POST /v1/threads/{threadId}/turns`.
  - query `GET /v1/threads/{threadId}/history?includeEvents=true`.
  - open the same thread in the Web UI during streaming and again after reload/history fetch.
- Expected:
  - shared ACP parsing preserves non-text assistant `content` blocks instead of dropping them when they are not plain `{type:"text",text:...}` chunks.
  - SSE emits `message_content` events with `turnId` and the raw structured ACP `content` payload.
  - turn history persists those same `message_content` events unchanged.
  - ordinary visible assistant text still arrives as `message_delta`, and `responseText` remains the visible-text aggregate only.
  - the Web UI timeline keeps text segments and `message_content` segments in the original emission order.
  - image content renders as an inline preview card rather than raw JSON.
  - embedded resource content renders as a resource card with URI and/or text preview when the payload provides them, and unknown shapes still fall back to JSON.
  - the same structured assistant content remains visible after a full page reload/history reconstruction for hub-created turns.
- Verification commands (executed 2026-03-22):
  - `go test ./internal/agents -run 'TestParseACPUpdateAgentMessageChunkKeepsNonTextContent|TestNewACPNotificationHandlerRoutesStructuredMessageContent' -count=1`
  - `go test ./internal/httpapi -run 'TestTurnsSSEIncludesStructuredMessageContentAndPersistsHistory' -count=1`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`

## Requirement 28: Web UI Attachment Uploads Flow Through ACP Resource Links

- Operation:
  - open the Web UI composer on a thread for an ACP-backed agent.
  - attach one file or image from the new attachment button in the lower-left composer footer; optionally also enter text.
  - send the turn and observe the live SSE stream plus persisted turn history.
  - reload the page or refetch `GET /v1/threads/{threadId}/history?includeEvents=true`.
- Expected:
  - the composer footer order is `Attachment`, `Model`, `Reasoning` on the left and `Send` on the right.
  - the Web UI allows attachment-only sends as well as text+attachment sends, shows removable attachment chips/previews before send, and accepts clipboard file/image paste (`Cmd+V` on macOS) into the current composer.
  - `POST /v1/threads/{threadId}/turns` accepts `multipart/form-data` and persists uploaded files into the configured `data-path` under typed subdirectories such as `attachments/images/`, `attachments/documents/`, or `attachments/files/` before dispatching the turn.
  - ACP-backed agents receive `session/prompt.prompt[]` with ordinary text items plus `resource_link` items containing `uri`, `name`, `mimeType`, and `size`.
  - ngent persists a readable `requestText` summary plus a structured `user_prompt` history event carrying stable attachment ids so attachment cards can be reconstructed after reload.
  - the Web UI renders uploaded user attachments as cards in the transcript both immediately after send and after history reload, and persisted image attachments continue to preview through the backend attachment route instead of disappearing after the stream finishes.
- Verification commands (executed 2026-03-26):
  - `go test ./internal/httpapi -run 'Test(MultipartTurnUploadsAttachmentsAsResourceLinks|AttachmentEndpointSupportsQueryTokenAndClientOwnership|BuildInjectedPromptKeepsResourceLinksWhenInjectingContext)' -count=1`
  - `go test ./internal/agents/opencode -run 'TestStreamPromptSendsResourceLinks' -count=1`
  - `cd internal/webui/web && npm run build`
  - `go test ./...`

## Requirement 29: Web UI Renders Inline Base64 Image Placeholders In User Messages

- Operation:
  - create or replay a user message whose text contains one or more bracketed placeholders in the form `[Image: data:image/png;base64,...]`, optionally mixed with ordinary markdown/text before or after the placeholder.
  - open the thread in the Web UI and inspect the user message bubble.
- Expected:
  - each valid `data:image/*;base64,...` placeholder is rendered as an inline image preview inside the user bubble instead of raw base64 text.
  - surrounding user text still renders through the normal markdown path.
  - malformed or unsupported placeholder strings remain visible as literal text instead of being turned into broken image tags.
  - the message copy button still copies the original raw message text rather than a transformed HTML representation.
- Verification commands (executed 2026-03-26):
  - `cd internal/webui/web && npm run build`
  - `env GOCACHE=/tmp/ngent-gocache GOFLAGS=-p=1 go test ./...`

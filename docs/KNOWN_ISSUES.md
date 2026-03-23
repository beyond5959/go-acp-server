# KNOWN ISSUES

## Issue Template

```text
- ID: KI-XXX
- Title:
- Status: Open | Mitigated | Closed
- Severity: Low | Medium | High
- Affects:
- Symptom:
- Workaround:
- Follow-up plan:
```

## Open Issues

- ID: KI-034
- Title: Human-readable stderr logs are less machine-friendly than JSON logs
- Status: Open
- Severity: Low
- Affects: deployments that ingest ngent stderr into pipelines expecting one JSON object per line
- Symptom: request/access logs and ACP debug traces are emitted as readable text lines, so strict JSON log collectors cannot parse them directly
- Workaround: use a text log parser in the collector pipeline, or pin an earlier revision if JSON log envelopes are a hard requirement
- Follow-up plan: consider an opt-in `--log-format=json|pretty` switch if operator demand for machine-readable logs returns

- ID: KI-001
- Title: SSE disconnect during long-running turn
- Status: Open
- Severity: Medium
- Affects: streaming clients on unstable links
- Symptom: stream closes and client misses live tokens/events
- Workaround: reconnect with last seen event sequence and replay from history endpoint
- Follow-up plan: add heartbeat and explicit resume token contract in M4

- ID: KI-002
- Title: Permission decision timeout
- Status: Open
- Severity: Medium
- Affects: slow/offline client decision path
- Symptom: pending permission expires and turn is fail-closed (`outcome=declined`), typically ending with `stopReason=cancelled`
- Workaround: increase server-side permission timeout and respond quickly to `permission_required`
- Follow-up plan: expose timeout metadata in SSE payload and add client-side countdown UX

- ID: KI-003
- Title: SQLite lock contention under burst writes
- Status: Open
- Severity: Medium
- Affects: high-concurrency turn/event persistence
- Symptom: transient `database is locked` errors
- Workaround: enable WAL, busy timeout, and retry with jitter
- Follow-up plan: benchmark and tune connection settings in M2 and M8

- ID: KI-004
- Title: `cwd` validation false positives
- Status: Closed
- Severity: Low
- Affects: legacy deployments that used restrictive allow-root policies
- Symptom: historical issue where valid paths could be rejected as outside allowed roots
- Workaround: N/A after ADR-016 default absolute-cwd policy
- Follow-up plan: none

- ID: KI-005
- Title: External agent process crash
- Status: Open
- Severity: High
- Affects: ACP/Codex provider turns
- Symptom: turn aborts unexpectedly; stream ends with provider error
- Workaround: detect process exit quickly, persist failure event, allow user retry
- Follow-up plan: supervised restart and backoff policy in M6 and M8

- ID: KI-006
- Title: Permission request races with SSE disconnect
- Status: Open
- Severity: Medium
- Affects: clients that close stream while permission is pending
- Symptom: decision endpoint may return `404/409` after auto fail-closed resolution
- Workaround: reconnect and inspect turn history terminal state; treat stale `permissionId` as non-retriable
- Follow-up plan: add explicit `permission_resolved` event with reason (`timeout|disconnect|client_decision`)

- ID: KI-007
- Title: Embedded codex runtime prerequisite mismatch
- Status: Open
- Severity: Medium
- Affects: deployments enabling embedded codex provider
- Symptom: codex turns fail when `codex app-server` prerequisites/auth/environment are not ready even though server binary is correctly configured
- Workaround: verify codex CLI/app-server availability and auth state before issuing codex turns; inspect startup preflight and turn error logs
- Follow-up plan: add richer preflight diagnostics and compatibility matrix checks for codex CLI vs linked `acp-adapter` module versions

- ID: KI-008
- Title: Character-based context budgeting can diverge from token budgets
- Status: Open
- Severity: Medium
- Affects: long multilingual threads with high token/char variance
- Symptom: prompt fits `context-max-chars` but may still be too large for model token limits
- Workaround: reduce `--context-max-chars` conservatively and run compact more frequently
- Follow-up plan: replace char-based policy with model-aware token estimation in M8

- ID: KI-009
- Title: Embedded codex local state/schema drift warnings
- Status: Open
- Severity: Medium
- Affects: real local embedded codex runs that depend on user `~/.codex` state and app-server version capabilities
- Symptom: stderr may show warnings like `state_5.sqlite migration ... missing` and endpoint compatibility errors such as `mcpServer/call unknown variant`; turn usually still completes but tool output can be empty
- Workaround: align local codex CLI/app-server version with linked `acp-adapter` schema expectations, and repair/reset local codex state DB when migration drift appears
- Follow-up plan: add explicit diagnostics/preflight endpoint to surface local state/schema compatibility before turn execution

- ID: KI-010
- Title: Qwen ACP environment/auth dependency
- Status: Open
- Severity: Medium
- Affects: implemented `qwen --acp` provider turns in constrained environments
- Symptom:
  - in sandboxed or permission-restricted environments, Qwen can fail before ACP initialize completes when local runtime files under `~/.qwen` are not writable.
  - hub-side symptom in those environments typically converges as `qwen: initialize: qwen: connection closed`.
  - prompt execution can still fail with upstream/internal errors when auth or network is not ready, even after handshake succeeds.
- Workaround:
  - ensure writable home/config directory for qwen runtime (`HOME`, `~/.qwen`).
  - ensure qwen authentication is completed and network path to model backend is available before turn execution.
  - run real qwen validation outside restrictive sandboxes when verifying `session/list` / `session/load` behavior against the locally installed CLI.
- Follow-up plan:
  - add clearer preflight diagnostics for qwen runtime prerequisites (filesystem writable check + auth hints).
  - map common qwen upstream errors to stable hub error details for easier operator debugging.

- ID: KI-011
- Title: Thread deletion is irreversible
- Status: Open
- Severity: Low
- Affects: users deleting historical threads via API/Web UI
- Symptom: deleting a thread permanently removes its thread/turn/event history and cannot be restored through server APIs.
- Workaround: export needed history before delete.
- Follow-up plan: evaluate optional soft-delete retention window and admin-only restore endpoint if product requirements demand recoverability.

- ID: KI-012
- Title: Model override id is accepted as free text
- Status: Open
- Severity: Low
- Affects: thread create/update flows using `agentOptions.modelId`
- Symptom: direct API clients can still submit any `modelId`; unsupported values fail later during provider/runtime execution instead of being rejected at create/update time.
- Workaround: query `GET /v1/agents/{agentId}/models` first, then submit a returned model id (or omit `modelId` to use provider default model).
- Follow-up plan: add optional server-side validation in create/update path against runtime-discovered model catalogs.

- ID: KI-013
- Title: Stdio providers apply config in transient ACP sessions
- Status: Open
- Severity: Low
- Affects: `opencode` / `qwen` / `gemini` / `kimi` thread config behavior
- Symptom: these providers are process-per-turn; config changes are mirrored into persisted thread metadata (`agentOptions.modelId` + `agentOptions.configOverrides`) and reapplied on the next ACP session, but there is still no long-lived runtime session to mutate between turns.
- Workaround: none required for normal usage; persisted config selections remain effective on future turns through thread metadata replay.
- Follow-up plan: evaluate persistent per-thread ACP runtime for stdio agents if future product requirements need truly in-session config mutations beyond thread-level replay.

- ID: KI-014
- Title: Web UI surfaces only model and reasoning config controls
- Status: Open
- Severity: Low
- Affects: advanced ACP config categories beyond `model` and `reasoning`
- Symptom: thread `configOptions` can contain additional categories/ids, but the composer footer currently exposes first-class controls only for model and reasoning.
- Workaround: use `GET/POST /v1/threads/{threadId}/config-options` directly to inspect and update other ACP config options.
- Follow-up plan: add a generic advanced-config surface in the Web UI if users need broader access to ACP session settings.

- ID: KI-015
- Title: Partial startup refresh keeps stale catalog rows for failed models
- Status: Open
- Severity: Low
- Affects: persisted `agent_config_catalogs` when startup background refresh succeeds for some models but fails for others
- Symptom: on partial refresh failure, the server intentionally keeps older sqlite catalog rows for models that could not be refreshed, so removed/changed upstream model metadata can remain temporarily stale until a later successful refresh or an explicit config change rewrites that model row.
- Workaround: restart again after upstream/provider health is restored, or trigger a config change on the affected model/thread so the latest snapshot is written through immediately.
- Follow-up plan: evaluate adding per-agent refresh status/age diagnostics in the API or Web UI so operators can see when catalog data is partially stale.

- ID: KI-016
- Title: Thread rename is blocked during an active turn
- Status: Open
- Severity: Low
- Affects: API/Web UI rename requests using `PATCH /v1/threads/{threadId}` with `title`
- Symptom: rename requests return `409 CONFLICT` while the thread is actively streaming because title updates share the same active-turn lock as other thread mutations.
- Workaround: wait for the current turn to finish or cancel it, then retry rename.
- Follow-up plan: evaluate whether title-only updates should continue using the shared mutation lock or move to a narrower metadata-only guard in a future revision.

- ID: KI-019
- Title: `item/tool/requestUserInput` currently uses fallback auto-selection
- Status: Open
- Severity: Medium
- Affects: codex app-server flows that require real user-entered answers or multi-choice semantics beyond first-option selection
- Symptom: adapter now avoids hard `-32000` errors, but for `requestUserInput` it auto-selects first option labels and does not expose full interactive question UI in hub frontend.
- Workaround: prefer MCP/tool flows that do not require complex interactive follow-up prompts; if needed, run the same operation in an environment with native codex app UI support.
- Follow-up plan: add first-class user-input request bridge and frontend interaction model for arbitrary question/option responses.

- ID: KI-020
- Title: Kimi ACP runtime/auth prerequisites and command-form drift
- Status: Open
- Severity: Medium
- Affects: implemented `kimi` provider turns in environments with uninitialized Kimi CLI state
- Symptom:
  - Kimi upstream docs currently show both `kimi acp` and `kimi --acp`; the hub retries both, but a local CLI that exits before ACP initialize still surfaces as a provider startup failure.
  - prompt execution can fail after handshake when local Kimi authentication or upstream network access is not ready.
- Workaround:
  - ensure the local `kimi` CLI is installed and already logged in before issuing turns.
  - inspect startup preflight and turn error logs when ACP mode closes immediately.
- Follow-up plan:
  - add richer preflight diagnostics for Kimi auth/runtime readiness beyond PATH existence.
  - keep validating future Kimi CLI releases and narrow the fallback path once upstream command syntax stabilizes.

- ID: KI-021
- Title: Resumed ACP sessions do not backfill prior transcript into hub history
- Status: Open
- Severity: Medium
- Affects: threads that select an existing ACP `sessionId` from the Web UI/API
- Symptom: ngent now caches prior provider transcript snapshots in SQLite for `GET /v1/threads/{threadId}/session-history`, but that replay is still not imported into SQLite `turns/events`; history APIs remain source-of-truth only for hub-created turns.
- Workaround: use the session sidebar replay for provider-owned historical context, but rely on persisted hub `/history` for turns created through ngent itself.
- Follow-up plan: evaluate importing selected provider transcript into local persisted history, or exposing an explicit merged-history view, without duplicating future hub-originated turns.

- ID: KI-022
- Title: Codex session sidebar titles can still show provider wrapper text
- Status: Open
- Severity: Low
- Affects: Codex `session/list` entries rendered in the Web UI session sidebar
- Symptom: Codex provider metadata and replayed transcript can expose long wrapper-generated text such as `[Conversation Summary] ... [Current User Input] ...` or IDE context blocks because ngent now shows the raw provider-owned ACP replay.
- Workaround: use the thread title or the most recent visible turn content when the sidebar label or replayed prompt body is noisy.
- Follow-up plan: normalize Codex `session/list` display titles in the backend, likely by preferring the first replayable user prompt over raw provider preview text when available.

- ID: KI-023
- Title: Fresh Kimi ACP sessions may resume before they appear in Kimi session browsing surfaces
- Status: Open
- Severity: Medium
- Affects: newly created Kimi sessions bound through ngent ACP turns
- Symptom:
  - a just-created Kimi `sessionId` can be resumed successfully through ACP `session/load`, but may still be absent from Kimi's own `session/list`, `kimi export`, and local `~/.kimi/sessions/*/<sessionId>` files for a while.
  - ngent can continue the bound session on the same or another thread if the `sessionId` is already known, but the session sidebar may not show the new session immediately after creation.
- Workaround:
  - continue using the bound `sessionId` directly in ngent even if Kimi's own session browser has not caught up yet.
  - retry session browsing later after Kimi finishes persisting its own session index/files.
- Follow-up plan:
  - keep validating Kimi CLI session persistence timing across upstream releases.
  - decide whether ngent should annotate newly bound sessions as "not yet discoverable upstream" when local evidence supports that distinction.

- ID: KI-024
- Title: Kimi CLI 1.20.0 does not replay transcript messages during historical session/load
- Status: Open
- Severity: Medium
- Affects: Kimi historical session replay through `GET /v1/threads/{threadId}/session-history`
- Symptom:
  - Kimi `session/list` returns historical sessions and ACP `session/load` succeeds for those session ids.
  - Kimi CLI 1.20.0 currently emits no replay `session/update` notifications for those historical loads, so ngent returns `supported=true` with an empty transcript under the ACP-only implementation.
- Workaround:
  - continue the selected Kimi session normally; `session/load` still restores provider context for subsequent turns.
  - use ngent-local `/history` for turns created through ngent itself.
- Follow-up plan:
  - keep validating newer Kimi CLI releases and switch to transcript replay immediately if Kimi starts emitting standard `session/update` history during `session/load`.

- ID: KI-025
- Title: Session-history cache does not auto-refresh from provider metadata
- Status: Open
- Severity: Medium
- Affects: repeated `GET /v1/threads/{threadId}/session-history` requests for the same `(agent, cwd, sessionId)`
- Symptom:
  - after the first successful replay, ngent serves the cached SQLite snapshot on later requests and server restarts.
  - if the provider session later gains more messages outside that cached snapshot, ngent does not yet compare provider `updatedAt` metadata before returning the cached transcript.
- Workaround:
  - continue the conversation through the current ngent thread so new hub-local turns remain visible in `/history`.
  - if a full provider replay refresh is required immediately, clear the cached row from sqlite and request `/session-history` again.
- Follow-up plan:
  - persist `session/list.updatedAt` metadata and invalidate or refresh `session_transcript_cache` when that metadata advances.

- ID: KI-026
- Title: Thread-wide config changes still serialize across concurrent sessions
- Status: Open
- Severity: Low
- Affects: `PATCH /v1/threads/{threadId}` non-session updates and `POST /v1/threads/{threadId}/config-options`
- Symptom:
  - the server now allows concurrent turns across different sessions on the same thread.
  - shared thread metadata such as title, model, and config overrides still returns `409 CONFLICT` while any session on that thread is active.
- Workaround:
  - wait for active sessions to finish or cancel them before renaming the thread or changing shared model/config options.
- Follow-up plan:
  - evaluate whether some metadata-only updates can move to a narrower guard without letting shared provider state drift across sessions.

- ID: KI-027
- Title: Slash-command cache currently assumes one stable command set per agent
- Status: Open
- Severity: Low
- Affects: agents whose ACP `available_commands_update` payload may vary by workspace, session, or model
- Symptom:
  - ngent currently stores the latest slash-command snapshot in SQLite keyed only by `agent_id`.
  - if a provider later emits different slash-command sets for different contexts, the most recently observed snapshot for that agent will replace the earlier one and the Web UI composer may show commands from the wrong context.
- Workaround:
  - for Codex, simply opening the thread is now enough because `config-options` backfills the latest provider snapshot into sqlite before the first turn.
  - for Kimi, Qwen, OpenCode, and Gemini, the first `/slash-commands` request now backfills sqlite directly from the live provider snapshot cache when the provider has already emitted one, so typing `/` on a fresh thread is enough as long as the underlying CLI actually publishes `available_commands_update`.
  - for other agents, run a fresh turn in the target context before relying on slash-command suggestions, so the latest provider snapshot overwrites the cache.
- Follow-up plan:
  - keep provider-specific delivery timing fixes in place; codex caches the initial `session/new` / `session/load` snapshot before the first prompt, and Kimi/Qwen/OpenCode/Gemini now share both the same early ACP notification handling and the same provider-local slash-command cache across turn/config-session flows, so the remaining risk here is cache-key scope or provider emission behavior, not notification loss inside ngent.
  - revisit the cache key and move to `(agent_id, cwd)` or another provider-specific scope if a real agent starts varying slash commands by context.

- ID: KI-028
- Title: Real OpenCode ACP `session/new` can stall until hub timeout
- Status: Open
- Severity: Medium
- Affects: real `opencode` ACP turns and the host smoke test path
- Symptom:
  - on the current host validation run dated 2026-03-13, `E2E_OPENCODE=1 go test ./internal/agents/opencode -run TestOpenCodeE2ESmoke -count=1 -v -timeout 180s` failed with `opencode: session/new: context deadline exceeded`.
  - the process starts and initializes, but the upstream CLI does not complete `session/new` before the 45-second test context expires.
- Workaround:
  - verify local OpenCode auth/session readiness and backend reachability outside ngent, then retry.
  - if turns are business-critical, prefer a provider whose local CLI is already known-good on the host until OpenCode readiness is restored.
- Follow-up plan:
  - add richer diagnostics around stalled OpenCode `session/new` calls so auth/backend/readiness failures are distinguishable from protocol regressions.
  - keep rerunning the real smoke after local OpenCode environment fixes to confirm the shared ACP driver is not the blocking factor.

- ID: KI-029
- Title: Denied ACP permission turns currently collapse into an empty agent bubble
- Status: Open
- Severity: Low
- Affects: Web UI turns where the user denies a provider permission request
- Symptom:
  - the Web UI now correctly renders the pending `Permission Required` card for direct ACP providers such as Kimi and OpenCode, but once the user clicks `Deny`, the subsequent completed turn still has empty `responseText`.
  - after the final re-render, the ephemeral permission card disappears and the chat shows the existing empty-agent fallback (`…`) instead of a clearer provider rejection message.
- Workaround:
  - use the permission card itself as the source of truth for what was denied; the underlying tool action remains fail-closed and is not executed.
  - inspect `/history?includeEvents=1` if you need to confirm that the turn did emit `permission_required` before completion.
- Follow-up plan:
  - decide whether denied-permission turns should persist a lightweight terminal message, or whether the Web UI should keep the resolved permission card visible after turn completion.

- ID: KI-030
- Title: Provider-owned historical session replay still omits hidden reasoning, tool timeline, and rich content blocks
- Status: Open
- Severity: Low
- Affects: `GET /v1/threads/{threadId}/session-history` and Web UI session-sidebar replay for pre-existing provider sessions
- Symptom:
  - ngent now surfaces hidden reasoning and ordered tool/content/thought segments for hub-created turns by persisting turn events in normal history.
  - provider-owned historical replay returned by `/session-history` still exposes only visible `user` / `assistant` transcript messages, so switching to an older external session in the Web UI does not reconstruct past hidden reasoning blocks, the tool-call timeline, or non-text assistant content such as images/embedded resources.
- Workaround:
  - use regular ngent turn history for turns created through ngent itself; those now preserve reasoning and the ordered assistant segment timeline after reload.
  - treat provider-owned session replay as visible transcript-only until the replay contract is extended.
- Follow-up plan:
  - evaluate whether session-transcript schema should grow optional reasoning/tool metadata, or whether provider-owned replay should intentionally remain visible-transcript-only for privacy/product reasons.

- ID: KI-031
- Title: Web UI thinking expand state is not persisted across page reload
- Status: Open
- Severity: Low
- Affects: finalized agent messages that include reasoning/thinking content
- Symptom: users can expand a collapsed `Thinking` panel during the current page session, but a full page reload or fresh history load resets it to the default collapsed presentation.
- Workaround: expand the needed `Thinking` panel again after reload.
- Follow-up plan: evaluate persisting per-message UI presentation preferences in browser-local state if users need sticky behavior across reloads.

- ID: KI-032
- Title: Missing target-model catalog can temporarily leave stale reasoning choices after a picker change
- Status: Open
- Severity: Low
- Affects: threads that switch to a model whose sqlite config catalog has not been refreshed yet
- Symptom:
  - `POST /v1/threads/{threadId}/config-options` now persists the selected model immediately without mutating the live provider.
  - if sqlite does not yet have a catalog snapshot for the newly selected model, the immediate response can only fall back to the current in-memory option set, so the Web UI may temporarily show the previous reasoning list until the next turn or a later catalog refresh fills in the new model snapshot.
- Workaround:
  - send the next turn, or wait for background catalog refresh / a later config fetch to repopulate the target model's reasoning choices.
- Follow-up plan:
  - add an explicit background fetch path for missing target-model catalogs so the picker can self-heal without waiting for the next turn.

- ID: KI-034
- Title: Some ACP tool-call payload shapes render as generic JSON
- Status: Open
- Severity: Low
- Affects: Web UI display of `tool_call` / `tool_call_update` events carrying non-text or provider-specific content blocks
- Symptom:
  - common text, diff, command, and path/location payloads render as structured cards.
  - richer ACP payloads such as media/resource-specific blocks currently fall back to raw JSON sections in the same tool-call card.
- Workaround:
  - inspect the JSON block shown in the tool-call card; the full structured payload is still preserved in SSE/history.
- Follow-up plan:
  - add richer renderers for additional ACP content block variants once real provider payloads stabilize.

- ID: KI-035
- Title: Full-suite `go test ./...` can flake when ACP-heavy agent packages run in parallel
- Status: Open
- Severity: Low
- Affects: local validation runs that execute the whole Go test suite with default package parallelism
- Symptom:
  - on some hosts, `go test ./...` can intermittently hit startup timeouts in ACP-heavy fake-process packages such as `internal/agents/codex`, `internal/agents/opencode`, or `internal/agents/qwen`.
  - the same packages pass when re-run individually, which points to host resource contention rather than a deterministic functional regression in those packages.
- Workaround:
  - rerun the full suite with serialized package scheduling, for example `GOFLAGS=-p=1 go test ./...`.
  - if one package still fails, rerun that package directly to distinguish a transient host-timeout from a real regression.
- Follow-up plan:
  - reduce test startup latency and remove package-parallel sensitivity in the ACP-heavy fake-process suites so default `go test ./...` stays stable without extra flags.

- ID: KI-036
- Title: Fresh agents do not expose model/reasoning metadata until one real session reports it
- Status: Open
- Severity: Low
- Affects: brand-new threads or fresh installs before any real turn / resumed-session turn has returned `configOptions`
- Symptom:
  - `GET /v1/agents/{agentId}/models` can return an empty list for an agent that has never reported config metadata into sqlite.
  - `GET /v1/threads/{threadId}/config-options` returns an empty `configOptions` list for a fresh thread until a real `session/new` or `session/load` runs during a turn.
  - the Web UI therefore hides model/reasoning controls until after that first real session snapshot arrives.
  - once a specific session has already been learned, switching back to that same session now restores its cached model/reasoning snapshot immediately.
  - switching directly onto an unseen existing session can also reveal controls immediately if that user-triggered `session/load` returns config metadata.
  - only sessions whose real `session/new` / `session/load` have never yielded config metadata remain empty.
- Workaround:
  - send one real turn on the thread, or switch to an existing session and let ngent load it once so the session's config snapshot can be learned.
- Follow-up plan:
  - if upstream CLIs eventually expose model/config catalogs without creating sessions, consider adopting those non-session surfaces so ngent can prefill metadata without reintroducing empty probe sessions.

- ID: KI-037
- Title: BLACKBOX ACP currently lacks session resume and catalog discovery surfaces
- Status: Open
- Severity: Medium
- Affects: `blackbox` threads, `/session-history`, session sidebar browsing, and model picker/catalog endpoints for BLACKBOX
- Symptom:
  - local probing on 2026-03-22 against `blackbox 1.2.47` showed `initialize` advertising `agentCapabilities.loadSession=false`.
  - real `session/load` currently returns `-32601 method not found`.
  - `session/new` currently returns only `sessionId`, without `models.availableModels` or `configOptions`.
  - BLACKBOX can also emit stdout noise unrelated to ACP frames; ngent now tolerates that noise during transport, but the missing ACP resume/catalog surfaces still limit UX.
- Workaround:
  - use BLACKBOX through normal ngent thread turns; ngent-local `/history` still preserves turns created through ngent itself.
  - if a specific model must be forced, set `agentOptions.modelId` directly via API/advanced thread options even though no picker/catalog is currently available.
- Follow-up plan:
  - keep validating newer BLACKBOX CLI releases for `session/list` / `session/load` / model-catalog support.
  - wire those surfaces into ngent immediately once upstream ACP exposes them consistently.

- ID: KI-038
- Title: Cursor ACP runtime depends on pre-authenticated local CLI state
- Status: Open
- Severity: Medium
- Affects: implemented `cursor` provider turns in environments without a ready local Cursor login
- Symptom:
  - Cursor ACP `initialize` advertises `authMethods=[cursor_login]`.
  - if local Cursor login state is missing or unusable, the provider can fail during the explicit ACP `authenticate` step before `session/new`.
  - even after ACP authentication succeeds, prompt execution still depends on the local account's current entitlement/quota state.
- Workaround:
  - run `agent login` before issuing Cursor turns, or provide supported Cursor CLI auth flags/environment when launching the CLI outside ngent.
  - if turns authenticate but return product-gating text rather than the requested answer, verify the local Cursor account plan/quota state separately from ngent.
- Follow-up plan:
  - add richer preflight diagnostics for Cursor auth readiness beyond PATH existence if this becomes a recurring support issue.
  - keep validating whether future Cursor CLI releases expose a more explicit non-interactive auth-health probe.

- ID: KI-039
- Title: Uploaded Web UI resource-link temp files currently rely on OS temp cleanup
- Status: Open
- Severity: Low
- Affects: long-running ngent instances that handle many uploaded Web UI attachments
- Symptom:
  - `POST /v1/threads/{threadId}/turns` now persists uploaded files into the local temp directory so ACP providers can read them through `file://` resource links.
  - ngent currently leaves those temp files in place after the turn finishes and relies on normal OS temp cleanup / user cleanup instead of running its own retention sweeper.
- Workaround:
  - periodically clear old `ngent-*` files from the system temp directory if disk usage matters in a long-lived environment.
- Follow-up plan:
  - add an age-based temp-upload janitor once real usage clarifies safe retention expectations for provider retries and history-driven debugging.

## Recently Closed

- ID: KI-033
- Title: Repeated `New session` reused stale anonymous Web UI scope after fast cancel
- Status: Closed
- Severity: Low
- Affects: Web UI fresh-session flows cancelled before ACP emits `session_bound`
- Symptom:
  - a fast `send -> cancel -> New session` sequence could leave the chat pane showing the just-cancelled content instead of returning to an empty composer because the UI kept reusing the same empty-session scope.
  - reopening the thread after reload could also replay that empty cancelled placeholder from local history hydration.
- Workaround:
  - none; fixed on 2026-03-16.
- Follow-up plan:
  - monitor whether explicit fresh-session scope state ever needs backend persistence beyond the current Web UI behavior.

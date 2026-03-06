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
- Affects: `opencode` / `qwen` / `gemini` thread config behavior
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

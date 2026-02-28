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
- Follow-up plan: add richer preflight diagnostics and compatibility matrix checks for codex CLI vs linked `codex-acp` module versions

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
- Workaround: align local codex CLI/app-server version with linked `codex-acp` schema expectations, and repair/reset local codex state DB when migration drift appears
- Follow-up plan: add explicit diagnostics/preflight endpoint to surface local state/schema compatibility before turn execution

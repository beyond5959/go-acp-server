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
- Status: Open
- Severity: Low
- Affects: symlink-heavy workspace layouts
- Symptom: valid paths rejected as outside allowed roots
- Workaround: resolve and compare canonical paths using evaluated symlinks
- Follow-up plan: add canonicalization tests in M3

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
- Title: Codex binary path misconfiguration
- Status: Open
- Severity: Medium
- Affects: deployments enabling codex runtime provider
- Symptom: codex turn creation fails at runtime with provider resolution/start errors
- Workaround: set absolute `--codex-acp-go-bin` path and verify executable permissions before startup
- Follow-up plan: add proactive startup preflight check for configured codex binary

- ID: KI-008
- Title: Character-based context budgeting can diverge from token budgets
- Status: Open
- Severity: Medium
- Affects: long multilingual threads with high token/char variance
- Symptom: prompt fits `context-max-chars` but may still be too large for model token limits
- Workaround: reduce `--context-max-chars` conservatively and run compact more frequently
- Follow-up plan: replace char-based policy with model-aware token estimation in M8

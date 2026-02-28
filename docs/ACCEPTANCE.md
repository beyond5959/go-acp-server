# ACCEPTANCE

This checklist defines executable acceptance checks for requirements 1-11.

## Requirement 1: HTTP/JSON plus SSE

- Operation: call JSON endpoint and one SSE turn endpoint.
- Expected: JSON response is `application/json`; turn endpoint is `text/event-stream`.
- Verification command:
  - `curl -sS -i http://127.0.0.1:8686/healthz`
  - `curl -sS -N -H 'X-Client-ID: demo' -H 'Content-Type: application/json' -d '{"input":"hello","stream":true}' http://127.0.0.1:8686/v1/threads/<threadId>/turns`
  - `go test ./internal/httpapi -run TestTurnsSSEAndHistory -count=1`

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

## Requirement 4: One active turn per thread plus cancel

- Operation: start first turn, submit second turn on same thread, then cancel.
- Expected: second turn gets `409 CONFLICT`; cancel converges quickly.
- Verification command:
  - `go test ./internal/httpapi -run TestTurnConflictSingleActiveTurnPerThread -count=1`
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
- Expected: `permission_required` emitted; timeout/disconnect fails closed.
- Verification command:
  - `go test ./internal/httpapi -run TestTurnPermissionRequiredSSEEvent -count=1`
  - `go test ./internal/httpapi -run TestTurnPermissionApprovedContinuesAndCompletes -count=1`
  - `go test ./internal/httpapi -run TestTurnPermissionTimeoutFailClosed -count=1`
  - `go test ./internal/httpapi -run TestTurnPermissionSSEDisconnectFailClosed -count=1`

## Requirement 8: Localhost default and explicit public opt-in

- Operation: validate listen address policy with/without allow-public.
- Expected: non-loopback bind denied unless `--allow-public=true`.
- Verification command:
  - `go test ./cmd/agent-hub-server -run TestValidateListenAddr -count=1`

## Requirement 9: Startup logging contract

- Operation: start server and inspect startup output on stderr.
- Expected: startup summary is multi-line, human-readable, and includes `Time`, `HTTP`, `DB`, `Agents`, and `Help`.
- Verification command:
  - `go test ./cmd/agent-hub-server -count=1`
  - manual run: `go run ./cmd/agent-hub-server --listen 127.0.0.1:8686`

## Requirement 10: Unified errors and structured logs

- Operation: trigger auth failure/path policy failure and inspect request completion logs.
- Expected: `UNAUTHORIZED` and `FORBIDDEN` error envelopes are stable; request logs include `requestTime`, `path`, `ip`, and `statusCode`.
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
  - `go test ./cmd/agent-hub-server -run TestGracefulShutdownForceCancelsTurns -count=1`

## Global Gate

- Operation: run repository checks.
- Expected: formatting and tests are green.
- Verification command:
  - `gofmt -w $(find . -name '*.go' -type f)`
  - `go test ./...`

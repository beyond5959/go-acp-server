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

## Requirement 8: Public-by-default bind with local-only opt-out

- Operation: validate listen address policy with/without allow-public.
- Expected: non-loopback bind is allowed by default; `--allow-public=false` restricts to loopback only.
- Verification command:
  - `go test ./cmd/agent-hub-server -run TestValidateListenAddr -count=1`

## Requirement 9: Startup logging contract

- Operation: start server and inspect startup output on stderr.
- Expected: startup output is multi-line, human-readable, includes a QR code (when public bind is enabled), and prints the service port + a concrete URL under the QR code.
- Verification command:
  - `go test ./cmd/agent-hub-server -count=1`
  - manual run: `go run ./cmd/agent-hub-server`

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

## Requirement 13: Embedded Web UI

- Operation: start server; open browser at `http://127.0.0.1:8686/`.
- Expected: UI loads, threads can be created, turns stream in real time, permissions can be resolved, history is browsable.
- Verification command:
  - `go test ./internal/webui -count=1` (checks `GET /` returns 200 with `text/html` content-type and SPA fallback)
  - manual: `make run` → open `http://127.0.0.1:8686/` or scan the startup QR code from another device

## Global Gate

- Operation: run repository checks.
- Expected: formatting and tests are green.
- Verification command:
  - `gofmt -w $(find . -name '*.go' -type f)`
  - `go test ./...`

## Requirement 14: OpenCode Agent

- Operation: verify opencode provider is listed and can complete a turn.
- Expected: `GET /v1/agents` includes `{"id":"opencode","name":"OpenCode","status":"available"}` when `opencode` is in PATH; a full turn over SSE returns `message_delta` events.
- Verification commands:
  - `go test ./internal/agents/opencode -run TestStreamWithFakeProcess -count=1`
  - `E2E_OPENCODE=1 go test ./internal/agents/opencode -run TestOpenCodeE2ESmoke -v -timeout 60s`

## Requirement 15: Gemini CLI Agent

- Operation: verify Gemini CLI provider is listed and can complete a turn.
- Expected: `GET /v1/agents` includes `{"id":"gemini","name":"Gemini CLI","status":"available"}` when `gemini` is in PATH and `GEMINI_API_KEY` is set; a full turn over SSE returns `message_delta` events.
- Verification commands:
  - `go test ./internal/agents/gemini -run TestStreamWithFakeProcess -count=1`
  - `E2E_GEMINI=1 go test ./internal/agents/gemini -run TestGeminiE2ESmoke -v -timeout 60s`

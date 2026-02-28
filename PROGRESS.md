# Progress

## Project Overview

Code Agent Hub Server is a Go service that exposes HTTP/JSON APIs and SSE streaming for multi-client, multi-thread agent turns.
The system targets ACP-compatible agent providers, lazily starts per-thread agents, persists interaction history in SQLite, and bridges runtime permission requests back to clients.
Current built-in provider is `codex`; additional ACP-compatible providers are planned.
This file is the source of milestone progress, validation commands, and next actions.

## Current Milestone

- `Post-M8` ACP multi-agent readiness and maintenance.

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
  - finalized canonical Go module path as `github.com/beyond5959/go-acp-server`.
  - replaced placeholder import path references across in-repo Go sources/tests.
- `Post-M8` embedded codex migration completed:
  - switched codex provider from external `codex-acp-go` path-based process spawning to embedded `github.com/beyond5959/codex-acp/pkg/codexacp`.
  - removed user-facing codex binary path flags; codex runtime is now linked into server and lazily created per thread on first turn.
  - kept HTTP API semantics unchanged (`threads/turns/sse/permissions/history`) and preserved permission fail-closed round-trip.
  - updated `/v1/agents` codex status contract to runtime preflight-based `available|unavailable`.
  - updated codex smoke test gate to `E2E_CODEX=1` without `CODEX_ACP_GO_BIN` path dependency.
- `Post-M8` real local codex regression completed:
  - fixed embedded runtime lifecycle bug in `internal/agents/codex/embedded.go` (`runtime.Start(context.Background())` instead of timeout-bound context) and kept retry-on-turn-start-failure guard.
  - updated context composer so first turn with empty summary/history passes raw input, preserving slash-command semantics for embedded codex-acp flows.
  - executed real HTTP/SSE regression with required prompts plus same-thread conflict (`409`), cancel convergence, and permission round-trip (`approved` + `declined`) using `/mcp call` on fresh threads.
- `Post-M8` docs refresh completed:
  - added root `README.md` in English with project goal, `go install` instructions, and startup examples (local/public/auth) using default DB home `$HOME/.go-agent-server`.
  - removed manual `mkdir` steps from README startup examples and documented that server auto-creates `$HOME/.go-agent-server` for default db path.
- `Post-M8` db-path default improvement completed:
  - changed default `--db-path` from relative `./agent-hub.db` to `$HOME/.go-agent-server/agent-hub.db`.
  - added startup auto-create for db parent directory so users can run without explicitly passing `--db-path`.
  - added unit tests for default path resolution and db parent directory creation.
- `Post-M8` cwd policy simplification completed:
  - removed runtime CLI parameter `--allowed-root`.
  - server now allows user-specified absolute `cwd` paths by default.
  - updated docs and tests to reflect absolute-cwd policy.
- `Post-M8` docs framing update completed:
  - adjusted README/SPEC/API/ARCHITECTURE wording to emphasize ACP-compatible multi-agent goal.
  - kept current-state note explicit: today only `codex` is built-in.
  - simplified README startup path to `agent-hub-server` with explicit `agent-hub-server --help` guidance.

### In Progress

- None.

### Next

- Optional enhancement 1: add embedded-runtime preflight diagnostics endpoint (auth, app-server reachability, version compatibility).
- Optional enhancement 2: WebSocket streaming transport in addition to SSE.
- Optional enhancement 3: History/event pagination and cursor-based replay.
- Optional enhancement 4: RBAC and finer-grained authorization policies.
- Optional enhancement 5: Expanded audit logs and retention tooling.
- Optional enhancement 6: expose environment diagnostics for codex local state DB/schema mismatches (for example `~/.codex/state_5.sqlite` migration drift) and app-server method compatibility.

## Verification Commands

- `gofmt -w .`
- `go test ./...`
- `make fmt`
- `make test`
- `make run`

## Latest Verification

- Date: `2026-02-28`
- Commands executed:
  - `gofmt -w $(find . -name '*.go' -type f)`
  - `go test ./...`
  - `/tmp/server_real_regression.sh`
- Result:
  - formatting: pass
  - tests: pass
  - real local regression: pass (required prompts/SSE/context/conflict/cancel/permission round-trip)

## Dependency Fetch Notes

- Date: `2026-02-28`
- Failure 1:
  - command: `go get modernc.org/sqlite`
  - error: `lookup proxy.golang.org: no such host`
- Attempted GOPROXY fallback 1:
  - command: `GOPROXY=https://goproxy.cn,direct go get modernc.org/sqlite`
  - error: `lookup goproxy.cn: no such host`
- Attempted GOPROXY fallback 2:
  - command: `GOPROXY=direct go get modernc.org/sqlite`
  - error: `lookup modernc.org: no such host`
- Effective workaround:
  - used locally cached module `modernc.org/sqlite@v1.18.2` and offline-capable verification.
- Failure 4:
  - command: `go get github.com/beyond5959/codex-acp@dev`
  - error: `lookup proxy.golang.org: no such host`
- Effective workaround:
  - reused locally cached `github.com/beyond5959/codex-acp` pseudo-version already present in module cache and pinned it as direct dependency in `go.mod`.

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
  - startup log includes time, port, db path, supported agents.
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

- Canonical module path is now finalized as `github.com/beyond5959/go-acp-server`.
- All in-repo Go import paths were updated from placeholder path to canonical path.

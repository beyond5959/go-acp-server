# DECISIONS

## ADR Index

- ADR-001: HTTP/JSON API with SSE streaming transport. (Accepted)
- ADR-002: Client identity via `X-Client-ID` header. (Accepted)
- ADR-003: SQLite append-only events table as interaction source of truth. (Accepted)
- ADR-004: Permission handling defaults to fail-closed. (Accepted)
- ADR-005: Default bind is localhost only. (Accepted)
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
  - startup summary gains a `Web:` line pointing to the UI URL.
- Consequences: single-binary distribution with no external file dependencies; Go binary size increases by the size of the minified JS/CSS bundle (~200–400 KB estimated). Build pipeline requires Node.js for frontend changes.
- Alternatives considered: separate static file directory (requires deployment of two artifacts); WebSocket-only SPA (rejected: SSE already implemented); React/Vue framework (rejected: adds runtime bundle weight and build complexity).
- Follow-up actions: add `npm run build` to CI pipeline; version-pin Node.js in project tooling docs.

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

- Status: Accepted
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
- Context: repository ownership and canonical GitHub path are now stable (`github.com/beyond5959/go-acp-server`), while source imports still used a placeholder module path.
- Decision:
  - set `go.mod` module path to `github.com/beyond5959/go-acp-server`.
  - update all in-repo imports from `github.com/example/code-agent-hub-server/...` to canonical module path.
- Consequences: local builds/tests and downstream module consumers resolve a single stable import path; placeholder path drift is removed.
- Alternatives considered: keep placeholder path longer and defer until post-release.
- Follow-up actions: ensure any external examples/scripts use canonical import path only.

## ADR-014: Codex Provider Migration to Embedded Library

- Status: Accepted
- Date: 2026-02-28
- Context: sidecar mode required user-facing binary path configuration (`--codex-acp-go-bin`) and made deployment ergonomics/error modes depend on path wiring.
- Decision:
  - replace codex turn execution from external `codex-acp-go` process spawning to in-process `github.com/beyond5959/codex-acp/pkg/codexacp` embedded runtime.
  - remove user-facing codex binary path flags; server now links codex-acp library directly.
  - keep lazy startup and per-thread isolation by creating one embedded runtime per thread provider on first turn.
  - keep existing HTTP/SSE/permission/history contracts unchanged; permission round-trip remains fail-closed.
  - set `/v1/agents` codex status by embedded runtime preflight (`available`/`unavailable`) instead of path-config presence.
- Consequences: simpler operator UX and fewer path misconfiguration failures; server binary is now more tightly coupled to codex-acp module/runtime behavior.
- Alternatives considered: keep sidecar-only mode; dual mode (embedded + sidecar fallback).
- Follow-up actions: define codex-acp version pin/upgrade policy and add compatibility smoke checks across codex CLI/app-server versions.

## ADR-015: First-Turn Prompt Passthrough for Embedded Slash Commands

- Status: Accepted
- Date: 2026-02-28
- Context: context-window injection always wrapped prompts with `[Conversation Summary]` / `[Recent Turns]` / `[Current User Input]`, which masked first-turn slash commands (for example `/mcp call`) in embedded codex-acp flows.
- Decision:
  - keep context wrapper for normal multi-turn continuity.
  - when `summary == ""` and there are no visible recent turns, pass through raw `currentInput` (still bounded by `context-max-chars`) instead of wrapping.
- Consequences:
  - first-turn slash commands remain functional in embedded mode, enabling deterministic permission round-trip validation (`approved` / `declined`).
  - first-turn request text persisted in history no longer includes synthetic wrapper headings.
- Alternatives considered:
  - parse wrapped `[Current User Input]` inside codex-acp slash-command parser.
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
  - print a concise multi-line startup summary to stderr with `Time`, `HTTP`, `DB`, `Agents`, and `Help`.
  - keep structured request completion logs via `slog` for all HTTP traffic.
  - include `requestTime`, `method`, `path`, `ip`, `statusCode`, `durationMs`, and `responseBytes` in completion logs.
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

# Progress

## Project Overview

Code Agent Hub Server is a Go service that exposes HTTP/JSON APIs and SSE streaming for multi-client, multi-thread agent turns.
The system targets ACP-compatible agent providers, lazily starts per-thread agents, persists interaction history in SQLite, and bridges runtime permission requests back to clients.
Current built-in providers are `codex`, `claude`, `opencode`, `gemini`, and `qwen`.
This file is the source of milestone progress, validation commands, and next actions.

## Current Milestone

- `Post-M8` ACP multi-agent readiness and maintenance.

## Latest Update (2026-03-06)

- codex embedded compatibility hotfix integrated:
  - patched local dependency `../acp-adapter` to handle newer codex app-server approval request methods (`item/commandExecution/requestApproval`, `item/fileChange/requestApproval`) and to avoid misleading `-32601 method not found` on unsupported server requests.
- validation:
  - pass: `go test ./...` (in `../acp-adapter`)
  - pass: `go test ./...` (in this repository)

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
  - kept current-state note explicit: built-in providers are `codex`, `claude`, `opencode`, `gemini`, and `qwen`.
  - simplified README startup path to `agent-hub-server` with explicit `agent-hub-server --help` guidance.
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
    - pass: `go test ./cmd/agent-hub-server ./internal/httpapi -count=1`
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
  - updated `cmd/agent-hub-server/main.go` to pass `webui.Handler()` and print a startup QR code for opening the UI.
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
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`
  - added unit tests (`TestPreflight_*`, `TestNew_*`, `TestClose_*`, `TestDefaultRuntimeConfig_ReadsEnv`) covering token presence/absence, default/custom timeouts, and idempotent close.
  - added optional real smoke test (`E2E_CLAUDE=1 go test ./internal/agents/claude/ -run TestClaudeE2ESmoke -v -timeout 120s`); confirmed `PONG` response and `stopReason=end_turn` (16.68s).
  - added `go.mod` `replace` directive pointing to local `github.com/beyond5959/acp-adapter` for local development; refreshed module dependencies for the embedded Claude runtime integration.
  - wired claude into `cmd/agent-hub-server/main.go`: preflight call, `"claude"` in `AllowedAgentIDs`, `case "claude"` in `TurnAgentFactory`, real status in `supportedAgents`.
  - updated `main_test.go`: `supportedAgents` signature extended with `claudeAvailable bool`; added claude id/status assertions.
  - executed validation:
    - pass: `go build ./...`
    - pass: `go test ./...` (all packages green)
    - pass: `E2E_CLAUDE=1 go test ./internal/agents/claude -run TestClaudeE2ESmoke -v -timeout 120s` (response: `PONG`, stopReason: `end_turn`)

- `Post-F9` CI and release pipeline completed:
  - removed `internal/webui/web/dist/` and `tsconfig.tsbuildinfo` from git tracking (`git rm --cached`); added both to `.gitignore`.
  - created `.github/workflows/ci.yml`: triggers on every push/PR (non-tag); steps: Go + Node.js 20 setup, `make build-web`, gofmt check, `go test ./...`.
  - created `.github/workflows/release.yml`: triggers on `v*.*.*` tags; runs `goreleaser release --clean` with `GITHUB_TOKEN`.
  - created `.goreleaser.yml` (version 2): `before.hooks: make build-web`; cross-compiles linux/darwin/windows × amd64/arm64 (windows arm64 excluded) with `CGO_ENABLED=0`; produces `agent-hub-server_VERSION_OS_ARCH.tar.gz` archives + `checksums.txt`.
  - updated `Makefile`: `make build` now outputs to `bin/agent-hub-server` (was `go build ./...`).
  - updated `README.md`: replaced `go install` with "Download pre-built binary" table + "Build from source" (`make build`) instructions.
  - updated `AGENTS.md`: replaced "MUST keep web/dist committed" rule with "MUST NOT commit web/dist"; added CI/Release pipeline section.



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
  - `go test ./cmd/agent-hub-server ./internal/httpapi -count=1`
  - `go test ./...`
- Result:
  - opencode/qwen package tests: pass
  - opencode real smoke: pass (`PONG`, `stopReason=end_turn`)
  - qwen version check: pass (`0.11.0`)
  - qwen package tests: pass
  - qwen real smoke: pass (`PONG`, `stopReason=end_turn`)
  - server/httpapi regression tests: pass
  - full repo tests: pass

## Dependency Fetch Notes

- Date: `2026-02-28`
- Failure 1:
  - command: `go get modernc.org/sqlite`
  - error: `lookup proxy.golang.org: no such host`
- Effective workaround:
  - used locally cached module `modernc.org/sqlite@v1.18.2` and offline-capable verification.
- Effective workaround:
  - reused locally cached `github.com/beyond5959/acp-adapter` pseudo-version already present in module cache and pinned it as direct dependency in `go.mod`.

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

- Canonical module path is now finalized as `github.com/beyond5959/go-acp-server`.
- All in-repo Go import paths were updated from placeholder path to canonical path.

- `Post-F9` Web UI multi-thread streaming behavior fixed:
  - removed UI behavior that aborted an in-flight SSE stream when switching threads.
  - changed client stream tracking from single global `streamState` to per-thread `streamStates`.
  - maintained per-thread stream runtime maps (stream handle, delta buffer, start time), so background threads can keep streaming and finalize correctly.
  - wired send/cancel/input disable logic to current thread only, enabling concurrent in-flight turns across different threads.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` permission countdown updated to 2 hours:
  - changed server default permission timeout to `2 * time.Hour`.
  - changed Web UI Permission Required countdown to 2 hours and display format to `H:MM:SS`.

- `Post-F9` permission card persistence across thread switch fixed:
  - stored pending permission requests per thread in Web UI runtime state.
  - when switching back to a thread, pending Permission Required cards are re-mounted with original deadline.
  - resolved/timeout outcomes remove pending records to avoid stale prompts.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` thread model selection and switching completed:
  - added thread update API `PATCH /v1/threads/{threadId}` (agentOptions-only payload) with ownership checks and active-turn conflict (`409`).
  - added model discovery API `GET /v1/agents/{agentId}/models` (ACP handshake-backed).
  - storage layer now supports `UpdateThreadAgentOptions` and updates thread `updated_at`.
  - successful thread option update closes cached per-thread provider, so next turn re-initializes with new model config.
  - wired runtime model discovery into all providers:
    - `codex`/`claude`: ACP embedded `session/new.configOptions`.
    - `gemini`/`opencode`/`qwen`: ACP `session/new.models.availableModels`.
  - wired `agentOptions.modelId` forwarding into all providers:
    - embedded `codex`/`claude` now pass `model` in ACP `session/new`.
    - `gemini` now passes `model` in `session/new` and `session/prompt`.
    - `opencode` passes `modelId` in `session/prompt`; `qwen` passes `model` in `session/prompt`.
  - Web UI updates:
    - new-thread modal model selector removed (create flow keeps agent/cwd/title/advanced JSON only).
    - active thread header switched from free-text model input to ACP-backed model dropdown + Apply action.
    - model controls are disabled while model lists load and during streaming turns.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` thread session model config switched to ACP `configOptions` + immediate apply:
  - added thread-scoped config options APIs:
    - `GET /v1/threads/{threadId}/config-options`
    - `POST /v1/threads/{threadId}/config-options`
  - `POST` now applies model changes through ACP `session/set_config_option` (no separate apply endpoint/action).
  - provider-side config option support added across all built-in agents:
    - embedded: `codex`, `claude` (in-session `session/set_config_option` on cached runtime).
    - stdio: `opencode`, `qwen`, `gemini` (ACP handshake + `session/set_config_option` apply path, then persist selected model for next turns).
  - Web UI changes:
    - removed thread header `Apply` button.
    - model dropdown now applies immediately on selection.
    - model source switched from agent-level model catalog to thread-level `configOptions` (`category=model`).
    - model option descriptions are rendered under the selector in the chat header.
  - thread metadata sync:
    - successful model switch persists `agentOptions.modelId` for thread continuity and restart recovery.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` thread reasoning selector and config override persistence completed:
  - backend now persists non-model session config selections under `agentOptions.configOverrides` when `POST /v1/threads/{threadId}/config-options` succeeds.
  - provider factories now restore persisted config overrides on fresh embedded sessions and per-turn stdio handshakes, so reasoning-style settings survive across future turns and restarts.
  - Web UI composer footer now renders both `Model` and `Reasoning` controls; reasoning options refresh from the latest thread `configOptions` after model changes.
  - reasoning control remains model-specific and is disabled/updated in the same active-turn safety envelope as model switching.
  - added coverage for config override persistence and thread agent-option parsing.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` shared agent config catalog caching completed:
  - Web UI no longer re-fetches thread config options when switching between threads that use the same agent and already have a cached agent config catalog.
  - frontend now keeps:
    - thread-scoped current config state cache.
    - agent-scoped shared config catalog cache for model/reasoning option lists.
  - same-agent threads reuse the same model/reasoning lists while keeping independent selected current values derived from each thread's persisted `agentOptions`.
  - composer footer pills now show only the selected model/reasoning names, without leading `MODEL` / `REASONING` labels.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`

- `Post-F9` codex thread-config timeout fix (Playwright real-env regression):
  - reproduced in real browser flow (Playwright MCP + local codex env): opening a codex thread triggered `GET /v1/threads/{threadId}/config-options` `503` caused by embedded startup timeout at 8s.
  - fixed by increasing embedded runtime default startup timeout from `8s` to `30s` for:
    - `internal/agents/codex`
    - `internal/agents/claude`
  - reran real browser flow:
    - thread model list now loads successfully from ACP `configOptions`.
    - model switching calls `POST /v1/threads/{threadId}/config-options` and persists selected model.
    - no frontend console errors during switch flow.
  - executed validation:
    - pass: `go test ./...`

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
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` persisted agent config catalog completed:
  - added sqlite-backed `agent_config_catalogs` storage keyed by `agent_id + model_id`, with a reserved default snapshot row used when a thread has no explicit model selection yet.
  - `GET /v1/threads/{threadId}/config-options` and `GET /v1/agents/{agentId}/models` now read persisted catalog data first, so service restart can keep serving model/reasoning metadata without blocking on live provider queries.
  - `POST /v1/threads/{threadId}/config-options` now writes through both:
    - thread-local selected values into `threads.agent_options_json`
    - current model config catalog into sqlite for reuse across threads/restarts
  - service startup now launches a background catalog refresher that silently re-queries built-in agents and refreshes stored model/reasoning catalogs without delaying frontend availability.
  - Web UI config cache is now keyed by `agent + selected model`, so different threads on the same agent no longer accidentally reuse the wrong reasoning list for another model.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

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
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` sidebar thread activity indicators completed:
  - thread list now shows a live spinner for any thread with an in-flight turn, so background work stays visible after switching to another thread.
  - when a background turn finishes, the spinner flips to a green check badge that stays on that thread until the user opens it again.
  - slowed the sidebar thread spinner slightly so the activity indicator reads as background work instead of a high-frequency busy loop.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` sidebar thread drawer actions and rename completed:
  - replaced the direct delete icon in sidebar thread rows with a drawer trigger.
  - added drawer actions for inline rename and delete, with rename ordered before delete and delete styled as the only dangerous text action.
  - extended `PATCH /v1/threads/{threadId}` so thread title updates reuse the existing ownership and active-turn conflict model.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

- `Post-F9` sidebar thread action popover refinement completed:
  - replaced the expanding inline drawer with a floating popover anchored to the three-dot trigger, so opening thread actions no longer changes sidebar row height.
  - kept rename and delete in the same order, with rename editing rendered in a floating panel and delete remaining the red dangerous action.
  - executed validation:
    - pass: `cd internal/webui/web && npm run build`
    - pass: `go test ./...`

 
- 2026-03-06: Removed the thread action trigger's per-thread actionLabel/title tooltip in the Web UI popover menu; the three-dot button now uses a neutral `aria-label` only, without hover text tied to the thread title.
- 2026-03-06: Moved the sidebar thread action menu and rename form into a sidebar-level floating layer instead of rendering them inside each thread row, so the rename UI is no longer clipped by the thread list or sidebar overflow.
- 2026-03-06: fixed embedded codex permission bridge timeout mismatch by aligning adapter-side `session/request_permission` wait window to 2h (was 30s), matching hub default timeout and avoiding premature fail-closed during manual approval.
- 2026-03-06: fixed embedded codex server-request compatibility for tool interaction:
  - `item/tool/requestUserInput` now returns schema-compatible answers (auto-select first option label per question) instead of `-32000 not supported`.
  - `item/tool/call` now returns structured tool failure payload (`success=false`) instead of JSON-RPC method error, so app-server no longer aborts the whole flow on this request type.

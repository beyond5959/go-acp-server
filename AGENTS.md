# AGENTS

This repository implements the **Ngent** — a local-first Go service exposing HTTP/JSON APIs and SSE streaming for multi-client, multi-thread ACP-compatible agent turns.

ACP protocol reference: <https://agentclientprotocol.com/>

## Mandatory Rules

- MUST use Go 1.24.
- MUST keep `go test ./...` passing for every change.
- MUST default to local-only bind (`127.0.0.1:8686`); require `--allow-public=true` for LAN-accessible binds.
- MUST support local-only mode when `--allow-public=false` (loopback-only binds).
- MUST validate inputs:
  - `agent` must be in server allowlist.
  - `cwd` must be an absolute path.
  - `cwd` must be inside configured allowed roots, otherwise reject.
- MUST enforce concurrency model:
  - one active turn per `(thread, session)` scope at a time.
  - thread-level destructive or shared-state operations must still fail/lock at whole-thread scope.
  - cancel must take effect quickly.
  - permission workflow is fail-closed by default.
- MUST keep stdout and HTTP output protocol-only.
- MUST send logs to stderr using the repo's human-readable logger; access logs should be easy to scan at a glance and ACP debug logs must still redact secrets.
- MUST redact sensitive information in logs and errors.

## Long-Term Memory Rules

- At the end of every completed phase, update `PROGRESS.md`.
- Write key technical/product decisions to `docs/DECISIONS.md`.
- Track known limitations and risks in `docs/KNOWN_ISSUES.md`.
- Keep acceptance checklist in `docs/ACCEPTANCE.md`.
- Keep implementation design in `docs/SPEC.md`.

---

## Frontend (Web UI)

Milestones F0–F9 are complete. The frontend is a no-framework Vite + TypeScript SPA embedded in the Go binary via `//go:embed web/dist`.

### Stack

- **Build**: Vite 6 + TypeScript 5.6, output to `internal/webui/web/dist/`.
- **Rendering**: vanilla DOM, no virtual DOM or component framework.
- **Markdown**: `marked@^9` with custom renderer; `highlight.js` core (go/ts/js/python/bash/json/yaml).
- **Go embed**: `internal/webui/webui.go` embeds `web/dist`, exposes `webui.Handler()` — an SPA fallback handler registered at lowest priority in `httpapi`.
- **Build targets** (Makefile):
  - `make build-web` — `npm ci && npm run build` inside `internal/webui/web/`.
  - `make build` — `build-web` then `go build ./...`.
  - `make run` — `build-web` then `go run ./cmd/ngent`.

### Source file map

All files live under `internal/webui/web/src/`.

| File | Purpose |
|---|---|
| `types.ts` | All TypeScript interfaces: `AgentInfo`, `Thread`, `Turn`, `TurnEvent`, `Message`, `PermissionRequest`, `StreamState`, `AppState`. |
| `utils.ts` | `generateUUID`, `formatTimestamp`, `formatRelativeTime`, `isAbsolutePath`, `escHtml`, `debounce`. |
| `store.ts` | Singleton `AppStore`: `get()` / `set(patch)` / `subscribe(fn) → unsub`. Persists `clientId`, `authToken`, `serverUrl`, `theme` to `localStorage` (keys `ngent:*`). Never persists runtime data. |
| `api.ts` | `ApiClient` singleton (`api`): reads `serverUrl`/`clientId`/`authToken` from store on every call. Methods: `getAgents`, `getThreads`, `getHistory`, `createThread`, `startTurn`, `cancelTurn`, `resolvePermission`. |
| `sse.ts` | `TurnStream` class: POST SSE via `fetch` + `ReadableStream` (not `EventSource` — lacks POST/custom-header support). Parses `event:\ndata:\n\n` blocks. Callbacks: `onTurnStarted`, `onDelta`, `onCompleted`, `onError`, `onPermissionRequired`, `onDisconnect`. `abort()` sets `terminated=true` and aborts fetch. |
| `markdown.ts` | Configures `marked` renderer: `html()` → `escHtml()` (XSS guard); `code()` → `.code-block` with hljs highlight, copy button, optional "Show all N lines" fold (>20 lines). Exports `renderMarkdown(text)` and `bindMarkdownControls(container)` (idempotent, `data-bound` guard). |
| `main.ts` | App entry: `renderShell()`, `init()`, all DOM wiring. See patterns below. |
| `components/settings-panel.ts` | Slide-in drawer: Client ID display/copy/reset, Bearer Token, Server URL, Light/Dark/System theme toggle. |
| `components/new-thread-modal.ts` | Modal: agent card grid (radio, disabled for unavailable), absolute-path CWD validation, optional title, collapsible JSON agent-options textarea. |
| `components/permission-card.ts` | `mountPermissionCard(listEl, event)`: appends ephemeral card with a 15 s countdown, renders all agent-advertised permission options (falls back to Allow/Deny when none are provided), and shows resolved states. Calls `api.resolvePermission()`; ignores 409. |
| `style.css` | All styles. CSS custom properties (`--bg`, `--accent`, etc.) with `[data-theme="dark"]` override block. hljs tokens use CSS variables (`--hljs-fg`, `--hljs-kw`, …). |

### Key patterns in `main.ts`

**Streaming sentinel** — prevents `updateMessageList()` from wiping the live bubble:
```
let activeStreamMsgId: string | null = null
```
The subscribe handler skips `updateMessageList()` while `activeStreamMsgId !== null`.

**Subscribe handler logic**:
```
store.subscribe(() => {
  const threadChanged = activeThreadId !== lastRenderThreadId
  const chatScopeChanged = chatScopeKey !== lastRenderChatScopeKey
  updateThreadList()
  if (threadChanged || (chatScopeChanged && !getScopeStreamState(chatScopeKey))) {
    lastRenderThreadId = activeThreadId
    lastRenderChatScopeKey = chatScopeKey
    updateChatArea()               // full re-render on thread switch
  } else {
    if (!activeStreamMsgId) updateMessageList()   // skip during streaming
    updateInputState()
  }
})
```

**`handleSend()` sequence**:
1. Add user message → `addMessageToStore` (triggers subscribe → renders user bubble).
2. Compute the current chat scope key `threadId::sessionId`.
3. Set `activeStreamMsgId = agentMsgId` before `setScopeStreamState(...)`.
4. Append streaming bubble directly to DOM (`id="bubble-<agentMsgId>"`).
5. `api.startTurn(...)` — `onDelta` patches `bubbleEl.textContent` in-place.
6. `onSessionBound`: move in-memory message/stream buffers from the provisional scope into the bound session scope.
7. `onCompleted` / `onError` / `onDisconnect`: clear scope-local stream tracking and append the finalized message back into that chat scope.

**Smart auto-scroll** (`isNearBottom(el)` — 100px threshold):
- `onDelta`: only scrolls when user was already near the bottom.
- `updateMessageList`: always scrolls, hides the scroll-to-bottom button.
- Scroll-to-bottom button (`#scroll-bottom-btn`) shown by scroll-event listener on `#message-list`; button is inside `.message-list-wrap` (position: relative).

**History load guards** in `loadHistory(threadId)`:
- After `await api.getHistory(...)`, check `state.activeThreadId !== threadId` (navigated away), that the selected `sessionId` still matches, and that `getScopeStreamState(threadId::sessionId)` is empty before writing to store.

**Markdown rendering** is applied only for agent `done` messages via `renderMarkdown(bodyText)` in `renderMessage()`. Streaming bubbles use plain `textContent` assignment; markdown is rendered once after `onCompleted` fires and `updateMessageList()` re-renders from store.

**Global keyboard shortcuts** (`bindGlobalShortcuts()`, called once in `init()`):
- `/` — focus `#search-input`.
- `Cmd/Ctrl+N` — open new thread modal.
- `Escape` — (1) close mobile sidebar, (2) clear/blur search, (3) cancel active stream.

**Permission card** is ephemeral: appended to `#message-list` during streaming, disappears naturally when `updateMessageList()` re-renders after `onCompleted`.

### CI / Release pipeline

- **`.github/workflows/ci.yml`** — triggers on every push/PR (non-tag). Steps: set up Go + Node.js 20, `make build-web`, gofmt check, `go test ./...`.
- **`.github/workflows/release.yml`** — triggers on `v*.*.*` tags. Runs `goreleaser release --clean` which executes `make build-web` (via `.goreleaser.yml` `before.hooks`) then cross-compiles for linux/darwin/windows (amd64 + arm64) with `CGO_ENABLED=0`.
- **`.goreleaser.yml`** — produces `ngent_VERSION_OS_ARCH.tar.gz` archives + `checksums.txt` and publishes to GitHub Releases.
- To cut a release: `git tag v0.x.y && git push origin v0.x.y`.

- MUST run `npm run build` (i.e. `tsc -b && vite build`) and confirm zero TypeScript errors before committing.
- MUST run `go test ./...` after any change (including frontend-only changes that affect `web/dist`).
- MUST NOT add a JS framework (React, Vue, Svelte, etc.).
- MUST NOT use `EventSource` for SSE — it does not support POST or custom headers. Use `TurnStream` (`fetch` + `ReadableStream`).
- MUST set `activeStreamMsgId` before calling `setScopeStreamState(...)` to prevent the subscribe handler from wiping the streaming bubble.
- MUST call `bindMarkdownControls(container)` after any `innerHTML` assignment that may contain `.code-copy-btn`, `.code-expand-btn`, or `.msg-copy-btn` elements.
- MUST escape user/agent content with `escHtml()` before inserting as HTML; only pass finalised agent text to `renderMarkdown()`.
- MUST NOT commit `web/dist/` — it is gitignored and built by CI. Local builds use `make build` or `make build-web`.

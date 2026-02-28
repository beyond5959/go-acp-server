# Go ACP Server

## Project Goal 🎯

`go-acp-server` is a local-first Code Agent Hub Server that provides:

- HTTP/JSON APIs with SSE streaming for agent turns
- Multi-thread conversation state persisted in SQLite
- ACP-compatible agent provider architecture (for example Claude Code, Gemini, OpenCode, Codex)
- Strict runtime controls: one active turn per thread, fast cancel, and fail-closed permission handling

By default, the server listens on `127.0.0.1` only.

Current implementation status:

- Built-in provider today: `codex` (embedded)
- Additional ACP-compatible providers are planned through the same provider abstraction

## Installation

### Download pre-built binary (recommended)

Download the latest release for your platform from the [GitHub Releases](https://github.com/beyond5959/go-acp-server/releases) page.

Supported platforms:

| OS      | Architecture |
|---------|-------------|
| Linux   | amd64, arm64 |
| macOS   | amd64 (Intel), arm64 (Apple Silicon) |
| Windows | amd64 |

Extract the archive and place `agent-hub-server` on your `$PATH`.

### Build from source

Requirements: Go `1.24+`, Node.js `20+`, npm.

```bash
git clone https://github.com/beyond5959/go-acp-server.git
cd go-acp-server
make build          # builds frontend then Go binary → bin/agent-hub-server
```

## Run

This README uses the default DB home path:

- `DB_HOME=$HOME/.go-agent-server`
- `DB_PATH=$HOME/.go-agent-server/agent-hub.db`

Recommended local startup:

```bash
agent-hub-server
```

Show all CLI options:

```bash
agent-hub-server --help
```

`--db-path` is optional. If omitted, the server uses:

- `$HOME/.go-agent-server/agent-hub.db`
- The server automatically creates `$HOME/.go-agent-server` if it does not exist.

With bearer auth token:

```bash
agent-hub-server \
  --listen 127.0.0.1:8686 \
  --db-path "$HOME/.go-agent-server/agent-hub.db" \
  --auth-token "your-token"
```

Public bind (explicitly opt in):

```bash
agent-hub-server \
  --listen 0.0.0.0:8686 \
  --allow-public=true \
  --db-path "$HOME/.go-agent-server/agent-hub.db"
```

Notes:

- `/v1/*` requests must include `X-Client-ID`.

## Quick Check

```bash
curl -s http://127.0.0.1:8686/healthz
curl -s -H "X-Client-ID: demo" http://127.0.0.1:8686/v1/agents
```

## Web UI

After starting the server, open your browser at the address shown in the startup summary:

```
Agent Hub Server started
  Time:   2026-02-28 18:01:02 UTC+8
  HTTP:   http://127.0.0.1:8686
  Web:    http://127.0.0.1:8686/
  DB:     /Users/you/.go-agent-server/agent-hub.db
  Agents: Codex (available), Claude Code (unavailable)
  Help:   agent-hub-server --help
```

The built-in web UI lets you:

- Create threads (choose agent, set working directory)
- Send messages and view streaming agent responses
- Approve or deny runtime permission requests inline
- Browse turn history across sessions
- Switch between light, dark, and system themes

No Node.js is required at runtime — the UI is compiled and embedded in the server binary.

To rebuild the frontend after local changes:

```bash
make build-web
go build ./...
```

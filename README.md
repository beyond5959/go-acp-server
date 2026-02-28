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

## Installation (go install)

Requirements:

- Go `1.24+`

Install from module:

```bash
go install github.com/beyond5959/go-acp-server/cmd/agent-hub-server@latest
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

# Ngent

[![CI](https://github.com/beyond5959/ngent/actions/workflows/ci.yml/badge.svg)](https://github.com/beyond5959/ngent/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/beyond5959/ngent)](https://go.dev/)
[![License](https://img.shields.io/github/license/beyond5959/ngent)](LICENSE)

> **Web Service Wrapper for ACP-compatible Agents**
>
> Ngent wraps command-line agents that speak the [Agent Client Protocol (ACP)](https://github.com/beyond5959/acp-adapter) into a web service, making them accessible via HTTP API and Web UI.

## What is Ngent?

Ngent acts as a bridge between **ACP-compatible agents** (like Codex, Kimi CLI, OpenCode, etc.) and **web clients**:

```
┌─────────────┐     HTTP/WebSocket     ┌─────────┐     JSON-RPC (ACP)     ┌──────────────┐
│  Web UI     │ ◄────────────────────► │  Ngent  │ ◄────────────────────► │  CLI Agent   │
│  /v1/* API  │   SSE streaming        │  Server │   stdio                │  (ACP-based) │
└─────────────┘                        └─────────┘                        └──────────────┘
```

### How it Works

1. **ACP Protocol**: Agents like Codex, Kimi CLI, OpenCode, etc. expose their capabilities through the Agent Client Protocol (ACP) — a JSON-RPC protocol over stdio
2. **Ngent Bridge**: Ngent spawns these CLI agents as child processes and translates their ACP protocol into HTTP/JSON APIs
3. **Web Interface**: Provides a built-in Web UI and REST API for creating conversations, sending prompts, and managing permissions

### Features

- 🔌 **Multi-Agent Support**: Works with any ACP-compatible agent (Codex, Kimi CLI, OpenCode, etc.)
- 🌐 **Web API**: HTTP/JSON endpoints with Server-Sent Events (SSE) for streaming responses
- 🖥️ **Built-in UI**: No separate frontend deployment needed — the web UI is embedded in the binary
- 🔒 **Permission Control**: Fine-grained approval system for agent file/system operations
- 💾 **Persistent State**: SQLite-backed conversation history across sessions
- 📱 **Mobile Friendly**: QR code for easy access from mobile devices on the same network


## Supported Agents

| Agent | Supported |
|---|---|
| Codex | ✅ |
| Claude Code | ✅ |
| Cursor CLI | ✅ |
| Gemini CLI | ✅ |
| Kimi CLI | ✅ |
| Qwen Code | ✅ |
| OpenCode | ✅ |
| BLACKBOX AI | ✅ |



## Installation

### Quick Install (recommended for Linux/macOS)

```bash
curl -sSL https://raw.githubusercontent.com/beyond5959/ngent/master/install.sh | bash

# Or install to a custom directory:
curl -sSL https://raw.githubusercontent.com/beyond5959/ngent/master/install.sh | INSTALL_DIR=~/.local/bin bash
```

## Run

Start with default settings (local-only):

```bash
ngent
```

LAN-accessible mode (allows connections from other devices):

```bash
ngent --allow-public=true
```

Custom port:

```bash
ngent --port 8080
```

With authentication:

```bash
ngent --auth-token "your-token"
```

Custom data directory:

```bash
ngent --data-path /path/to/ngent-data
```

Show all options:

```bash
ngent --help
```

**Default paths:**
- Data directory: `$HOME/.ngent/`
- Database: `$HOME/.ngent/ngent.db`
- Attachments: `$HOME/.ngent/attachments/<category>/`

Notes:

- `/v1/*` requests must include `X-Client-ID`.

## Quick Check

```bash
curl -s http://127.0.0.1:8686/healthz
curl -s -H "X-Client-ID: demo" http://127.0.0.1:8686/v1/agents
```

## Web UI

Open the URL shown in the startup output (e.g., `http://127.0.0.1:8686/`). 

# DEPLOYMENT

How ngent is built, installed, and run in production on this homelab.

## Overview

ngent runs as a **systemd user service** on the host. It is compiled from source and
installed as a static binary at `/usr/local/bin/ngent`. The service starts automatically
on login and restarts on failure.

## Build & Install

```bash
# From the repo root
cd ~/workspace/ngent

# Build the binary
go build -o /tmp/ngent ./cmd/ngent/

# Install (requires sudo)
sudo cp /tmp/ngent /usr/local/bin/ngent
```

## systemd Unit

File: `~/.config/systemd/user/ngent.service`

```ini
[Unit]
Description=Ngent ACP server (axon-android)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/ngent -port 8686 -allow-public -auth-token <token>
Restart=always
RestartSec=5
KillMode=process
Environment=HOME=/home/jmagar
Environment="PATH=/home/jmagar/.local/share/fnm/aliases/default/bin:/home/jmagar/.local/bin:/usr/local/bin:/usr/bin:/bin"

[Install]
WantedBy=default.target
```

Key flags:
- `-port 8686` — HTTP listen port (reachable over Tailscale)
- `-allow-public` — allows connections without requiring a local origin
- `-auth-token` — bearer token; clients send as `x-api-key` header

## Service Management

```bash
# Start / stop / restart
systemctl --user start ngent
systemctl --user stop ngent
systemctl --user restart ngent

# Check status and recent logs
systemctl --user status ngent
journalctl --user -u ngent -f          # follow live
journalctl --user -u ngent -n 100      # last 100 lines

# Enable / disable autostart
systemctl --user enable ngent
systemctl --user disable ngent
```

## Updating (rebuild → restart)

```bash
cd ~/workspace/ngent
go build -o /tmp/ngent ./cmd/ngent/ && \
  sudo cp /tmp/ngent /usr/local/bin/ngent && \
  systemctl --user restart ngent && \
  systemctl --user status ngent
```

ngent holds open SQLite and agent processes. `Restart=always` with `KillMode=process`
means the service manager sends SIGTERM to the main process only — child agent processes
(Claude, Codex, etc.) are left to clean up via their own shutdown path.

## Network

ngent listens on `:8686`. It is **not** exposed to the public internet — access is
via Tailscale only. Android connects using the MagicDNS hostname (e.g.
`http://hostname.ts.net:8686`). Cleartext HTTP is permitted for Tailscale IPs via the
Android network security config (`res/xml/network_security_config.xml`).

## Agent Lifecycle

ngent keeps one live agent process per thread in an in-memory map (`agentsByScope`).
Between turns the process stays warm. An idle janitor runs every ~2.5 minutes and
reaps agents that have been idle for more than 5 minutes (`defaultAgentIdleTTL`).
Reaped agents get a cold start on the next turn.

## Data

ngent stores all state in SQLite. Default path is `~/.local/share/ngent/ngent.db`
(or whatever `$NGENT_DB` / `-db` flag points to). The DB survives restarts — thread
history, slash command cache, config option catalog, and session transcript cache all
persist across binary upgrades.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Service won't start | `journalctl --user -u ngent -n 50` — usually missing binary or bad flag |
| Android can't connect | Confirm Tailscale is up; `curl http://hostname.ts.net:8686/healthz` |
| Slash commands empty | Old cached state — send one turn to trigger `available_commands` SSE push |
| Agent cold-starts every turn | Check idle TTL; `journalctl` for `agent.idle_reclaimed` log lines |
| Auth failures | Confirm `x-api-key` header matches `-auth-token` value in unit file |

# DB

## Storage Choice

- Engine: SQLite.
- Access layer: `database/sql` + `modernc.org/sqlite` (pure Go driver).
- Runtime pragmas at open:
  - `PRAGMA foreign_keys = ON`
  - `PRAGMA busy_timeout = 5000`
  - `PRAGMA journal_mode = WAL`

## Migration Strategy

- Migrations are versioned and applied in order.
- Applied versions are recorded in `schema_migrations`.
- Repeat startup is idempotent: already applied versions are skipped.
- DDL uses `IF NOT EXISTS` to keep reruns safe.

### `schema_migrations`

- `version INTEGER PRIMARY KEY`
- `name TEXT NOT NULL`
- `applied_at TEXT NOT NULL`

## Implemented Tables

### `threads`

- `thread_id TEXT PRIMARY KEY`
- `agent_id TEXT NOT NULL`
- `cwd TEXT NOT NULL`
- `title TEXT NOT NULL`
- `agent_options_json TEXT NOT NULL`
- `summary TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

### `turns`

- `turn_id TEXT PRIMARY KEY`
- `thread_id TEXT NOT NULL REFERENCES threads(thread_id)`
- `request_text TEXT NOT NULL`
- `response_text TEXT NOT NULL`
- `is_internal INTEGER NOT NULL DEFAULT 0`
- `status TEXT NOT NULL`
- `stop_reason TEXT NOT NULL`
- `error_message TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `completed_at TEXT`

### `events`

- `event_id INTEGER PRIMARY KEY AUTOINCREMENT`
- `turn_id TEXT NOT NULL REFERENCES turns(turn_id)`
- `seq INTEGER NOT NULL`
- `type TEXT NOT NULL`
- `data_json TEXT NOT NULL`
- `created_at TEXT NOT NULL`

### `session_transcript_cache`

- `agent_id TEXT NOT NULL`
- `cwd TEXT NOT NULL`
- `session_id TEXT NOT NULL`
- `messages_json TEXT NOT NULL`
- `updated_at TEXT NOT NULL`
- `PRIMARY KEY (agent_id, cwd, session_id)`

### `agent_slash_commands`

- `agent_id TEXT PRIMARY KEY`
- `commands_json TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

## Indexes (M2)

- `idx_turns_thread_id_created_at` on `turns(thread_id, created_at)`
- `idx_events_turn_id_seq` unique index on `events(turn_id, seq)`
- `session_transcript_cache` primary key on `(agent_id, cwd, session_id)`

## Storage API (M2)

- `UpsertClient(clientID)` compatibility validator; no-op for SQLite persistence
- `CreateThread(...)`
- `GetThread(threadID)`
- `UpdateThreadSummary(threadID, summary)`
- `ListThreads()`
- `GetSessionTranscriptCache(agentID, cwd, sessionID)`
- `UpsertSessionTranscriptCache(...)`
- `GetAgentSlashCommands(agentID)`
- `UpsertAgentSlashCommands(...)`
- `CreateTurn(...)`
- `GetTurn(turnID)`
- `ListTurnsByThread(threadID)`
- `AppendEvent(turnID, type, dataJSON)`
- `ListEventsByTurn(turnID)`
- `FinalizeTurn(...)`

## Event Sequence Rule

- `AppendEvent` computes `seq` as `max(seq)+1` per `turn_id` in a transaction.
- Unique index on `(turn_id, seq)` enforces sequence uniqueness.

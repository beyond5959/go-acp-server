# API

This document defines the current HTTP API contract.

## Common Conventions

- JSON response content type: `application/json; charset=utf-8`.
- Except `/healthz`, every `/v1/*` endpoint requires `X-Client-ID` header (non-empty).
- For every `/v1/*` request with valid `X-Client-ID`, server upserts client presence (`UpsertClient`).
- Optional auth switch:
  - if server starts with `--auth-token=<token>`, `/v1/*` also requires `Authorization: Bearer <token>`.

## Runtime Logging Conventions

- Startup prints a human-readable multi-line summary on `stderr` with `Time`, `HTTP`, `DB`, `Agents`, and `Help`.
- Every HTTP request emits one structured completion log entry (`http.request.completed`) with:
  - `requestTime`
  - `method`
  - `path`
  - `ip`
  - `statusCode`
  - `durationMs`
  - `responseBytes`

## Unified Error Envelope

All errors use:

```json
{
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "human-readable message",
    "details": {
      "field": "cwd"
    }
  }
}
```

## Implemented Endpoints

1. `GET /healthz`
- Response `200`:

```json
{
  "ok": true
}
```

2. `GET /v1/agents`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- agent status contract:
  - each agent entry reports readiness as `available|unavailable`.
  - current built-in runtime implementation is `codex`; additional ACP-compatible agent ids may appear as unavailable placeholders until integrated.
- Response `200`:

```json
{
  "agents": [
    {
      "id": "codex",
      "name": "Codex",
      "status": "available"
    },
    {
      "id": "claude",
      "name": "Claude Code",
      "status": "unavailable"
    }
  ]
}
```

3. `POST /v1/threads`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- Request:

```json
{
  "agent": "<agent-id>",
  "cwd": "/abs/path",
  "title": "optional",
  "agentOptions": {
    "mode": "safe"
  }
}
```

- Validation:
  - `agent` must be in allowlist (currently `codex`; more ACP-compatible agents planned).
  - `cwd` must be absolute.
  - server default policy accepts any absolute `cwd`.
  - create thread only persists row; no agent process is started.

- Response `200`:

```json
{
  "threadId": "th_..."
}
```

4. `GET /v1/threads`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- Response `200`:

```json
{
  "threads": [
    {
      "threadId": "th_...",
      "agent": "<agent-id>",
      "cwd": "/abs/path",
      "title": "optional",
      "agentOptions": {},
      "summary": "",
      "createdAt": "2026-02-28T00:00:00Z",
      "updatedAt": "2026-02-28T00:00:00Z"
    }
  ]
}
```

5. `GET /v1/threads/{threadId}`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- Ownership rule:
  - if thread does not exist OR does not belong to `X-Client-ID`, return `404`.
- Response `200`:

```json
{
  "thread": {
    "threadId": "th_...",
    "agent": "<agent-id>",
    "cwd": "/abs/path",
    "title": "optional",
    "agentOptions": {},
    "summary": "",
    "createdAt": "2026-02-28T00:00:00Z",
    "updatedAt": "2026-02-28T00:00:00Z"
  }
}
```

6. `POST /v1/threads/{threadId}/turns`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- Request:

```json
{
  "input": "hello",
  "stream": true
}
```

- Behavior:
  - response is SSE (`text/event-stream`).
  - same thread allows only one active turn at a time.
  - if another turn is active on that thread, return `409 CONFLICT`.
  - if provider requests runtime permission, server emits `permission_required` and pauses turn until decision/timeout.

- SSE event types:
  - `turn_started`: `{"turnId":"..."}`
  - `message_delta`: `{"turnId":"...","delta":"..."}`
  - `permission_required`: `{"turnId":"...","permissionId":"...","approval":"command|file|network|mcp","command":"...","requestId":"..."}`
  - `turn_completed`: `{"turnId":"...","stopReason":"end_turn|cancelled|error"}`
  - `error`: `{"turnId":"...","code":"...","message":"..."}`

- Permission fail-closed contract:
  - permission request timeout or disconnected stream defaults to `declined`.
  - fake ACP flow uses terminal `stopReason="cancelled"` for `declined`/`cancelled`.

7. `POST /v1/turns/{turnId}/cancel`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- Behavior:
  - requests cancellation for active turn.
  - terminal stream event should end with `stopReason=cancelled` if cancellation wins race.
- Response `200`:

```json
{
  "turnId": "tu_...",
  "threadId": "th_...",
  "status": "cancelling"
}
```

8. `GET /v1/threads/{threadId}/history`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- Query:
  - `includeEvents=true|1` (optional, default false)
  - `includeInternal=true|1` (optional, default false)
- Response `200`:

```json
{
  "turns": [
    {
      "turnId": "tu_...",
      "requestText": "hello",
      "responseText": "hello",
      "status": "completed",
      "stopReason": "end_turn",
      "errorMessage": "",
      "createdAt": "2026-02-28T00:00:00Z",
      "completedAt": "2026-02-28T00:00:01Z",
      "events": [
        {
          "eventId": 1,
          "seq": 1,
          "type": "turn_started",
          "data": {
            "turnId": "tu_..."
          },
          "createdAt": "2026-02-28T00:00:00Z"
        }
      ]
    }
  ]
}
```

9. `POST /v1/permissions/{permissionId}`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- Request:

```json
{
  "outcome": "approved"
}
```

10. `POST /v1/threads/{threadId}/compact`
- Headers: `X-Client-ID` (required), optional bearer auth if enabled.
- Request (optional body):

```json
{
  "maxSummaryChars": 1200
}
```

- Behavior:
  - triggers one internal summarization turn (`is_internal=1`).
  - updates `threads.summary` on success.
  - internal compact turn is hidden from default history.

- Response `200`:

```json
{
  "threadId": "th_...",
  "turnId": "tu_...",
  "status": "completed",
  "stopReason": "end_turn",
  "summary": "updated summary text",
  "summaryChars": 324
}
```

- Validation:
  - `outcome` must be one of `approved|declined|cancelled`.
  - `permissionId` must exist and belong to the same `X-Client-ID`.
  - already-resolved permission returns `409 CONFLICT`.

- Response `200`:

```json
{
  "permissionId": "perm_...",
  "status": "recorded",
  "outcome": "approved"
}
```

## Baseline Error Codes

- `INVALID_ARGUMENT`: validation failed.
- `UNAUTHORIZED`: bearer token missing or invalid.
- `FORBIDDEN`: path/policy denied.
- `NOT_FOUND`: endpoint/resource missing, including cross-client thread/turn access.
- `CONFLICT`: active-turn conflict or invalid cancel state.
- `TIMEOUT`: upstream/model operation exceeded allowed time budget.
- `UPSTREAM_UNAVAILABLE`: configured agent/provider is unavailable or failed to start/respond.
- `INTERNAL`: unexpected server/storage failure.

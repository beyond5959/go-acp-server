# CONTEXT WINDOW

## Goal

Provide deterministic context injection for each turn so continuity survives both:
- live process continuation (fast path while server is running), and
- server restart recovery (rebuild from SQLite `threads.summary` + recent turns).

## Data Sources

- `threads.summary`: rolling durable summary for older conversation state.
- `turns` + `events`: full durable history.
- `turns.is_internal`: marks compact/summarization turns; hidden from default history and excluded from recent user-visible window.

## Injection Strategy (Implemented)

For each user turn, server builds:

1. `[Conversation Summary]` block from `threads.summary`
2. `[Recent Turns]` block from latest non-internal turns
3. `[Current User Input]` block from current request input

This assembled prompt is sent to provider as the injected prompt.

## Runtime Controls

CLI flags:
- `--context-recent-turns` (default `10`): max non-internal turns included in recent window.
- `--context-max-chars` (default `20000`): max characters for injected prompt.
- `--compact-max-chars` (default `4000`): max summary chars produced by compact.

Trimming policy when prompt exceeds `context-max-chars`:

1. drop oldest recent turns first,
2. then shrink summary,
3. then shrink current input only as last resort.

This preserves recency and current intent while honoring hard budget.

## Compact (Manual)

Endpoint: `POST /v1/threads/{threadId}/compact`

Behavior:
- creates an internal turn (`is_internal=1`);
- builds a compact prompt from current summary + recent turns + summarization instruction;
- asks configured provider to generate updated summary;
- trims summary to `maxSummaryChars` from request (or `--compact-max-chars`);
- writes summary back to `threads.summary`.

Internal compact turns are hidden in `GET /v1/threads/{threadId}/history` by default.
Use `includeInternal=true` to view them.

## Restart Recovery

No provider in-memory state is required for continuity.

After restart:
- server reads durable `threads.summary` and recent non-internal turns from SQLite;
- next turn rebuilds injected prompt with the same strategy;
- continuity is preserved even with a new provider process/session.

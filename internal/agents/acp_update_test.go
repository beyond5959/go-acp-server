package agents

import (
	"encoding/json"
	"testing"
)

func TestParseACPUpdateMessageChunk(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "agent_message_chunk",
			"content": {
				"type": "text",
				"text": "hello"
			}
		}
	}`)

	update, err := ParseACPUpdate(raw)
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeMessageChunk {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeMessageChunk)
	}
	if update.Delta != "hello" {
		t.Fatalf("update.Delta = %q, want %q", update.Delta, "hello")
	}
	if len(update.PlanEntries) != 0 {
		t.Fatalf("len(update.PlanEntries) = %d, want 0", len(update.PlanEntries))
	}
}

func TestParseACPUpdatePlan(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "plan",
			"entries": [
				{"content": " Inspect files ", "status": " in_progress ", "priority": " high "},
				{"content": "   ", "status": "pending", "priority": "low"},
				{"content": "Write patch", "status": "pending", "priority": "medium"}
			]
		}
	}`)

	update, err := ParseACPUpdate(raw)
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypePlan {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypePlan)
	}
	if len(update.PlanEntries) != 2 {
		t.Fatalf("len(update.PlanEntries) = %d, want 2", len(update.PlanEntries))
	}
	if got := update.PlanEntries[0]; got.Content != "Inspect files" || got.Status != "in_progress" || got.Priority != "high" {
		t.Fatalf("first plan entry = %+v, want trimmed values", got)
	}
}

func TestParseACPUpdateLegacyDelta(t *testing.T) {
	t.Parallel()

	update, err := ParseACPUpdate(json.RawMessage(`{"delta":"legacy"}`))
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeMessageChunk {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeMessageChunk)
	}
	if update.Delta != "legacy" {
		t.Fatalf("update.Delta = %q, want %q", update.Delta, "legacy")
	}
}

func TestParseACPUpdatePlanKeepsEmptyReplacement(t *testing.T) {
	t.Parallel()

	update, err := ParseACPUpdate(json.RawMessage(`{"update":{"sessionUpdate":"plan","entries":[]}}`))
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypePlan {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypePlan)
	}
	if update.PlanEntries != nil {
		t.Fatalf("update.PlanEntries = %#v, want nil empty replacement", update.PlanEntries)
	}
}

func TestParseACPUpdateUserMessageChunk(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"timestamp": "2026-03-12T08:00:00Z",
		"_meta": {"messageId": "msg-user-1"},
		"update": {
			"sessionUpdate": "user_message_chunk",
			"content": {
				"type": "text",
				"text": "hello from user"
			}
		}
	}`)

	update, err := ParseACPUpdate(raw)
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeUserMessageChunk {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeUserMessageChunk)
	}
	if update.Role != "user" {
		t.Fatalf("update.Role = %q, want %q", update.Role, "user")
	}
	if update.Delta != "hello from user" {
		t.Fatalf("update.Delta = %q, want %q", update.Delta, "hello from user")
	}
	if update.MessageID != "msg-user-1" {
		t.Fatalf("update.MessageID = %q, want %q", update.MessageID, "msg-user-1")
	}
	if update.Timestamp != "2026-03-12T08:00:00Z" {
		t.Fatalf("update.Timestamp = %q, want %q", update.Timestamp, "2026-03-12T08:00:00Z")
	}
}

func TestParseACPUpdateAgentThoughtChunkAlias(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "agent_thought_chunk",
			"content": {
				"type": "text",
				"text": "thinking"
			}
		}
	}`)

	update, err := ParseACPUpdate(raw)
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeThoughtMessageChunk {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeThoughtMessageChunk)
	}
	if update.Delta != "thinking" {
		t.Fatalf("update.Delta = %q, want %q", update.Delta, "thinking")
	}
}

func TestParseACPUpdateIgnoresNonTextToolCallPayload(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "tool_call_update",
			"content": [
				{"type": "content", "content": {"type": "text", "text": "tool output"}}
			]
		}
	}`)

	update, err := ParseACPUpdate(raw)
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != "tool_call_update" {
		t.Fatalf("update.Type = %q, want %q", update.Type, "tool_call_update")
	}
	if update.Delta != "" {
		t.Fatalf("update.Delta = %q, want empty", update.Delta)
	}
}

func TestParseACPUpdateAvailableCommands(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "available_commands_update",
			"availableCommands": [
				{
					"name": "plan",
					"description": " Toggle plan mode ",
					"input": {
						"placeholder": "on|off|view|clear"
					}
				},
				{
					"name": "clear",
					"description": "Clear the context"
				},
				{
					"name": "plan",
					"description": "duplicate should be ignored"
				}
			]
		}
	}`)

	update, err := ParseACPUpdate(raw)
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeAvailableCommands {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeAvailableCommands)
	}
	if got, want := len(update.Commands), 2; got != want {
		t.Fatalf("len(update.Commands) = %d, want %d", got, want)
	}
	if got := update.Commands[0]; got.Name != "plan" || got.Description != "Toggle plan mode" || got.InputHint != "on|off|view|clear" {
		t.Fatalf("first command = %+v, want normalized plan command", got)
	}
	if got := update.Commands[1]; got.Name != "clear" || got.Description != "Clear the context" {
		t.Fatalf("second command = %+v, want clear command", got)
	}
}

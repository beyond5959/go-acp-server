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

func TestParseACPUpdateSessionInfoTitle(t *testing.T) {
	t.Parallel()

	update, err := ParseACPUpdate(json.RawMessage(`{
		"sessionId": " sess_123 ",
		"update": {
			"sessionUpdate": "session_info_update",
			"title": "  Implement auth  "
		}
	}`))
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeSessionInfo {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeSessionInfo)
	}
	if update.SessionInfo == nil {
		t.Fatal("update.SessionInfo = nil, want populated session metadata")
	}
	if got := update.SessionInfo.SessionID; got != "sess_123" {
		t.Fatalf("update.SessionInfo.SessionID = %q, want %q", got, "sess_123")
	}
	if got := update.SessionInfo.Title; got != "Implement auth" {
		t.Fatalf("update.SessionInfo.Title = %q, want %q", got, "Implement auth")
	}
	if !update.SessionInfo.HasTitle {
		t.Fatal("update.SessionInfo.HasTitle = false, want true")
	}
}

func TestParseACPUpdateSessionInfoNullTitleIgnored(t *testing.T) {
	t.Parallel()

	update, err := ParseACPUpdate(json.RawMessage(`{
		"sessionId": "sess_123",
		"update": {
			"sessionUpdate": "session_info_update",
			"title": null
		}
	}`))
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeSessionInfo {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeSessionInfo)
	}
	if update.SessionInfo != nil {
		t.Fatalf("update.SessionInfo = %#v, want nil when title is null", update.SessionInfo)
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

func TestParseACPUpdateAgentMessageChunkKeepsNonTextContent(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "agent_message_chunk",
			"content": {
				"type": "image",
				"mimeType": "image/png",
				"data": "aGVsbG8="
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
	if update.Delta != "" {
		t.Fatalf("update.Delta = %q, want empty", update.Delta)
	}
	if update.MessageContent == nil {
		t.Fatal("update.MessageContent = nil, want structured content")
	}
	if !update.MessageContent.HasContent {
		t.Fatal("update.MessageContent.HasContent = false, want true")
	}
	var payload map[string]any
	if err := json.Unmarshal(update.MessageContent.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(update.MessageContent.Content): %v", err)
	}
	if got, _ := payload["type"].(string); got != "image" {
		t.Fatalf("payload.type = %q, want %q", got, "image")
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

func TestParseACPUpdateToolCall(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "tool_call",
			"toolCallId": "call-1",
			"title": "Read file",
			"kind": "read_file",
			"status": "running",
			"content": [
				{"type": "content", "content": {"type": "text", "text": "opening file"}}
			],
			"locations": [
				{"path": "/tmp/demo.txt"}
			],
			"rawInput": {"path": "/tmp/demo.txt"}
		}
	}`)

	update, err := ParseACPUpdate(raw)
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeToolCall {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeToolCall)
	}
	if update.ToolCall == nil {
		t.Fatal("update.ToolCall is nil, want populated tool call")
	}
	if got := update.ToolCall.ToolCallID; got != "call-1" {
		t.Fatalf("update.ToolCall.ToolCallID = %q, want %q", got, "call-1")
	}
	if got := update.ToolCall.Title; got != "Read file" {
		t.Fatalf("update.ToolCall.Title = %q, want %q", got, "Read file")
	}
	if got := update.ToolCall.Kind; got != "read_file" {
		t.Fatalf("update.ToolCall.Kind = %q, want %q", got, "read_file")
	}
	if got := update.ToolCall.Status; got != "running" {
		t.Fatalf("update.ToolCall.Status = %q, want %q", got, "running")
	}
	if !update.ToolCall.HasContent || string(update.ToolCall.Content) == "" {
		t.Fatalf("update.ToolCall.Content = %q, want raw JSON content", string(update.ToolCall.Content))
	}
	if !update.ToolCall.HasLocations || string(update.ToolCall.Locations) == "" {
		t.Fatalf("update.ToolCall.Locations = %q, want raw JSON locations", string(update.ToolCall.Locations))
	}
	if !update.ToolCall.HasRawInput || string(update.ToolCall.RawInput) == "" {
		t.Fatalf("update.ToolCall.RawInput = %q, want raw JSON input", string(update.ToolCall.RawInput))
	}
}

func TestParseACPUpdateToolCallUpdateKeepsExplicitClears(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "tool_call_update",
			"toolCallId": "call-1",
			"title": "",
			"status": "completed",
			"content": [],
			"rawOutput": {"ok": true}
		}
	}`)

	update, err := ParseACPUpdate(raw)
	if err != nil {
		t.Fatalf("ParseACPUpdate() error = %v", err)
	}
	if update.Type != ACPUpdateTypeToolCallUpdate {
		t.Fatalf("update.Type = %q, want %q", update.Type, ACPUpdateTypeToolCallUpdate)
	}
	if update.ToolCall == nil {
		t.Fatal("update.ToolCall is nil, want populated tool call update")
	}
	if !update.ToolCall.HasTitle {
		t.Fatal("update.ToolCall.HasTitle = false, want true for explicit empty title")
	}
	if got := update.ToolCall.Title; got != "" {
		t.Fatalf("update.ToolCall.Title = %q, want empty explicit clear", got)
	}
	if !update.ToolCall.HasContent || string(update.ToolCall.Content) != "[]" {
		t.Fatalf("update.ToolCall.Content = %q, want %q", string(update.ToolCall.Content), "[]")
	}
	if !update.ToolCall.HasRawOutput {
		t.Fatal("update.ToolCall.HasRawOutput = false, want true")
	}
	var rawOutput map[string]bool
	if err := json.Unmarshal(update.ToolCall.RawOutput, &rawOutput); err != nil {
		t.Fatalf("json.Unmarshal(update.ToolCall.RawOutput): %v", err)
	}
	if !rawOutput["ok"] {
		t.Fatalf("rawOutput = %#v, want ok=true", rawOutput)
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

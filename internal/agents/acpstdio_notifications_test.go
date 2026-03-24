package agents

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewACPNotificationHandlerRoutesThoughtChunksToReasoningHandler(t *testing.T) {
	t.Parallel()

	var answer string
	var reasoning string
	ctx := WithReasoningHandler(context.Background(), func(ctx context.Context, delta string) error {
		_ = ctx
		reasoning += delta
		return nil
	})

	handler, markPromptStarted := NewACPNotificationHandler(ctx, func(delta string) error {
		answer += delta
		return nil
	})
	markPromptStarted()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "agent_thought_chunk",
			"content": {
				"type": "text",
				"text": "thinking"
			}
		}
	}`)
	if err := handler("session/update", raw); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	if answer != "" {
		t.Fatalf("answer = %q, want empty", answer)
	}
	if reasoning != "thinking" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "thinking")
	}
}

func TestNewACPNotificationHandlerRoutesToolCallsToToolCallHandler(t *testing.T) {
	t.Parallel()

	var received ACPToolCall
	ctx := WithToolCallHandler(context.Background(), func(ctx context.Context, event ACPToolCall) error {
		_ = ctx
		received = CloneACPToolCall(event)
		return nil
	})

	handler, markPromptStarted := NewACPNotificationHandler(ctx, func(delta string) error {
		_ = delta
		return nil
	})
	markPromptStarted()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "tool_call_update",
			"toolCallId": "tool-1",
			"status": "completed",
			"rawOutput": {"ok": true}
		}
	}`)
	if err := handler("session/update", raw); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	if got := received.Type; got != ACPUpdateTypeToolCallUpdate {
		t.Fatalf("received.Type = %q, want %q", got, ACPUpdateTypeToolCallUpdate)
	}
	if got := received.ToolCallID; got != "tool-1" {
		t.Fatalf("received.ToolCallID = %q, want %q", got, "tool-1")
	}
	if got := received.Status; got != "completed" {
		t.Fatalf("received.Status = %q, want %q", got, "completed")
	}
	if !received.HasRawOutput {
		t.Fatal("received.HasRawOutput = false, want true")
	}
	var rawOutput map[string]bool
	if err := json.Unmarshal(received.RawOutput, &rawOutput); err != nil {
		t.Fatalf("json.Unmarshal(received.RawOutput): %v", err)
	}
	if !rawOutput["ok"] {
		t.Fatalf("rawOutput = %#v, want ok=true", rawOutput)
	}
}

func TestNewACPNotificationHandlerRoutesStructuredMessageContent(t *testing.T) {
	t.Parallel()

	var received ACPMessageContent
	ctx := WithMessageContentHandler(context.Background(), func(ctx context.Context, event ACPMessageContent) error {
		_ = ctx
		received = CloneACPMessageContent(event)
		return nil
	})

	handler, markPromptStarted := NewACPNotificationHandler(ctx, func(delta string) error {
		_ = delta
		return nil
	})
	markPromptStarted()

	raw := json.RawMessage(`{
		"update": {
			"sessionUpdate": "agent_message_chunk",
			"content": {
				"type": "resource",
				"resource": {
					"uri": "file:///tmp/demo.txt",
					"mimeType": "text/plain",
					"text": "hello"
				}
			}
		}
	}`)
	if err := handler("session/update", raw); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	if !received.HasContent {
		t.Fatal("received.HasContent = false, want true")
	}
	var payload map[string]any
	if err := json.Unmarshal(received.Content, &payload); err != nil {
		t.Fatalf("json.Unmarshal(received.Content): %v", err)
	}
	if got, _ := payload["type"].(string); got != "resource" {
		t.Fatalf("payload.type = %q, want %q", got, "resource")
	}
}

func TestNewACPNotificationHandlerRoutesSessionInfoBeforePromptStart(t *testing.T) {
	t.Parallel()

	var received SessionInfoUpdate
	ctx := WithSessionInfoHandler(context.Background(), func(ctx context.Context, update SessionInfoUpdate) error {
		_ = ctx
		received = update
		return nil
	})

	handler, _ := NewACPNotificationHandler(ctx, func(delta string) error {
		_ = delta
		return nil
	})

	raw := json.RawMessage(`{
		"sessionId": "sess_42",
		"update": {
			"sessionUpdate": "session_info_update",
			"title": "Runtime title"
		}
	}`)
	if err := handler("session/update", raw); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	if got := received.SessionID; got != "sess_42" {
		t.Fatalf("received.SessionID = %q, want %q", got, "sess_42")
	}
	if got := received.Title; got != "Runtime title" {
		t.Fatalf("received.Title = %q, want %q", got, "Runtime title")
	}
	if !received.HasTitle {
		t.Fatal("received.HasTitle = false, want true")
	}
}

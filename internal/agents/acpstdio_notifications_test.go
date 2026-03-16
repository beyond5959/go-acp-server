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

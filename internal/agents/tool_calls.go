package agents

import (
	"context"
	"encoding/json"
	"strings"
)

const (
	// ACPUpdateTypeToolCall reports one new tool call emitted by the agent.
	ACPUpdateTypeToolCall = "tool_call"
	// ACPUpdateTypeToolCallUpdate reports one incremental update to an existing tool call.
	ACPUpdateTypeToolCallUpdate = "tool_call_update"
)

// ACPToolCall is one normalized ACP tool-call update payload.
type ACPToolCall struct {
	Type       string
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
	// Delta carries the plain-text streaming output for in-progress tool calls
	// (e.g. command execution stdout/stderr chunks). Extracted from the nested
	// content {"type":"text","text":"..."} emitted by the acp-adapter.
	Delta     string          `json:"delta,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Locations json.RawMessage `json:"locations,omitempty"`
	RawInput  json.RawMessage `json:"rawInput,omitempty"`
	RawOutput json.RawMessage `json:"rawOutput,omitempty"`

	HasTitle     bool `json:"-"`
	HasKind      bool `json:"-"`
	HasStatus    bool `json:"-"`
	HasDelta     bool `json:"-"`
	HasContent   bool `json:"-"`
	HasLocations bool `json:"-"`
	HasRawInput  bool `json:"-"`
	HasRawOutput bool `json:"-"`
}

// EventPayload returns one JSON-serializable SSE/history payload for the tool call.
func (event ACPToolCall) EventPayload(turnID string) map[string]any {
	payload := map[string]any{
		"turnId":     strings.TrimSpace(turnID),
		"toolCallId": strings.TrimSpace(event.ToolCallID),
	}
	if event.HasTitle {
		payload["title"] = strings.TrimSpace(event.Title)
	}
	if event.HasKind {
		payload["kind"] = strings.TrimSpace(event.Kind)
	}
	if event.HasStatus {
		payload["status"] = strings.TrimSpace(event.Status)
	}
	if event.HasDelta {
		payload["delta"] = strings.TrimSpace(event.Delta)
	}
	if event.HasContent {
		payload["content"] = cloneACPToolCallJSON(event.Content)
	}
	if event.HasLocations {
		payload["locations"] = cloneACPToolCallJSON(event.Locations)
	}
	if event.HasRawInput {
		payload["rawInput"] = cloneACPToolCallJSON(event.RawInput)
	}
	if event.HasRawOutput {
		payload["rawOutput"] = cloneACPToolCallJSON(event.RawOutput)
	}
	return payload
}

// CloneACPToolCall returns a deep copy of one tool-call event.
func CloneACPToolCall(event ACPToolCall) ACPToolCall {
	return ACPToolCall{
		Type:         strings.TrimSpace(event.Type),
		ToolCallID:   strings.TrimSpace(event.ToolCallID),
		Title:        strings.TrimSpace(event.Title),
		Kind:         strings.TrimSpace(event.Kind),
		Status:       strings.TrimSpace(event.Status),
		Delta:        strings.TrimSpace(event.Delta),
		Content:      cloneACPToolCallJSON(event.Content),
		Locations:    cloneACPToolCallJSON(event.Locations),
		RawInput:     cloneACPToolCallJSON(event.RawInput),
		RawOutput:    cloneACPToolCallJSON(event.RawOutput),
		HasTitle:     event.HasTitle,
		HasKind:      event.HasKind,
		HasStatus:    event.HasStatus,
		HasDelta:     event.HasDelta,
		HasContent:   event.HasContent,
		HasLocations: event.HasLocations,
		HasRawInput:  event.HasRawInput,
		HasRawOutput: event.HasRawOutput,
	}
}

func cloneACPToolCallJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// ToolCallHandler receives one ACP tool-call event for the active turn.
type ToolCallHandler func(ctx context.Context, event ACPToolCall) error

type toolCallHandlerContextKey struct{}

// WithToolCallHandler binds one per-turn tool-call callback to context.
func WithToolCallHandler(ctx context.Context, handler ToolCallHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, toolCallHandlerContextKey{}, handler)
}

// ToolCallHandlerFromContext gets tool-call callback from context, if present.
func ToolCallHandlerFromContext(ctx context.Context) (ToolCallHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(toolCallHandlerContextKey{}).(ToolCallHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

// NotifyToolCall reports one ACP tool-call event to the active callback, if any.
func NotifyToolCall(ctx context.Context, event ACPToolCall) error {
	event = CloneACPToolCall(event)
	if event.Type == "" || event.ToolCallID == "" {
		return nil
	}
	handler, ok := ToolCallHandlerFromContext(ctx)
	if !ok {
		return nil
	}
	return handler(ctx, event)
}

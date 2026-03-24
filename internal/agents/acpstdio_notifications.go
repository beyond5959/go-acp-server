package agents

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/beyond5959/ngent/internal/agents/acpstdio"
)

// NewACPNotificationHandler builds one shared ACP session/update handler and
// returns a callback that marks the prompt as started.
func NewACPNotificationHandler(
	ctx context.Context,
	onDelta func(delta string) error,
) (func(method string, params json.RawMessage) error, func()) {
	var promptStarted atomic.Bool

	handler := func(method string, params json.RawMessage) error {
		if method != "session/update" || len(params) == 0 {
			return nil
		}
		update, err := ParseACPUpdate(params)
		if err != nil {
			return nil
		}
		if update.Type == ACPUpdateTypeAvailableCommands {
			return NotifySlashCommands(ctx, update.Commands)
		}
		if update.Type == ACPUpdateTypeSessionInfo && update.SessionInfo != nil {
			return NotifySessionInfoUpdate(ctx, *update.SessionInfo)
		}
		if !promptStarted.Load() {
			return nil
		}
		switch update.Type {
		case ACPUpdateTypeMessageChunk:
			if update.Delta != "" && onDelta != nil {
				return onDelta(update.Delta)
			}
			if update.MessageContent != nil {
				return NotifyMessageContent(ctx, *update.MessageContent)
			}
		case ACPUpdateTypeThoughtMessageChunk:
			return NotifyReasoningDelta(ctx, update.Delta)
		case ACPUpdateTypePlan:
			if handler, ok := PlanHandlerFromContext(ctx); ok {
				return handler(ctx, update.PlanEntries)
			}
		case ACPUpdateTypeToolCall, ACPUpdateTypeToolCallUpdate:
			if update.ToolCall != nil {
				return NotifyToolCall(ctx, *update.ToolCall)
			}
		}
		return nil
	}

	return handler, func() {
		promptStarted.Store(true)
	}
}

// InstallACPStdioNotificationHandler registers one shared ACP stdio session/update
// handler and returns a callback that marks the prompt as started.
func InstallACPStdioNotificationHandler(
	conn *acpstdio.Conn,
	ctx context.Context,
	onDelta func(delta string) error,
) func() {
	handler, markPromptStarted := NewACPNotificationHandler(ctx, onDelta)
	if conn != nil {
		conn.SetNotificationHandler(func(msg acpstdio.Message) error {
			return handler(msg.Method, msg.Params)
		})
	}
	return markPromptStarted
}

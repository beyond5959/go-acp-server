package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// ACPUpdateTypeMessageChunk streams assistant text deltas.
	ACPUpdateTypeMessageChunk = "agent_message_chunk"
	// ACPUpdateTypeAgentMessageChunk streams assistant text deltas.
	ACPUpdateTypeAgentMessageChunk = ACPUpdateTypeMessageChunk
	// ACPUpdateTypeUserMessageChunk replays user text during session/load.
	ACPUpdateTypeUserMessageChunk = "user_message_chunk"
	// ACPUpdateTypeThoughtMessageChunk streams hidden reasoning deltas.
	ACPUpdateTypeThoughtMessageChunk = "thought_message_chunk"
	// acpUpdateTypeAgentThoughtChunk is the real kimi-cli thought chunk name.
	acpUpdateTypeAgentThoughtChunk = "agent_thought_chunk"
	// ACPUpdateTypePlan replaces the current agent plan entries.
	ACPUpdateTypePlan = "plan"
	// ACPUpdateTypeSessionInfo updates ACP session metadata such as title.
	ACPUpdateTypeSessionInfo = "session_info_update"
	// ACPUpdateTypeAvailableCommands replaces the current slash-command list.
	ACPUpdateTypeAvailableCommands = "available_commands_update"
)

// PlanEntry is one ACP plan entry shown to the user.
type PlanEntry struct {
	Content  string `json:"content"`
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
}

// ACPUpdate is one normalized ACP session/update payload.
type ACPUpdate struct {
	Type           string
	Role           string
	Delta          string
	MessageID      string
	Timestamp      string
	PlanEntries    []PlanEntry
	Commands       []SlashCommand
	SessionInfo    *SessionInfoUpdate
	MessageContent *ACPMessageContent
	ToolCall       *ACPToolCall
}

// ParseACPUpdate normalizes provider-specific session/update payloads.
func ParseACPUpdate(raw json.RawMessage) (ACPUpdate, error) {
	if len(raw) == 0 {
		return ACPUpdate{}, nil
	}

	var payload struct {
		SessionID string         `json:"sessionId"`
		Delta     string         `json:"delta"`
		MessageID string         `json:"messageId"`
		Timestamp string         `json:"timestamp"`
		Meta      map[string]any `json:"_meta"`
		Update    struct {
			SessionUpdate     string            `json:"sessionUpdate"`
			MessageID         string            `json:"messageId"`
			Timestamp         string            `json:"timestamp"`
			Meta              map[string]any    `json:"_meta"`
			ToolCallID        string            `json:"toolCallId"`
			Title             *string           `json:"title"`
			Kind              *string           `json:"kind"`
			Status            *string           `json:"status"`
			Content           json.RawMessage   `json:"content"`
			Locations         json.RawMessage   `json:"locations"`
			RawInput          json.RawMessage   `json:"rawInput"`
			RawOutput         json.RawMessage   `json:"rawOutput"`
			Entries           []PlanEntry       `json:"entries"`
			AvailableCommands []json.RawMessage `json:"availableCommands"`
		} `json:"update"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ACPUpdate{}, fmt.Errorf("decode ACP session/update payload: %w", err)
	}

	switch strings.TrimSpace(payload.Update.SessionUpdate) {
	case "":
		if payload.Delta == "" {
			return ACPUpdate{}, nil
		}
		return ACPUpdate{
			Type:  ACPUpdateTypeMessageChunk,
			Role:  "assistant",
			Delta: payload.Delta,
			MessageID: normalizeACPUpdateString(
				payload.Update.MessageID,
				payload.MessageID,
				acpUpdateMetaString(payload.Update.Meta, "messageId"),
				acpUpdateMetaString(payload.Meta, "messageId"),
			),
			Timestamp: normalizeACPUpdateString(
				payload.Update.Timestamp,
				payload.Timestamp,
				acpUpdateMetaString(payload.Update.Meta, "timestamp"),
				acpUpdateMetaString(payload.Meta, "timestamp"),
			),
		}, nil
	case ACPUpdateTypeAgentMessageChunk, ACPUpdateTypeUserMessageChunk, ACPUpdateTypeThoughtMessageChunk, acpUpdateTypeAgentThoughtChunk:
		delta, isText, rawContent, hasContent, err := parseACPUpdateMessageContent(payload.Update.Content)
		if err != nil {
			return ACPUpdate{}, err
		}
		normalizedType := normalizeACPUpdateType(payload.Update.SessionUpdate)
		role := ""
		switch normalizedType {
		case ACPUpdateTypeAgentMessageChunk:
			role = "assistant"
		case ACPUpdateTypeUserMessageChunk:
			role = "user"
		}
		update := ACPUpdate{
			Type:  normalizedType,
			Role:  role,
			Delta: delta,
			MessageID: normalizeACPUpdateString(
				payload.Update.MessageID,
				payload.MessageID,
				acpUpdateMetaString(payload.Update.Meta, "messageId"),
				acpUpdateMetaString(payload.Meta, "messageId"),
			),
			Timestamp: normalizeACPUpdateString(
				payload.Update.Timestamp,
				payload.Timestamp,
				acpUpdateMetaString(payload.Update.Meta, "timestamp"),
				acpUpdateMetaString(payload.Meta, "timestamp"),
			),
		}
		if !isText && hasContent && normalizedType == ACPUpdateTypeAgentMessageChunk {
			update.MessageContent = &ACPMessageContent{
				Content:    rawContent,
				HasContent: true,
			}
		}
		return update, nil
	case ACPUpdateTypePlan:
		return ACPUpdate{
			Type:        ACPUpdateTypePlan,
			PlanEntries: normalizePlanEntries(payload.Update.Entries),
		}, nil
	case ACPUpdateTypeSessionInfo:
		if payload.Update.Title == nil {
			return ACPUpdate{
				Type: ACPUpdateTypeSessionInfo,
			}, nil
		}
		return ACPUpdate{
			Type: ACPUpdateTypeSessionInfo,
			SessionInfo: &SessionInfoUpdate{
				SessionID: strings.TrimSpace(payload.SessionID),
				Title:     strings.TrimSpace(*payload.Update.Title),
				HasTitle:  true,
			},
		}, nil
	case ACPUpdateTypeAvailableCommands:
		return ACPUpdate{
			Type:     ACPUpdateTypeAvailableCommands,
			Commands: parseACPUpdateSlashCommands(payload.Update.AvailableCommands),
		}, nil
	case ACPUpdateTypeToolCall, ACPUpdateTypeToolCallUpdate:
		title, hasTitle := normalizeACPOptionalString(payload.Update.Title)
		kind, hasKind := normalizeACPOptionalString(payload.Update.Kind)
		status, hasStatus := normalizeACPOptionalString(payload.Update.Status)
		toolCall := ACPToolCall{
			Type:         normalizeACPUpdateType(payload.Update.SessionUpdate),
			ToolCallID:   strings.TrimSpace(payload.Update.ToolCallID),
			Title:        title,
			Kind:         kind,
			Status:       status,
			Content:      cloneACPUpdateJSON(payload.Update.Content),
			Locations:    cloneACPUpdateJSON(payload.Update.Locations),
			RawInput:     cloneACPUpdateJSON(payload.Update.RawInput),
			RawOutput:    cloneACPUpdateJSON(payload.Update.RawOutput),
			HasTitle:     hasTitle,
			HasKind:      hasKind,
			HasStatus:    hasStatus,
			HasContent:   hasACPUpdateJSON(payload.Update.Content),
			HasLocations: hasACPUpdateJSON(payload.Update.Locations),
			HasRawInput:  hasACPUpdateJSON(payload.Update.RawInput),
			HasRawOutput: hasACPUpdateJSON(payload.Update.RawOutput),
		}
		return ACPUpdate{
			Type:     toolCall.Type,
			ToolCall: &toolCall,
		}, nil
	default:
		return ACPUpdate{Type: normalizeACPUpdateType(payload.Update.SessionUpdate)}, nil
	}
}

func parseACPUpdateSlashCommands(rawCommands []json.RawMessage) []SlashCommand {
	if len(rawCommands) == 0 {
		return nil
	}

	commands := make([]SlashCommand, 0, len(rawCommands))
	for _, raw := range rawCommands {
		raw = json.RawMessage(strings.TrimSpace(string(raw)))
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}

		var payload struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputHint   string `json:"inputHint"`
			Input       struct {
				Hint        string `json:"hint"`
				Placeholder string `json:"placeholder"`
			} `json:"input"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			continue
		}

		inputHint := strings.TrimSpace(payload.InputHint)
		if inputHint == "" {
			inputHint = strings.TrimSpace(payload.Input.Hint)
		}
		if inputHint == "" {
			inputHint = strings.TrimSpace(payload.Input.Placeholder)
		}

		commands = append(commands, SlashCommand{
			Name:        payload.Name,
			Description: payload.Description,
			InputHint:   inputHint,
		})
	}

	return CloneSlashCommands(commands)
}

func parseACPUpdateMessageContent(raw json.RawMessage) (string, bool, json.RawMessage, bool, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil, false, nil
	}

	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		// Providers can emit non-text payloads with a schema that differs from
		// standard text chunks. Preserve those raw blocks instead of aborting
		// the whole turn or transcript replay.
		return "", false, cloneACPUpdateJSON(raw), true, nil
	}

	contentType, _ := record["type"].(string)
	text, hasText := record["text"].(string)
	contentType = strings.TrimSpace(contentType)
	if contentType == "text" || (contentType == "" && hasText) {
		return text, true, cloneACPUpdateJSON(raw), true, nil
	}
	return "", false, cloneACPUpdateJSON(raw), true, nil
}

func acpUpdateMetaString(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, _ := values[key]
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func normalizeACPUpdateString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeACPOptionalString(value *string) (string, bool) {
	if value == nil {
		return "", false
	}
	return strings.TrimSpace(*value), true
}

func hasACPUpdateJSON(raw json.RawMessage) bool {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	return len(raw) != 0
}

func cloneACPUpdateJSON(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func normalizeACPUpdateType(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == acpUpdateTypeAgentThoughtChunk {
		return ACPUpdateTypeThoughtMessageChunk
	}
	return raw
}

// ClonePlanEntries returns a trimmed deep copy of the provided entries.
func ClonePlanEntries(entries []PlanEntry) []PlanEntry {
	return normalizePlanEntries(entries)
}

func normalizePlanEntries(entries []PlanEntry) []PlanEntry {
	if len(entries) == 0 {
		return nil
	}

	normalized := make([]PlanEntry, 0, len(entries))
	for _, entry := range entries {
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		normalized = append(normalized, PlanEntry{
			Content:  content,
			Status:   strings.TrimSpace(entry.Status),
			Priority: strings.TrimSpace(entry.Priority),
		})
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// PlanHandler receives ACP plan replacements for the active turn.
type PlanHandler func(ctx context.Context, entries []PlanEntry) error

type planHandlerContextKey struct{}

// WithPlanHandler binds one per-turn plan callback to context.
func WithPlanHandler(ctx context.Context, handler PlanHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, planHandlerContextKey{}, handler)
}

// PlanHandlerFromContext gets plan callback from context, if present.
func PlanHandlerFromContext(ctx context.Context) (PlanHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(planHandlerContextKey{}).(PlanHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

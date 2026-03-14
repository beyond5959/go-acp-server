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
	Type        string
	Role        string
	Delta       string
	MessageID   string
	Timestamp   string
	PlanEntries []PlanEntry
	Commands    []SlashCommand
}

// ParseACPUpdate normalizes provider-specific session/update payloads.
func ParseACPUpdate(raw json.RawMessage) (ACPUpdate, error) {
	if len(raw) == 0 {
		return ACPUpdate{}, nil
	}

	var payload struct {
		Delta     string         `json:"delta"`
		MessageID string         `json:"messageId"`
		Timestamp string         `json:"timestamp"`
		Meta      map[string]any `json:"_meta"`
		Update    struct {
			SessionUpdate     string            `json:"sessionUpdate"`
			MessageID         string            `json:"messageId"`
			Timestamp         string            `json:"timestamp"`
			Meta              map[string]any    `json:"_meta"`
			Content           json.RawMessage   `json:"content"`
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
		content, ok, err := parseACPUpdateTextContent(payload.Update.Content)
		if err != nil {
			return ACPUpdate{}, err
		}
		if !ok {
			return ACPUpdate{Type: normalizeACPUpdateType(payload.Update.SessionUpdate)}, nil
		}
		role := ""
		switch normalizeACPUpdateType(payload.Update.SessionUpdate) {
		case ACPUpdateTypeAgentMessageChunk:
			role = "assistant"
		case ACPUpdateTypeUserMessageChunk:
			role = "user"
		}
		return ACPUpdate{
			Type:  normalizeACPUpdateType(payload.Update.SessionUpdate),
			Role:  role,
			Delta: content,
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
	case ACPUpdateTypePlan:
		return ACPUpdate{
			Type:        ACPUpdateTypePlan,
			PlanEntries: normalizePlanEntries(payload.Update.Entries),
		}, nil
	case ACPUpdateTypeAvailableCommands:
		return ACPUpdate{
			Type:     ACPUpdateTypeAvailableCommands,
			Commands: parseACPUpdateSlashCommands(payload.Update.AvailableCommands),
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

func parseACPUpdateTextContent(raw json.RawMessage) (string, bool, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}

	var content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &content); err != nil {
		// Providers like Qwen emit non-text updates such as tool_call_update
		// whose content is an array/object with a different schema. Those
		// updates should be ignored rather than aborting the whole replay.
		return "", false, nil
	}
	if contentType := strings.TrimSpace(content.Type); contentType != "" && contentType != "text" {
		return "", false, nil
	}
	return content.Text, true, nil
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

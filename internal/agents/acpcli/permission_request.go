package acpcli

import (
	"encoding/json"
	"strings"

	"github.com/beyond5959/ngent/internal/agents"
)

// PermissionRequestPayload is one normalized ACP session/request_permission payload.
type PermissionRequestPayload struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// PermissionToolCall describes the ACP tool-call preview attached to a permission request.
type PermissionToolCall struct {
	Title      string                  `json:"title"`
	Kind       string                  `json:"kind"`
	ToolCallID string                  `json:"toolCallId"`
	Content    []PermissionToolContent `json:"content"`
	Locations  []PermissionToolPath    `json:"locations"`
	RawInput   map[string]any          `json:"rawInput"`
}

// PermissionToolContent is one ACP permission-preview content block.
type PermissionToolContent struct {
	Type    string                 `json:"type"`
	Path    string                 `json:"path"`
	Command string                 `json:"command"`
	Content *PermissionTextContent `json:"content"`
	OldText string                 `json:"oldText"`
	NewText string                 `json:"newText"`
}

// PermissionTextContent is one embedded ACP text block.
type PermissionTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// PermissionToolPath is one path-like location attached to a permission request.
type PermissionToolPath struct {
	Path string `json:"path"`
}

// ParsePermissionRequestPayload decodes one ACP permission request payload.
func ParsePermissionRequestPayload(raw json.RawMessage) (PermissionRequestPayload, error) {
	var payload PermissionRequestPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return PermissionRequestPayload{}, err
	}
	return payload, nil
}

// ToAgentPermissionRequest converts one ACP permission payload into the shared agent model.
func (p PermissionRequestPayload) ToAgentPermissionRequest() agents.PermissionRequest {
	rawParams := map[string]any{
		"sessionId": strings.TrimSpace(p.SessionID),
	}
	if toolCallID := strings.TrimSpace(p.ToolCall.ToolCallID); toolCallID != "" {
		rawParams["toolCallId"] = toolCallID
	}
	if title := strings.TrimSpace(p.ToolCall.Title); title != "" {
		rawParams["title"] = title
	}
	if kind := strings.TrimSpace(p.ToolCall.Kind); kind != "" {
		rawParams["kind"] = kind
	}
	if path := strings.TrimSpace(p.firstPath()); path != "" {
		rawParams["path"] = path
	}

	return agents.PermissionRequest{
		Approval:  normalizePermissionApproval(p.ToolCall),
		Command:   normalizePermissionCommand(p.ToolCall),
		Options:   toAgentPermissionOptions(p.Options),
		RawParams: rawParams,
	}
}

func toAgentPermissionOptions(options []PermissionOption) []agents.PermissionOption {
	if len(options) == 0 {
		return nil
	}
	converted := make([]agents.PermissionOption, 0, len(options))
	for _, option := range options {
		optionID := strings.TrimSpace(option.OptionID)
		if optionID == "" {
			continue
		}
		converted = append(converted, agents.PermissionOption{
			OptionID: optionID,
			Name:     strings.TrimSpace(option.Name),
			Kind:     strings.TrimSpace(option.Kind),
		})
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

func (p PermissionRequestPayload) firstPath() string {
	for _, item := range p.ToolCall.Content {
		if path := strings.TrimSpace(item.Path); path != "" {
			return path
		}
	}
	for _, item := range p.ToolCall.Locations {
		if path := strings.TrimSpace(item.Path); path != "" {
			return path
		}
	}
	for _, key := range []string{"path", "filepath", "filePath", "parentDir", "dir", "directory"} {
		if value, ok := p.ToolCall.RawInput[key].(string); ok {
			if path := strings.TrimSpace(value); path != "" {
				return path
			}
		}
	}
	return ""
}

func normalizePermissionApproval(toolCall PermissionToolCall) string {
	title := strings.ToLower(strings.TrimSpace(toolCall.Title))
	kind := strings.ToLower(strings.TrimSpace(toolCall.Kind))

	for _, item := range toolCall.Content {
		if strings.EqualFold(strings.TrimSpace(item.Type), "diff") || strings.TrimSpace(item.Path) != "" {
			return "file"
		}
	}
	for _, item := range toolCall.Locations {
		if strings.TrimSpace(item.Path) != "" {
			return "file"
		}
	}
	if rawPath := permissionRawInputPath(toolCall.RawInput); rawPath != "" {
		return "file"
	}

	switch {
	case strings.Contains(title, "file"),
		strings.Contains(kind, "file"),
		strings.Contains(title, "directory"),
		strings.Contains(kind, "directory"),
		strings.Contains(title, "path"),
		strings.Contains(kind, "path"):
		return "file"
	case strings.Contains(title, "mcp"), strings.Contains(kind, "mcp"):
		return "mcp"
	case strings.Contains(title, "http"),
		strings.Contains(title, "fetch"),
		strings.Contains(title, "download"),
		strings.Contains(title, "network"),
		strings.Contains(kind, "network"):
		return "network"
	default:
		return "command"
	}
}

func normalizePermissionCommand(toolCall PermissionToolCall) string {
	if title := strings.TrimSpace(toolCall.Title); title != "" && !isGenericPermissionTitle(title) {
		return title
	}
	for _, item := range toolCall.Content {
		if path := strings.TrimSpace(item.Path); path != "" {
			return path
		}
		if command := strings.TrimSpace(item.Command); command != "" {
			return command
		}
		if item.Content != nil {
			if text := strings.TrimSpace(item.Content.Text); text != "" {
				return text
			}
		}
	}
	for _, item := range toolCall.Locations {
		if path := strings.TrimSpace(item.Path); path != "" {
			return path
		}
	}
	if path := permissionRawInputPath(toolCall.RawInput); path != "" {
		return path
	}
	if title := strings.TrimSpace(toolCall.Title); title != "" {
		return title
	}
	if kind := strings.TrimSpace(toolCall.Kind); kind != "" {
		return kind
	}
	return "permission request"
}

func permissionRawInputPath(rawInput map[string]any) string {
	for _, key := range []string{"path", "filepath", "filePath", "parentDir", "dir", "directory"} {
		if value, ok := rawInput[key].(string); ok {
			if path := strings.TrimSpace(value); path != "" {
				return path
			}
		}
	}
	return ""
}

func isGenericPermissionTitle(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "", "external_directory", "permission request":
		return true
	default:
		return false
	}
}

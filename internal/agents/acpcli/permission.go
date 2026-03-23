package acpcli

import (
	"encoding/json"
	"strings"
)

// PermissionOption describes one selectable permission option from an ACP provider.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind"`
}

// BuildSelectedPermissionResponse returns a selected permission outcome response.
func BuildSelectedPermissionResponse(optionID string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"outcome": map[string]any{
			"outcome":  "selected",
			"optionId": strings.TrimSpace(optionID),
		},
	})
}

// BuildCancelledPermissionResponse returns a cancelled permission outcome response.
func BuildCancelledPermissionResponse() (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"outcome": map[string]any{
			"outcome": "cancelled",
		},
	})
}

// PickPermissionOptionID returns the first matching optionId for the preferred kinds.
func PickPermissionOptionID(options []PermissionOption, preferredKinds ...string) string {
	for _, kind := range preferredKinds {
		normalizedKind := normalizePermissionKind(kind)
		if normalizedKind == "" {
			continue
		}
		for _, option := range options {
			if strings.TrimSpace(option.OptionID) == "" {
				continue
			}
			if normalizePermissionKind(option.Kind) == normalizedKind ||
				normalizePermissionKind(option.OptionID) == normalizedKind {
				return strings.TrimSpace(option.OptionID)
			}
		}
	}
	return ""
}

func normalizePermissionKind(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}

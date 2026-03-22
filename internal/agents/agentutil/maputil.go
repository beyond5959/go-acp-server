package agentutil

// MapString extracts a string value from a map[string]any by key.
// Returns "" if the key is absent or the value is not a string.
func MapString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key]
	text, _ := value.(string)
	return text
}

// MapStringSlice extracts a []string value from a map[string]any by key.
// Handles both []string and []any (the JSON-decoded form). Returns nil if
// the key is absent or the value is neither slice type.
func MapStringSlice(values map[string]any, key string) []string {
	if values == nil {
		return nil
	}
	value, ok := values[key]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

// MapInt extracts an int value from a map[string]any by key.
// Handles float64 (JSON numbers), int, and int64. Returns 0 if absent or
// the value is none of those types.
func MapInt(values map[string]any, key string) int {
	if values == nil {
		return 0
	}
	value, ok := values[key]
	if !ok {
		return 0
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

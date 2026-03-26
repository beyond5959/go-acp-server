package observability

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

var (
	acpDebugEnabled atomic.Bool
	acpDebugLogger  atomic.Pointer[Logger]
)

var (
	bearerTokenPattern    = regexp.MustCompile(`(?i)\bbearer\s+[^\s]+`)
	openAIKeyPattern      = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+\b`)
	sensitiveQueryPattern = regexp.MustCompile(`(?i)([?&](?:access[_-]?token|auth[_-]?token|api[_-]?key|token|secret|password)=)[^&\s]+`)
)

// ConfigureACPDebug toggles verbose ACP tracing on the shared logger.
func ConfigureACPDebug(logger *Logger, enabled bool) {
	if !enabled {
		acpDebugEnabled.Store(false)
		acpDebugLogger.Store(nil)
		return
	}
	if logger == nil {
		logger = NewLogger(LevelDebug)
	}
	acpDebugLogger.Store(logger)
	acpDebugEnabled.Store(true)
}

// LogACPMessage emits one sanitized ACP JSON-RPC message when debug tracing is enabled.
func LogACPMessage(component, direction string, msg any) {
	if !acpDebugEnabled.Load() {
		return
	}
	logger := acpDebugLogger.Load()
	if logger == nil {
		return
	}

	component = strings.TrimSpace(component)
	if component == "" {
		component = "acp"
	}
	direction = strings.TrimSpace(direction)
	if direction == "" {
		direction = "unknown"
	}

	normalized := sanitizeLogValue(normalizeLogValue(msg))
	attrs := []any{
		"component", component,
		"direction", direction,
		"rpc", normalized,
	}
	if summary, ok := normalized.(map[string]any); ok {
		if rpcType := detectRPCType(summary); rpcType != "" {
			attrs = append(attrs, "rpcType", rpcType)
		}
		if method, _ := summary["method"].(string); strings.TrimSpace(method) != "" {
			attrs = append(attrs, "method", method)
		}
		if id, ok := summary["id"]; ok {
			attrs = append(attrs, "id", formatLogValue(id))
		}
	}

	logger.Debug("acp.message", attrs...)
}

func normalizeLogValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return decodeRawJSON(v)
	case []byte:
		return decodeRawJSON(v)
	case map[string]any, []any, string, bool, float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return v
	case error:
		return redactString(v.Error())
	default:
		wire, err := json.Marshal(v)
		if err != nil {
			return redactString(fmt.Sprintf("%v", v))
		}
		return decodeRawJSON(wire)
	}
}

func decodeRawJSON(raw []byte) any {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return redactString(string(raw))
	}
	return decoded
}

func sanitizeLogValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if isSensitiveKey(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = sanitizeLogValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = sanitizeLogValue(v[i])
		}
		return out
	case string:
		return redactString(v)
	default:
		return v
	}
}

func detectRPCType(msg map[string]any) string {
	method, _ := msg["method"].(string)
	_, hasID := msg["id"]

	switch {
	case strings.TrimSpace(method) != "" && hasID:
		return "request"
	case strings.TrimSpace(method) != "" && !hasID:
		return "notification"
	case strings.TrimSpace(method) == "" && hasID:
		return "response"
	default:
		return ""
	}
}

func formatLogValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	replacer := strings.NewReplacer("-", "", "_", "", " ", "")
	normalized = replacer.Replace(normalized)
	if normalized == "" {
		return false
	}

	switch {
	case strings.Contains(normalized, "authorization"):
		return true
	case strings.Contains(normalized, "authtoken"):
		return true
	case strings.Contains(normalized, "apikey"):
		return true
	case strings.Contains(normalized, "token"):
		return true
	case strings.Contains(normalized, "secret"):
		return true
	case strings.Contains(normalized, "password"):
		return true
	case strings.Contains(normalized, "cookie"):
		return true
	default:
		return false
	}
}

func redactString(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	value = bearerTokenPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = openAIKeyPattern.ReplaceAllString(value, "[REDACTED]")
	value = sensitiveQueryPattern.ReplaceAllString(value, "${1}[REDACTED]")
	return value
}

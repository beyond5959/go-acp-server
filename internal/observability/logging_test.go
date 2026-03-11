package observability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewJSONHandlerUsesSecondPrecisionTimestamp(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, slog.LevelInfo))

	logger.Info("test.log", "k", "v")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("empty log output")
	}
	entry := map[string]any{}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}

	timeVal, ok := entry["time"].(string)
	if !ok {
		t.Fatalf("time type = %T, want string", entry["time"])
	}
	timeRaw := strings.TrimSpace(timeVal)
	if timeRaw == "" {
		t.Fatal("time is empty")
	}
	if strings.Contains(timeRaw, ".") {
		t.Fatalf("time includes sub-second precision: %q", timeRaw)
	}
	parsed, err := time.Parse(time.DateTime, timeRaw)
	if err != nil {
		t.Fatalf("time parse error: %v (value=%q)", err, timeRaw)
	}
	if !parsed.Equal(parsed.UTC()) {
		t.Fatalf("time is not UTC: %q", timeRaw)
	}
}

func TestLogACPMessageDisabledDoesNotEmit(t *testing.T) {
	ConfigureACPDebug(nil, false)

	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, slog.LevelDebug))
	ConfigureACPDebug(logger, false)

	LogACPMessage("codex-embedded", "outbound", map[string]any{
		"jsonrpc": "2.0",
		"id":      "srv-1",
		"method":  "session/prompt",
		"params": map[string]any{
			"prompt": "hello",
		},
	})

	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Fatalf("debug log output = %q, want empty", got)
	}
}

func TestLogACPMessageSanitizesSensitiveFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, slog.LevelDebug))
	ConfigureACPDebug(logger, true)
	t.Cleanup(func() {
		ConfigureACPDebug(nil, false)
	})

	LogACPMessage("codex-embedded", "outbound", map[string]any{
		"jsonrpc": "2.0",
		"id":      "srv-2",
		"method":  "session/prompt",
		"params": map[string]any{
			"prompt":    "run with Bearer secret-token and sk-abcdef",
			"authToken": "secret-token",
			"nested":    map[string]any{"api_key": "sk-abcdef"},
		},
	})

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("empty debug log output")
	}

	entry := map[string]any{}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("unmarshal log: %v", err)
	}
	if got := fmt.Sprintf("%v", entry["msg"]); got != "acp.message" {
		t.Fatalf("msg = %q, want %q", got, "acp.message")
	}
	if got := fmt.Sprintf("%v", entry["component"]); got != "codex-embedded" {
		t.Fatalf("component = %q, want %q", got, "codex-embedded")
	}
	if got := fmt.Sprintf("%v", entry["direction"]); got != "outbound" {
		t.Fatalf("direction = %q, want %q", got, "outbound")
	}
	if got := fmt.Sprintf("%v", entry["rpcType"]); got != "request" {
		t.Fatalf("rpcType = %q, want %q", got, "request")
	}
	if got := fmt.Sprintf("%v", entry["method"]); got != "session/prompt" {
		t.Fatalf("method = %q, want %q", got, "session/prompt")
	}

	rpc, ok := entry["rpc"].(map[string]any)
	if !ok {
		t.Fatalf("rpc type = %T, want map[string]any", entry["rpc"])
	}
	params, ok := rpc["params"].(map[string]any)
	if !ok {
		t.Fatalf("params type = %T, want map[string]any", rpc["params"])
	}
	if got := fmt.Sprintf("%v", params["authToken"]); got != "[REDACTED]" {
		t.Fatalf("authToken = %q, want %q", got, "[REDACTED]")
	}
	nested, ok := params["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested type = %T, want map[string]any", params["nested"])
	}
	if got := fmt.Sprintf("%v", nested["api_key"]); got != "[REDACTED]" {
		t.Fatalf("nested api_key = %q, want %q", got, "[REDACTED]")
	}
	prompt, _ := params["prompt"].(string)
	if strings.Contains(prompt, "secret-token") {
		t.Fatalf("prompt still contains bearer token: %q", prompt)
	}
	if strings.Contains(prompt, "sk-abcdef") {
		t.Fatalf("prompt still contains API key: %q", prompt)
	}
}

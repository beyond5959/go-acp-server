package observability

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestLoggerFormatsInfoLine(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, LevelInfo)

	logger.Info("startup.ready", "port", 8686, "allowPublic", false)

	got := strings.TrimSpace(buf.String())
	want := "INFO: startup.ready port=8686 allowPublic=false"
	if got != want {
		t.Fatalf("log output = %q, want %q", got, want)
	}
}

func TestHTTPRequestFormatsAccessLog(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, LevelInfo)
	requestTime := time.Date(2026, time.March, 23, 15, 30, 45, 0, time.FixedZone("UTC+8", 8*60*60))

	logger.HTTPRequest(HTTPRequestLogEntry{
		RemoteAddr:  "127.0.0.1",
		Method:      "GET",
		Path:        "/api/sessions/?limit=100&offset=0",
		Proto:       "HTTP/1.1",
		Status:      200,
		RequestTime: requestTime,
		Duration:    1250 * time.Microsecond,
	})

	got := strings.TrimSpace(buf.String())
	want := `INFO: ` + requestTime.In(time.Local).Format(time.DateTime) +
		` 127.0.0.1 - "GET /api/sessions/?limit=100&offset=0 HTTP/1.1" 200 OK 1.2ms`
	if got != want {
		t.Fatalf("access log = %q, want %q", got, want)
	}
}

func TestLogACPMessageDisabledDoesNotEmit(t *testing.T) {
	ConfigureACPDebug(nil, false)

	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, LevelDebug)
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
	logger := NewLoggerWithWriter(&buf, LevelDebug)
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
	if !strings.Contains(line, "DEBUG: acp.message") {
		t.Fatalf("missing debug prefix in %q", line)
	}
	if !strings.Contains(line, "component=codex-embedded") {
		t.Fatalf("component missing from %q", line)
	}
	if !strings.Contains(line, "direction=outbound") {
		t.Fatalf("direction missing from %q", line)
	}
	if !strings.Contains(line, "rpcType=request") {
		t.Fatalf("rpcType missing from %q", line)
	}
	if !strings.Contains(line, "method=session/prompt") {
		t.Fatalf("method missing from %q", line)
	}
	if !strings.Contains(line, `"authToken":"[REDACTED]"`) {
		t.Fatalf("authToken not redacted in %q", line)
	}
	if !strings.Contains(line, `"api_key":"[REDACTED]"`) {
		t.Fatalf("nested api_key not redacted in %q", line)
	}
	if strings.Contains(line, "secret-token") {
		t.Fatalf("secret token still present in %q", line)
	}
	if strings.Contains(line, "sk-abcdef") {
		t.Fatalf("openai key still present in %q", line)
	}
}

func TestRedactStringRedactsSensitiveQueryValues(t *testing.T) {
	got := RedactString("/attachments/att_1?client_id=client-a&access_token=secret-token")
	want := "/attachments/att_1?client_id=client-a&access_token=[REDACTED]"
	if got != want {
		t.Fatalf("RedactString() = %q, want %q", got, want)
	}
}

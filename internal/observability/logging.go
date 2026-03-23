package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
)

// Level controls which log entries are emitted.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiCyan   = "\x1b[36m"
)

// Logger writes human-readable service logs to stderr.
type Logger struct {
	mu    sync.Mutex
	out   io.Writer
	level Level
	color bool
}

// HTTPRequestLogEntry captures one access-log line.
type HTTPRequestLogEntry struct {
	RemoteAddr  string
	Method      string
	Path        string
	Proto       string
	Status      int
	RequestTime time.Time
	Duration    time.Duration
}

// NewLogger builds a logger that writes to stderr.
func NewLogger(level Level) *Logger {
	return NewLoggerWithWriter(os.Stderr, level)
}

// NewLoggerWithWriter builds a logger that writes to the provided writer.
func NewLoggerWithWriter(w io.Writer, level Level) *Logger {
	if w == nil {
		w = io.Discard
	}
	return &Logger{
		out:   w,
		level: level,
		color: shouldUseANSI(w),
	}
}

// Enabled reports whether the provided level will be emitted.
func (l *Logger) Enabled(level Level) bool {
	if l == nil {
		return false
	}
	return level >= l.level
}

// Debug emits one debug log line.
func (l *Logger) Debug(msg string, attrs ...any) {
	l.log(LevelDebug, msg, attrs...)
}

// Info emits one info log line.
func (l *Logger) Info(msg string, attrs ...any) {
	l.log(LevelInfo, msg, attrs...)
}

// Warn emits one warning log line.
func (l *Logger) Warn(msg string, attrs ...any) {
	l.log(LevelWarn, msg, attrs...)
}

// Error emits one error log line.
func (l *Logger) Error(msg string, attrs ...any) {
	l.log(LevelError, msg, attrs...)
}

// HTTPRequest emits one nginx-style access-log line.
func (l *Logger) HTTPRequest(entry HTTPRequestLogEntry) {
	if !l.Enabled(LevelInfo) {
		return
	}

	remoteAddr := strings.TrimSpace(entry.RemoteAddr)
	if remoteAddr == "" {
		remoteAddr = "unknown"
	}
	method := strings.TrimSpace(entry.Method)
	if method == "" {
		method = http.MethodGet
	}
	path := strings.TrimSpace(entry.Path)
	if path == "" {
		path = "/"
	}
	proto := strings.TrimSpace(entry.Proto)
	if proto == "" {
		proto = "HTTP/1.1"
	}

	requestLine := fmt.Sprintf("%s %s %s", method, path, proto)
	statusText := strings.TrimSpace(http.StatusText(entry.Status))
	if statusText == "" {
		statusText = "UNKNOWN"
	}
	requestTime := entry.RequestTime.In(time.Local)
	if entry.RequestTime.IsZero() {
		requestTime = time.Now().In(time.Local)
	}

	line := fmt.Sprintf(
		"%s %s %s - %s %s %s %s\n",
		l.formatLevel(LevelInfo),
		requestTime.Format(time.DateTime),
		remoteAddr,
		l.formatRequestLine(requestLine),
		l.formatStatusCode(entry.Status),
		l.formatStatusText(entry.Status, statusText),
		formatDuration(entry.Duration),
	)
	l.write(line)
}

func (l *Logger) log(level Level, msg string, attrs ...any) {
	if !l.Enabled(level) {
		return
	}

	msg = strings.TrimSpace(RedactString(msg))
	if msg == "" {
		msg = "log"
	}

	var builder strings.Builder
	builder.WriteString(l.formatLevel(level))
	builder.WriteString(" ")
	builder.WriteString(msg)

	fields := normalizeFields(attrs)
	for _, field := range fields {
		builder.WriteString(" ")
		builder.WriteString(field.key)
		builder.WriteString("=")
		builder.WriteString(formatFieldValue(field.value))
	}
	builder.WriteString("\n")

	l.write(builder.String())
}

func (l *Logger) write(line string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.out, line)
}

func (l *Logger) formatLevel(level Level) string {
	label := level.String() + ":"
	if !l.color {
		return label
	}
	return colorize(levelColor(level), label)
}

func (l *Logger) formatRequestLine(requestLine string) string {
	quoted := strconv.Quote(requestLine)
	if !l.color {
		return quoted
	}
	return colorize(ansiBold, quoted)
}

func (l *Logger) formatStatusCode(status int) string {
	text := strconv.Itoa(status)
	if !l.color {
		return text
	}
	return colorize(statusColor(status), text)
}

func (l *Logger) formatStatusText(status int, text string) string {
	if !l.color {
		return text
	}
	return colorize(statusColor(status), text)
}

// String returns the uppercase log level name.
func (level Level) String() string {
	switch level {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func levelColor(level Level) string {
	switch level {
	case LevelDebug:
		return ansiCyan
	case LevelInfo:
		return ansiGreen
	case LevelWarn:
		return ansiYellow
	case LevelError:
		return ansiRed
	default:
		return ""
	}
}

func statusColor(status int) string {
	switch {
	case status >= 500:
		return ansiRed
	case status >= 400:
		return ansiYellow
	case status >= 300:
		return ansiCyan
	default:
		return ansiGreen
	}
}

func colorize(color, text string) string {
	if strings.TrimSpace(color) == "" {
		return text
	}
	return color + text + ansiReset
}

func shouldUseANSI(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

type logField struct {
	key   string
	value any
}

func normalizeFields(attrs []any) []logField {
	fields := make([]logField, 0, len(attrs)/2+1)
	for i := 0; i < len(attrs); {
		if i == len(attrs)-1 {
			fields = append(fields, logField{
				key:   fmt.Sprintf("arg%d", len(fields)+1),
				value: attrs[i],
			})
			break
		}

		key, ok := attrs[i].(string)
		if !ok || strings.TrimSpace(key) == "" {
			fields = append(fields, logField{
				key:   fmt.Sprintf("arg%d", len(fields)+1),
				value: attrs[i],
			})
			i++
			continue
		}

		fields = append(fields, logField{
			key:   strings.TrimSpace(key),
			value: attrs[i+1],
		})
		i += 2
	}
	return fields
}

func formatFieldValue(value any) string {
	normalized := sanitizeLogValue(normalizeLogValue(value))
	switch v := normalized.(type) {
	case nil:
		return "null"
	case string:
		if shouldQuote(v) {
			return strconv.Quote(v)
		}
		return v
	default:
		wire, err := json.Marshal(v)
		if err != nil {
			return strconv.Quote(RedactString(fmt.Sprintf("%v", value)))
		}
		return string(wire)
	}
}

func shouldQuote(value string) bool {
	if value == "" {
		return true
	}
	return strings.ContainsAny(value, " \t\r\n\"=")
}

func formatDuration(duration time.Duration) string {
	if duration <= 0 {
		return "0ms"
	}
	switch {
	case duration >= time.Second:
		return fmt.Sprintf("%.3fs", duration.Seconds())
	case duration >= time.Millisecond:
		return fmt.Sprintf("%.1fms", float64(duration)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%dus", duration/time.Microsecond)
	}
}

// RedactString removes obvious secrets from log text.
func RedactString(value string) string {
	return redactString(value)
}

// IsSensitiveKey reports whether a key should have its value redacted in logs.
func IsSensitiveKey(key string) bool {
	return isSensitiveKey(key)
}

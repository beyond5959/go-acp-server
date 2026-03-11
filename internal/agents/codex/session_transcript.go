package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/beyond5959/ngent/internal/agents"
)

const sessionTranscriptScannerMaxToken = 16 * 1024 * 1024

var _ agents.SessionTranscriptLoader = (*Client)(nil)

// LoadSessionTranscript returns replayable user/assistant messages for one Codex session.
func (c *Client) LoadSessionTranscript(
	ctx context.Context,
	req agents.SessionTranscriptRequest,
) (agents.SessionTranscriptResult, error) {
	if c == nil {
		return agents.SessionTranscriptResult{}, errors.New("codex: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return agents.SessionTranscriptResult{}, errors.New("codex: sessionID is required")
	}

	session, err := c.findSession(ctx, req.CWD, sessionID)
	if err != nil {
		return agents.SessionTranscriptResult{}, err
	}
	path, err := sessionTranscriptPath(session)
	if err != nil {
		return agents.SessionTranscriptResult{}, err
	}
	return loadSessionTranscriptFile(path)
}

func (c *Client) findSession(
	ctx context.Context,
	cwd, sessionID string,
) (agents.SessionInfo, error) {
	cursor := ""
	for {
		result, err := c.ListSessions(ctx, agents.SessionListRequest{
			CWD:    cwd,
			Cursor: cursor,
		})
		if err != nil {
			return agents.SessionInfo{}, err
		}
		for _, session := range result.Sessions {
			if codexSessionMatchesID(session, sessionID) {
				return agents.CloneSessionInfo(session), nil
			}
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			break
		}
	}
	return agents.SessionInfo{}, agents.ErrSessionNotFound
}

func sessionTranscriptPath(session agents.SessionInfo) (string, error) {
	if len(session.Meta) == 0 {
		return "", fmt.Errorf("codex: session %q transcript path missing", session.SessionID)
	}
	pathValue, _ := session.Meta["path"].(string)
	path := strings.TrimSpace(pathValue)
	if path == "" {
		return "", fmt.Errorf("codex: session %q transcript path missing", session.SessionID)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("codex: session %q transcript path is not absolute", session.SessionID)
	}
	return filepath.Clean(path), nil
}

func loadSessionTranscriptFile(path string) (agents.SessionTranscriptResult, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return agents.SessionTranscriptResult{}, agents.ErrSessionNotFound
		}
		return agents.SessionTranscriptResult{}, fmt.Errorf("codex: open session transcript: %w", err)
	}
	defer file.Close()

	messages, err := parseSessionTranscript(file)
	if err != nil {
		return agents.SessionTranscriptResult{}, err
	}
	return agents.CloneSessionTranscriptResult(agents.SessionTranscriptResult{
		Messages: messages,
	}), nil
}

func parseSessionTranscript(reader io.Reader) ([]agents.SessionTranscriptMessage, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), sessionTranscriptScannerMaxToken)

	messages := make([]agents.SessionTranscriptMessage, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var item struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("codex: decode session transcript line: %w", err)
		}
		if item.Type != "response_item" {
			continue
		}

		message, ok, err := parseSessionTranscriptMessage(item.Timestamp, item.Payload)
		if err != nil {
			return nil, err
		}
		if ok {
			messages = append(messages, message)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("codex: scan session transcript: %w", err)
	}
	return messages, nil
}

func parseSessionTranscriptMessage(
	timestamp string,
	payload json.RawMessage,
) (agents.SessionTranscriptMessage, bool, error) {
	var item struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Phase   string `json:"phase"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(payload, &item); err != nil {
		return agents.SessionTranscriptMessage{}, false, fmt.Errorf("codex: decode transcript message: %w", err)
	}
	if item.Type != "message" {
		return agents.SessionTranscriptMessage{}, false, nil
	}

	role := strings.TrimSpace(strings.ToLower(item.Role))
	if role != "user" && role != "assistant" {
		return agents.SessionTranscriptMessage{}, false, nil
	}
	if role == "assistant" {
		phase := strings.TrimSpace(strings.ToLower(item.Phase))
		if phase != "" && phase != "final_answer" {
			return agents.SessionTranscriptMessage{}, false, nil
		}
	}

	var body strings.Builder
	for _, part := range item.Content {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		body.WriteString(part.Text)
	}
	content, ok := normalizeCodexTranscriptContent(role, body.String())
	if !ok {
		return agents.SessionTranscriptMessage{}, false, nil
	}

	return agents.SessionTranscriptMessage{
		Role:      role,
		Content:   content,
		Timestamp: strings.TrimSpace(timestamp),
	}, true, nil
}

func normalizeCodexTranscriptContent(role, content string) (string, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	if role != "user" {
		return content, true
	}
	return normalizeCodexTranscriptUserContent(content)
}

func normalizeCodexTranscriptUserContent(content string) (string, bool) {
	content = normalizeTranscriptNewlines(content)

	if normalized := codexExtractIDERequest(content); normalized != "" {
		return normalized, true
	}
	if normalized := codexExtractCurrentUserInput(content); normalized != "" {
		return normalized, true
	}
	if codexIsBootstrapUserContent(content) {
		return "", false
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	return content, true
}

func normalizeTranscriptNewlines(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content)
}

func codexExtractIDERequest(content string) string {
	if !strings.HasPrefix(content, "# Context from my IDE setup:") {
		return ""
	}
	return extractCodexTranscriptSuffix(content, "## My request for Codex:")
}

func codexExtractCurrentUserInput(content string) string {
	if !strings.HasPrefix(content, "[Conversation Summary]") {
		return ""
	}
	return extractCodexTranscriptSuffix(content, "[Current User Input]")
}

func extractCodexTranscriptSuffix(content, marker string) string {
	index := strings.LastIndex(content, marker)
	if index < 0 {
		return ""
	}
	content = strings.TrimSpace(content[index+len(marker):])
	if content == "" {
		return ""
	}
	return content
}

func codexIsBootstrapUserContent(content string) bool {
	if strings.HasPrefix(content, "# AGENTS.md instructions for ") {
		return true
	}
	if strings.HasPrefix(content, "<environment_context>") {
		return true
	}
	return strings.Contains(content, "\n<environment_context>") &&
		strings.Contains(content, "# AGENTS.md instructions for ")
}

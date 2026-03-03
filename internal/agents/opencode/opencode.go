package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/beyond5959/go-acp-server/internal/agents"
	"github.com/beyond5959/go-acp-server/internal/agents/acpstdio"
	"github.com/beyond5959/go-acp-server/internal/agents/agentutil"
)

const updateTypeMessageChunk = "agent_message_chunk"

// Config configures the OpenCode ACP stdio provider.
type Config struct {
	// Dir is the working directory passed to opencode acp --cwd.
	Dir string
	// ModelID is the optional model identifier (e.g. "anthropic/claude-3-5-haiku-20241022").
	// When empty, OpenCode uses its configured default model.
	ModelID string
}

// Client runs one opencode acp process per Stream call.
type Client struct {
	dir     string
	modelID string
}

var _ agents.Streamer = (*Client)(nil)

// New constructs an OpenCode ACP client.
func New(cfg Config) (*Client, error) {
	dir, err := agentutil.RequireDir("opencode", cfg.Dir)
	if err != nil {
		return nil, err
	}
	return &Client{
		dir:     dir,
		modelID: strings.TrimSpace(cfg.ModelID),
	}, nil
}

// Preflight checks that the opencode binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary("opencode")
}

// Name returns the provider identifier.
func (c *Client) Name() string { return "opencode" }

// Stream spawns opencode acp, runs one turn, and streams deltas via onDelta.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("opencode: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("opencode: onDelta callback is required")
	}

	cmd := exec.Command("opencode", "acp", "--cwd", c.dir)
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: open stdout pipe: %w", err)
	}
	// Discard stderr to avoid blocking.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := acpstdio.NewConn(stdin, stdout, "opencode")
	defer conn.Close()
	defer acpstdio.TerminateProcess(cmd, errCh, 2*time.Second)

	// 1. initialize — protocolVersion must be an integer.
	if _, err := conn.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "agent-hub-server",
			"version": "0.1.0",
		},
		"protocolVersion": 1,
	}); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: initialize: %w", err)
	}

	// 2. session/new — server assigns sessionId; mcpServers is required.
	newResult, err := conn.Call(ctx, "session/new", map[string]any{
		"cwd":        c.dir,
		"mcpServers": []any{},
	})
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: session/new: %w", err)
	}
	sessionID := acpstdio.ParseSessionID(newResult)
	if sessionID == "" {
		return agents.StopReasonEndTurn, errors.New("opencode: session/new returned empty sessionId")
	}

	// 3. Wire streaming: agent_message_chunk -> onDelta.
	conn.SetNotificationHandler(func(msg acpstdio.Message) error {
		if msg.Method != "session/update" {
			return nil
		}
		var payload struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if len(msg.Params) == 0 {
			return nil
		}
		if err := json.Unmarshal(msg.Params, &payload); err != nil {
			return nil // ignore malformed updates
		}
		if payload.Update.SessionUpdate == updateTypeMessageChunk {
			if text := payload.Update.Content.Text; text != "" {
				return onDelta(text)
			}
		}
		return nil
	})

	// 4. session/prompt.
	promptParams := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": input}},
	}
	if c.modelID != "" {
		promptParams["modelId"] = c.modelID
	}

	promptResult, err := conn.Call(ctx, "session/prompt", promptParams)
	if err != nil {
		if ctx.Err() != nil {
			c.sendCancel(conn, sessionID)
			return agents.StopReasonCancelled, nil
		}
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: session/prompt: %w", err)
	}

	if acpstdio.ParseStopReason(promptResult) == "cancelled" {
		return agents.StopReasonCancelled, nil
	}
	return agents.StopReasonEndTurn, nil
}

func (c *Client) sendCancel(conn *acpstdio.Conn, sessionID string) {
	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = conn.Call(cancelCtx, "session/cancel", map[string]any{"sessionId": sessionID})
}

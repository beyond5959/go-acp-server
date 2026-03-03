package qwen

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

const (
	updateTypeMessageChunk = "agent_message_chunk"

	defaultPermissionTimeout = 15 * time.Second
)

// Config configures the Qwen CLI ACP stdio provider.
type Config struct {
	// Dir is the working directory for the Qwen session.
	Dir string
	// ModelID is the optional model identifier.
	ModelID string
}

// Client runs one qwen --acp process per Stream call.
type Client struct {
	dir     string
	modelID string
}

var _ agents.Streamer = (*Client)(nil)

// New constructs a Qwen ACP client.
func New(cfg Config) (*Client, error) {
	dir, err := agentutil.RequireDir("qwen", cfg.Dir)
	if err != nil {
		return nil, err
	}
	return &Client{
		dir:     dir,
		modelID: strings.TrimSpace(cfg.ModelID),
	}, nil
}

// Preflight checks that the qwen binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary("qwen")
}

// Name returns the provider identifier.
func (c *Client) Name() string { return "qwen" }

// Stream spawns qwen --acp, runs one turn, and streams deltas via onDelta.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("qwen: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("qwen: onDelta callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.Command("qwen", "--acp")
	cmd.Dir = c.dir
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("qwen: open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("qwen: open stdout pipe: %w", err)
	}
	// Discard stderr to avoid protocol corruption and pipe blocking.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("qwen: open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("qwen: start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := acpstdio.NewConn(stdin, stdout, "qwen")
	defer conn.Close()
	defer acpstdio.TerminateProcess(cmd, errCh, 2*time.Second)

	// 1) initialize
	if _, err := conn.Call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	}); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("qwen: initialize: %w", err)
	}

	// 2) session/new
	newResult, err := conn.Call(ctx, "session/new", map[string]any{
		"cwd":        c.dir,
		"mcpServers": []any{},
	})
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("qwen: session/new: %w", err)
	}
	sessionID := acpstdio.ParseSessionID(newResult)
	if sessionID == "" {
		return agents.StopReasonEndTurn, errors.New("qwen: session/new returned empty sessionId")
	}

	// 3) wire permission requests with fail-closed default.
	permHandler, hasPermHandler := agents.PermissionHandlerFromContext(ctx)
	conn.SetRequestHandler(func(method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "session/request_permission" {
			return nil, &acpstdio.RPCError{Code: acpstdio.MethodNotFound, Message: "method not found"}
		}

		var req struct {
			SessionID string `json:"sessionId"`
			ToolCall  struct {
				Title string `json:"title"`
				Kind  string `json:"kind"`
			} `json:"toolCall"`
			Options []struct {
				OptionID string `json:"optionId"`
				Kind     string `json:"kind"`
			} `json:"options"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			// Fail-closed: malformed request => decline/cancel.
			return buildDeclinedPermissionResponse(req.Options)
		}

		// Default fail-closed when no handler.
		if !hasPermHandler {
			return buildDeclinedPermissionResponse(req.Options)
		}

		permCtx, cancel := context.WithTimeout(ctx, defaultPermissionTimeout)
		defer cancel()

		resp, err := permHandler(permCtx, agents.PermissionRequest{
			Approval:  strings.TrimSpace(req.ToolCall.Title),
			Command:   strings.TrimSpace(req.ToolCall.Kind),
			RawParams: map[string]any{"sessionId": req.SessionID},
		})
		if err != nil {
			// Fail-closed: timeout/exception => decline/cancel.
			return buildDeclinedPermissionResponse(req.Options)
		}

		switch resp.Outcome {
		case agents.PermissionOutcomeApproved:
			return buildApprovedPermissionResponse(req.Options)
		case agents.PermissionOutcomeCancelled:
			return buildCancelledPermissionResponse()
		default:
			return buildDeclinedPermissionResponse(req.Options)
		}
	})

	// 4) stream session/update -> agent_message_chunk.content.text
	conn.SetNotificationHandler(func(msg acpstdio.Message) error {
		if msg.Method != "session/update" {
			return nil
		}
		if len(msg.Params) == 0 {
			return nil
		}
		var payload struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if err := json.Unmarshal(msg.Params, &payload); err != nil {
			return nil // Ignore malformed update notifications.
		}
		if payload.Update.SessionUpdate != updateTypeMessageChunk {
			return nil
		}
		text := payload.Update.Content.Text
		if text == "" {
			return nil
		}
		return onDelta(text)
	})

	// 5) send session/cancel quickly when context is cancelled.
	stopCancelWatch := make(chan struct{})
	defer close(stopCancelWatch)
	go func() {
		select {
		case <-ctx.Done():
			c.sendCancel(conn, sessionID)
		case <-stopCancelWatch:
		}
	}()

	// 6) session/prompt
	promptParams := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": input}},
	}

	promptResult, err := conn.Call(ctx, "session/prompt", promptParams)
	if err != nil {
		if ctx.Err() != nil {
			return agents.StopReasonCancelled, nil
		}
		return agents.StopReasonEndTurn, fmt.Errorf("qwen: session/prompt: %w", err)
	}

	if acpstdio.ParseStopReason(promptResult) == "cancelled" {
		return agents.StopReasonCancelled, nil
	}
	return agents.StopReasonEndTurn, nil
}

func (c *Client) sendCancel(conn *acpstdio.Conn, sessionID string) {
	conn.Notify("session/cancel", map[string]any{"sessionId": sessionID})
}

func buildApprovedPermissionResponse(options []struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}) (json.RawMessage, error) {
	optionID := pickPermissionOptionID(options, "allow_once", "allow_always")
	if optionID == "" {
		// Fail-closed when no allow option is available.
		return buildDeclinedPermissionResponse(options)
	}
	return buildSelectedPermissionResponse(optionID)
}

func buildDeclinedPermissionResponse(options []struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}) (json.RawMessage, error) {
	optionID := pickPermissionOptionID(options, "reject_once", "reject_always")
	if optionID == "" {
		return buildCancelledPermissionResponse()
	}
	return buildSelectedPermissionResponse(optionID)
}

func buildSelectedPermissionResponse(optionID string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"outcome": map[string]any{
			"outcome":  "selected",
			"optionId": optionID,
		},
	})
}

func buildCancelledPermissionResponse() (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"outcome": map[string]any{
			"outcome": "cancelled",
		},
	})
}

func pickPermissionOptionID(options []struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
}, preferredKinds ...string) string {
	for _, kind := range preferredKinds {
		for _, option := range options {
			if strings.TrimSpace(option.OptionID) == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(option.Kind), kind) {
				return strings.TrimSpace(option.OptionID)
			}
		}
	}
	return ""
}

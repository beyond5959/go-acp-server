package qwen

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpcli"
	"github.com/beyond5959/ngent/internal/agents/acpstdio"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
)

const defaultPermissionTimeout = 15 * time.Second

var handlePermissionRequest = acpcli.StructuredPermissionRequestHandler(defaultPermissionTimeout)

// Config configures the Qwen CLI ACP stdio provider.
type Config = agentutil.Config

// Client runs one qwen --acp process per ACP operation.
type Client struct {
	*acpcli.Client
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SessionTranscriptLoader = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs a Qwen ACP client.
func New(cfg Config) (*Client, error) {
	base, err := acpcli.New(agents.AgentIDQwen, cfg, acpcli.Hooks{
		OpenConn:                openConn(cfg.Dir),
		SessionNewParams:        acpcli.SessionNewParams(cfg.Dir),
		SessionLoadParams:       acpcli.SessionLoadParams(cfg.Dir),
		SessionListParams:       acpcli.SessionListParams(cfg.Dir),
		PromptParams:            promptParams,
		DiscoverModelsParams:    acpcli.DiscoverModelsParams(cfg.Dir),
		HandlePermissionRequest: handlePermissionRequest,
		Cancel:                  cancelWithNotify,
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: base}, nil
}

// Preflight checks that the qwen binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary(agents.AgentIDQwen)
}

func openConn(dir string) func(context.Context, acpcli.OpenConnRequest) (*acpstdio.Conn, func(), json.RawMessage, error) {
	return func(
		ctx context.Context,
		req acpcli.OpenConnRequest,
	) (*acpstdio.Conn, func(), json.RawMessage, error) {
		conn, cleanup, initResult, err := acpcli.OpenProcess(ctx, acpcli.ProcessConfig{
			Command: agents.AgentIDQwen,
			Args:    []string{"--acp"},
			Dir:     strings.TrimSpace(dir),
			Env:     os.Environ(),
			ConnOptions: acpstdio.ConnOptions{
				Prefix: agents.AgentIDQwen,
			},
			InitializeParams: initializeParams(),
		})
		if err != nil {
			return nil, nil, nil, acpcli.WrapOpenError(agents.AgentIDQwen, req.Purpose, err)
		}
		return conn, cleanup, initResult, nil
	}
}

func initializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	}
}

func promptParams(sessionID string, prompt agents.Prompt, modelID string) map[string]any {
	params := acpcli.ACPPromptParams(sessionID, prompt)
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		params["model"] = modelID
	}
	return params
}

func cancelWithNotify(conn *acpstdio.Conn, sessionID string) {
	if conn == nil {
		return
	}
	conn.Notify("session/cancel", map[string]any{"sessionId": strings.TrimSpace(sessionID)})
}

// Name returns the provider identifier.
func (c *Client) Name() string {
	if c == nil || c.Client == nil {
		return agents.AgentIDQwen
	}
	return c.Client.Name()
}

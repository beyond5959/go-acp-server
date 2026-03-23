package blackbox

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

// Config configures the BLACKBOX AI CLI ACP stdio provider.
type Config = agentutil.Config

// Client runs one blackbox --experimental-acp process per ACP operation.
type Client struct {
	*acpcli.Client
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SessionTranscriptLoader = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs a BLACKBOX AI ACP client.
func New(cfg Config) (*Client, error) {
	base, err := acpcli.New(agents.AgentIDBlackbox, cfg, acpcli.Hooks{
		OpenConn:                openConn(cfg.Dir),
		SessionNewParams:        acpcli.SessionNewParams(cfg.Dir),
		SessionLoadParams:       acpcli.SessionLoadParams(cfg.Dir),
		SessionListParams:       acpcli.SessionListParams(cfg.Dir),
		PromptParams:            promptParams,
		DiscoverModelsParams:    acpcli.DiscoverModelsParams(cfg.Dir),
		HandlePermissionRequest: handlePermissionRequest,
		Cancel:                  cancelWithCall,
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: base}, nil
}

// Preflight checks that the blackbox binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary(agents.AgentIDBlackbox)
}

func openConn(dir string) func(context.Context, acpcli.OpenConnRequest) (*acpstdio.Conn, func(), json.RawMessage, error) {
	return func(
		ctx context.Context,
		req acpcli.OpenConnRequest,
	) (*acpstdio.Conn, func(), json.RawMessage, error) {
		modelID := strings.TrimSpace(req.ModelID)
		if req.Purpose == acpcli.OpenPurposeDiscoverModels {
			modelID = ""
		}

		conn, cleanup, initResult, err := acpcli.OpenProcess(ctx, acpcli.ProcessConfig{
			Command: agents.AgentIDBlackbox,
			Args:    commandArgs(modelID),
			Dir:     strings.TrimSpace(dir),
			Env:     os.Environ(),
			ConnOptions: acpstdio.ConnOptions{
				Prefix:           agents.AgentIDBlackbox,
				AllowStdoutNoise: true,
			},
			InitializeParams: initializeParams(),
		})
		if err != nil {
			return nil, nil, nil, acpcli.WrapOpenError(agents.AgentIDBlackbox, req.Purpose, err)
		}
		return conn, cleanup, initResult, nil
	}
}

func commandArgs(modelID string) []string {
	args := []string{"--skip-update", "--telemetry=false"}
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		args = append(args, "--model", modelID)
	}
	return append(args, "--experimental-acp")
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

func cancelWithCall(conn *acpstdio.Conn, sessionID string) {
	if conn == nil {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = conn.Call(cancelCtx, "session/cancel", map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
	})
}

// Name returns the provider identifier.
func (c *Client) Name() string {
	if c == nil || c.Client == nil {
		return agents.AgentIDBlackbox
	}
	return c.Client.Name()
}

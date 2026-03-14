package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpcli"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	"github.com/beyond5959/ngent/internal/agents/acpstdio"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
)

const defaultPermissionTimeout = 15 * time.Second

var handlePermissionRequest = acpcli.StructuredPermissionRequestHandler(defaultPermissionTimeout)

// Config configures the OpenCode ACP stdio provider.
type Config = agentutil.Config

// Client runs one opencode acp process per ACP operation.
type Client struct {
	*acpcli.Client
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SessionTranscriptLoader = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs an OpenCode ACP client.
func New(cfg Config) (*Client, error) {
	base, err := acpcli.New("opencode", cfg, acpcli.Hooks{
		OpenConn:                openConn(cfg.Dir),
		SessionNewParams:        sessionNewParams(cfg.Dir),
		SessionLoadParams:       sessionLoadParams(cfg.Dir),
		SessionListParams:       sessionListParams(cfg.Dir),
		PromptParams:            promptParams,
		DiscoverModelsParams:    discoverModelsParams(cfg.Dir),
		PrepareConfigSession:    prepareConfigSession,
		HandlePermissionRequest: handlePermissionRequest,
		Cancel:                  cancelWithCall,
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: base}, nil
}

// Preflight checks that the opencode binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary("opencode")
}

func openConn(dir string) func(context.Context, acpcli.OpenConnRequest) (*acpstdio.Conn, func(), json.RawMessage, error) {
	return func(
		ctx context.Context,
		req acpcli.OpenConnRequest,
	) (*acpstdio.Conn, func(), json.RawMessage, error) {
		args := []string{"acp", "--cwd", strings.TrimSpace(dir)}
		if modelID := strings.TrimSpace(req.ModelID); modelID != "" {
			args = append([]string{"-m", modelID}, args...)
		}
		conn, cleanup, initResult, err := acpcli.OpenProcess(ctx, acpcli.ProcessConfig{
			Command: "opencode",
			Args:    args,
			Dir:     strings.TrimSpace(dir),
			Env:     os.Environ(),
			ConnOptions: acpstdio.ConnOptions{
				Prefix: "opencode",
			},
			InitializeParams: initializeParams(),
		})
		if err != nil {
			return nil, nil, nil, acpcli.WrapOpenError("opencode", req.Purpose, err)
		}
		return conn, cleanup, initResult, nil
	}
}

func initializeParams() map[string]any {
	return map[string]any{
		"clientInfo": map[string]any{
			"name":    "ngent",
			"version": "0.1.0",
		},
		"protocolVersion": 1,
	}
}

func sessionNewParams(dir string) func(string) map[string]any {
	return func(modelID string) map[string]any {
		params := map[string]any{
			"cwd":        strings.TrimSpace(dir),
			"mcpServers": []any{},
		}
		if modelID = strings.TrimSpace(modelID); modelID != "" {
			params["model"] = modelID
			params["modelId"] = modelID
		}
		return params
	}
}

func discoverModelsParams(dir string) func(string) map[string]any {
	return func(string) map[string]any {
		return map[string]any{
			"cwd":        strings.TrimSpace(dir),
			"mcpServers": []any{},
		}
	}
}

func sessionLoadParams(dir string) func(string) map[string]any {
	return func(sessionID string) map[string]any {
		return map[string]any{
			"sessionId":  strings.TrimSpace(sessionID),
			"cwd":        strings.TrimSpace(dir),
			"mcpServers": []any{},
		}
	}
}

func sessionListParams(dir string) func(string, string) map[string]any {
	return func(cwd, cursor string) map[string]any {
		params := map[string]any{
			"cwd":        sessionCWD(dir, cwd),
			"mcpServers": []any{},
		}
		if cursor = strings.TrimSpace(cursor); cursor != "" {
			params["cursor"] = cursor
		}
		return params
	}
}

func promptParams(sessionID, input, modelID string) map[string]any {
	params := map[string]any{
		"sessionId": strings.TrimSpace(sessionID),
		"prompt":    []map[string]any{{"type": "text", "text": input}},
	}
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		params["modelId"] = modelID
	}
	return params
}

func prepareConfigSession(
	modelID string,
	_ map[string]string,
	configID, value string,
) acpcli.ConfigSessionPlan {
	plan := acpcli.ConfigSessionPlan{
		SessionModelID: strings.TrimSpace(modelID),
	}
	if strings.EqualFold(strings.TrimSpace(configID), "model") && strings.TrimSpace(value) != "" {
		plan.SessionModelID = strings.TrimSpace(value)
		plan.SkipSetConfig = true
	}
	return plan
}

// SetConfigOption applies one ACP session config option.
func (c *Client) SetConfigOption(ctx context.Context, configID, value string) ([]agents.ConfigOption, error) {
	if c == nil || c.Client == nil {
		return nil, errors.New("opencode: nil client")
	}
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" {
		return nil, errors.New("opencode: configID is required")
	}
	if value == "" {
		return nil, errors.New("opencode: value is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	options, err := c.RunConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), configID, value)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(configID, "model") {
		options = configOptionsWithSelection(options, configID, value)
	}
	c.ApplyConfigOptionResult(configID, value, options)
	return options, nil
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

func sessionCWD(dir, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return strings.TrimSpace(dir)
}

func configOptionsWithSelection(options []agents.ConfigOption, configID, value string) []agents.ConfigOption {
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" || value == "" || len(options) == 0 {
		return options
	}

	cloned := acpmodel.CloneConfigOptions(options)
	updated := false
	for i := range cloned {
		if !strings.EqualFold(strings.TrimSpace(cloned[i].ID), configID) {
			continue
		}
		cloned[i].CurrentValue = value
		foundValue := false
		for _, optionValue := range cloned[i].Options {
			if strings.EqualFold(strings.TrimSpace(optionValue.Value), value) {
				foundValue = true
				break
			}
		}
		if !foundValue {
			cloned[i].Options = append([]agents.ConfigOptionValue{{
				Value: value,
				Name:  value,
			}}, cloned[i].Options...)
		}
		updated = true
		break
	}
	if !updated {
		return options
	}
	return acpmodel.NormalizeConfigOptions(cloned)
}

// Name returns the provider identifier.
func (c *Client) Name() string {
	if c == nil || c.Client == nil {
		return "opencode"
	}
	return c.Client.Name()
}

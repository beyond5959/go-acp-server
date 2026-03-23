package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
const methodSessionSetModel = "session/set_model"

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
	base, err := acpcli.New(agents.AgentIDOpencode, cfg, acpcli.Hooks{
		OpenConn:                openConn(cfg.Dir),
		SessionNewParams:        acpcli.SessionNewParams(cfg.Dir),
		SessionLoadParams:       acpcli.SessionLoadParams(cfg.Dir),
		SessionListParams:       acpcli.SessionListParams(cfg.Dir),
		PromptParams:            promptParams,
		DiscoverModelsParams:    acpcli.DiscoverModelsParams(cfg.Dir),
		PrepareConfigSession:    prepareConfigSession,
		SelectSessionModel:      selectSessionModel,
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
	return agentutil.PreflightBinary(agents.AgentIDOpencode)
}

func openConn(dir string) func(context.Context, acpcli.OpenConnRequest) (*acpstdio.Conn, func(), json.RawMessage, error) {
	return func(
		ctx context.Context,
		req acpcli.OpenConnRequest,
	) (*acpstdio.Conn, func(), json.RawMessage, error) {
		args := []string{"acp", "--cwd", strings.TrimSpace(dir)}
		conn, cleanup, initResult, err := acpcli.OpenProcess(ctx, acpcli.ProcessConfig{
			Command: agents.AgentIDOpencode,
			Args:    args,
			Dir:     strings.TrimSpace(dir),
			Env:     os.Environ(),
			ConnOptions: acpstdio.ConnOptions{
				Prefix: agents.AgentIDOpencode,
			},
			InitializeParams: initializeParams(),
		})
		if err != nil {
			return nil, nil, nil, acpcli.WrapOpenError(agents.AgentIDOpencode, req.Purpose, err)
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

func promptParams(sessionID string, prompt agents.Prompt, modelID string) map[string]any {
	params := acpcli.ACPPromptParams(sessionID, prompt)
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		params["modelId"] = modelID
	}
	return params
}

func selectSessionModel(
	ctx context.Context,
	conn *acpstdio.Conn,
	sessionID, modelID string,
	options []agents.ConfigOption,
) ([]agents.ConfigOption, error) {
	if conn == nil {
		return options, nil
	}
	sessionID = strings.TrimSpace(sessionID)
	modelID = strings.TrimSpace(modelID)
	if sessionID == "" || modelID == "" {
		return options, nil
	}

	_, err := conn.Call(ctx, methodSessionSetModel, map[string]any{
		"sessionId": sessionID,
		"modelId":   modelID,
	})
	if err != nil {
		if isMethodNotFoundError(err) {
			return configOptionsWithSelection(options, "model", modelID), nil
		}
		return nil, fmt.Errorf("%s: %s: %w", agents.AgentIDOpencode, methodSessionSetModel, err)
	}
	return configOptionsWithSelection(options, "model", modelID), nil
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

func isMethodNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "(-32601)") && strings.Contains(text, "method not found")
}

// Name returns the provider identifier.
func (c *Client) Name() string {
	if c == nil || c.Client == nil {
		return agents.AgentIDOpencode
	}
	return c.Client.Name()
}

package kimi

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
	"github.com/beyond5959/ngent/internal/agents/acpstdio"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
)

const defaultPermissionTimeout = 15 * time.Second

var handlePermissionRequest = acpcli.StructuredPermissionRequestHandler(defaultPermissionTimeout)

// Config configures the Kimi CLI ACP stdio provider.
type Config = agentutil.Config

// Client runs one Kimi ACP process per ACP operation.
type Client struct {
	*acpcli.Client
}

type commandSpec struct {
	mode  string
	label string
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SessionTranscriptLoader = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs a Kimi ACP client.
func New(cfg Config) (*Client, error) {
	base, err := acpcli.New("kimi", cfg, acpcli.Hooks{
		OpenConn:                openConn(cfg.Dir),
		SessionNewParams:        sessionNewParams(cfg.Dir),
		SessionLoadParams:       sessionLoadParams(cfg.Dir),
		SessionListParams:       sessionListParams(cfg.Dir),
		PromptParams:            promptParams,
		DiscoverModelsParams:    sessionNewParams(cfg.Dir),
		PrepareConfigSession:    prepareConfigSession,
		HandlePermissionRequest: handlePermissionRequest,
		Cancel:                  cancelWithNotify,
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: base}, nil
}

// Preflight checks that the kimi binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary("kimi")
}

// ConfigOptions queries ACP session config options.
func (c *Client) ConfigOptions(ctx context.Context) ([]agents.ConfigOption, error) {
	if c == nil || c.Client == nil {
		return nil, errors.New("kimi: nil client")
	}
	if localCfg, err := loadLocalConfig(); err == nil {
		return localCfg.ConfigOptions(c.CurrentModelID(), c.CurrentConfigOverrides()), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.RunConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), "", "")
}

// SetConfigOption applies one ACP session config option.
func (c *Client) SetConfigOption(ctx context.Context, configID, value string) ([]agents.ConfigOption, error) {
	if c == nil || c.Client == nil {
		return nil, errors.New("kimi: nil client")
	}
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" {
		return nil, errors.New("kimi: configID is required")
	}
	if value == "" {
		return nil, errors.New("kimi: value is required")
	}

	if localCfg, err := loadLocalConfig(); err == nil {
		options, localErr := c.setLocalConfigOption(localCfg, configID, value)
		if localErr != nil {
			return nil, localErr
		}
		c.ApplyConfigOptionResult(configID, value, options)
		return options, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	options, err := c.RunConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), configID, value)
	if err != nil {
		return nil, err
	}
	c.ApplyConfigOptionResult(configID, value, options)
	return options, nil
}

func openConn(dir string) func(context.Context, acpcli.OpenConnRequest) (*acpstdio.Conn, func(), json.RawMessage, error) {
	return func(
		ctx context.Context,
		req acpcli.OpenConnRequest,
	) (*acpstdio.Conn, func(), json.RawMessage, error) {
		var attemptErrors []string
		for idx, spec := range commandCandidates() {
			conn, cleanup, initResult, err := acpcli.OpenProcess(ctx, acpcli.ProcessConfig{
				Command: "kimi",
				Args:    spec.args(req.ModelID, kimiThinkingArg(req.ModelID, req.ConfigOverrides)),
				Dir:     strings.TrimSpace(dir),
				Env:     os.Environ(),
				ConnOptions: acpstdio.ConnOptions{
					Prefix: "kimi",
				},
				InitializeParams: initializeParams(),
			})
			if err == nil {
				return conn, cleanup, initResult, nil
			}

			wrapped := acpcli.WrapOpenError("kimi", req.Purpose, fmt.Errorf("%s: %w", spec.label, err))
			attemptErrors = append(attemptErrors, wrapped.Error())
			if idx == len(commandCandidates())-1 || !shouldRetryACPStartup(err) {
				break
			}
		}
		return nil, nil, nil, fmt.Errorf("kimi: failed to start ACP mode (%s)", strings.Join(attemptErrors, "; "))
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

func sessionNewParams(dir string) func(string) map[string]any {
	return func(modelID string) map[string]any {
		params := map[string]any{
			"cwd":        strings.TrimSpace(dir),
			"mcpServers": []any{},
		}
		modelID = strings.TrimSpace(modelID)
		if modelID != "" {
			params["model"] = modelID
			params["modelId"] = modelID
		}
		return params
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
		params["model"] = modelID
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

func cancelWithNotify(conn *acpstdio.Conn, sessionID string) {
	if conn == nil {
		return
	}
	conn.Notify("session/cancel", map[string]any{"sessionId": strings.TrimSpace(sessionID)})
}

func commandCandidates() []commandSpec {
	return []commandSpec{
		{mode: "subcommand", label: "kimi acp"},
		{mode: "flag", label: "kimi --acp"},
	}
}

func (s commandSpec) args(modelID, thinkingArg string) []string {
	args := make([]string, 0, 4)
	if modelID = strings.TrimSpace(modelID); modelID != "" {
		args = append(args, "--model", modelID)
	}
	if thinkingArg != "" {
		args = append(args, thinkingArg)
	}
	switch s.mode {
	case "flag":
		args = append(args, "--acp")
	default:
		args = append(args, "acp")
	}
	return args
}

func kimiThinkingArg(modelID string, configOverrides map[string]string) string {
	reasoningValue, ok := normalizeThinkingValue(configOverrides[reasoningConfigID])
	if !ok {
		return ""
	}
	if localCfg, err := loadLocalConfig(); err == nil && !localCfg.SupportsThinking(modelID) {
		return ""
	}
	if reasoningValue == reasoningValueEnabled {
		return "--thinking"
	}
	return "--no-thinking"
}

func (c *Client) setLocalConfigOption(cfg localConfig, configID, value string) ([]agents.ConfigOption, error) {
	switch {
	case strings.EqualFold(configID, "model"):
		if _, ok := cfg.modelByID(value); !ok {
			return nil, fmt.Errorf("kimi: unsupported model %q", value)
		}
		return cfg.ConfigOptions(value, c.CurrentConfigOverrides()), nil
	case strings.EqualFold(configID, reasoningConfigID):
		reasoningValue, ok := normalizeThinkingValue(value)
		if !ok {
			return nil, fmt.Errorf("kimi: unsupported reasoning value %q", value)
		}
		modelID := c.CurrentModelID()
		if !cfg.SupportsThinking(modelID) {
			return nil, errors.New("kimi: current model does not support reasoning")
		}
		overrides := c.CurrentConfigOverrides()
		if overrides == nil {
			overrides = make(map[string]string)
		}
		overrides[reasoningConfigID] = reasoningValue
		return cfg.ConfigOptions(modelID, overrides), nil
	default:
		return nil, fmt.Errorf("kimi: config option %q is not supported without ACP session", configID)
	}
}

func sessionCWD(dir, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return strings.TrimSpace(dir)
}

func shouldRetryACPStartup(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "connection closed") ||
		strings.Contains(message, "start process") ||
		strings.Contains(message, "initialize")
}

// Name returns the provider identifier.
func (c *Client) Name() string {
	if c == nil || c.Client == nil {
		return "kimi"
	}
	return c.Client.Name()
}

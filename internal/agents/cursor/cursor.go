package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpcli"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	"github.com/beyond5959/ngent/internal/agents/acpstdio"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
)

const defaultPermissionTimeout = 15 * time.Second
const methodSessionSetConfigOption = "session/set_config_option"
const authMethodCursorLogin = "cursor_login"

var handlePermissionRequest = acpcli.StructuredPermissionRequestHandler(defaultPermissionTimeout)

// Config configures the Cursor CLI ACP stdio provider.
type Config = agentutil.Config

// Client runs one Cursor ACP process per ACP operation.
type Client struct {
	*acpcli.Client
}

type commandSpec struct {
	command string
	label   string
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SessionTranscriptLoader = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs a Cursor ACP client.
func New(cfg Config) (*Client, error) {
	base, err := acpcli.New(agents.AgentIDCursor, cfg, acpcli.Hooks{
		OpenConn:                openConn(cfg.Dir),
		SessionNewParams:        sessionNewParams(cfg.Dir),
		SessionLoadParams:       acpcli.SessionLoadParams(cfg.Dir),
		SessionListParams:       acpcli.SessionListParams(cfg.Dir),
		PromptParams:            promptParams,
		DiscoverModelsParams:    sessionNewParams(cfg.Dir),
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

// Preflight checks that one supported Cursor CLI binary is available in PATH.
func Preflight() error {
	var lastErr error
	for _, spec := range commandCandidates() {
		if _, err := exec.LookPath(spec.command); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		return errors.New("cursor: no command candidates configured")
	}
	return fmt.Errorf("cursor binary not found in PATH (tried %s): %w", joinedCommandNames(), lastErr)
}

func openConn(dir string) func(context.Context, acpcli.OpenConnRequest) (*acpstdio.Conn, func(), json.RawMessage, error) {
	return func(
		ctx context.Context,
		req acpcli.OpenConnRequest,
	) (*acpstdio.Conn, func(), json.RawMessage, error) {
		var attemptErrors []string
		for _, spec := range commandCandidates() {
			conn, cleanup, initResult, err := acpcli.OpenProcess(ctx, acpcli.ProcessConfig{
				Command: spec.command,
				Args:    []string{"acp"},
				Dir:     strings.TrimSpace(dir),
				Env:     os.Environ(),
				ConnOptions: acpstdio.ConnOptions{
					Prefix: agents.AgentIDCursor,
				},
				InitializeParams: initializeParams(),
			})
			if err != nil {
				attemptErrors = append(attemptErrors, acpcli.WrapOpenError(
					agents.AgentIDCursor,
					req.Purpose,
					fmt.Errorf("%s: %w", spec.label, err),
				).Error())
				continue
			}

			if err := authenticate(ctx, conn, initResult); err != nil {
				cleanup()
				attemptErrors = append(attemptErrors, acpcli.WrapOpenError(
					agents.AgentIDCursor,
					req.Purpose,
					fmt.Errorf("%s authenticate: %w", spec.label, err),
				).Error())
				continue
			}
			return conn, cleanup, initResult, nil
		}
		return nil, nil, nil, fmt.Errorf("cursor: failed to start ACP mode (%s)", strings.Join(attemptErrors, "; "))
	}
}

func initializeParams() map[string]any {
	return map[string]any{
		"clientInfo": map[string]any{
			"name":    "ngent",
			"version": "0.1.0",
		},
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
			"terminal": false,
		},
	}
}

func sessionNewParams(dir string) func(string) map[string]any {
	return func(string) map[string]any {
		return map[string]any{
			"cwd":        strings.TrimSpace(dir),
			"mcpServers": []any{},
		}
	}
}

func promptParams(sessionID string, prompt agents.Prompt, _ string) map[string]any {
	return acpcli.ACPPromptParams(sessionID, prompt)
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

	setResult, err := conn.Call(ctx, methodSessionSetConfigOption, map[string]any{
		"sessionId": sessionID,
		"configId":  "model",
		"value":     modelID,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %s(model): %w", agents.AgentIDCursor, methodSessionSetConfigOption, err)
	}
	if updated := acpmodel.ExtractConfigOptions(setResult); len(updated) > 0 {
		return updated, nil
	}
	return configOptionsWithSelection(options, "model", modelID), nil
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

func authenticate(ctx context.Context, conn *acpstdio.Conn, initResult json.RawMessage) error {
	if conn == nil || !requiresCursorLogin(initResult) {
		return nil
	}
	_, err := conn.Call(ctx, "authenticate", map[string]any{
		"methodId": authMethodCursorLogin,
	})
	if err != nil {
		return fmt.Errorf("authenticate(%s): %w", authMethodCursorLogin, err)
	}
	return nil
}

func requiresCursorLogin(initResult json.RawMessage) bool {
	var payload struct {
		AuthMethods []struct {
			ID string `json:"id"`
		} `json:"authMethods"`
	}
	if len(initResult) == 0 || json.Unmarshal(initResult, &payload) != nil {
		return false
	}
	for _, method := range payload.AuthMethods {
		if strings.EqualFold(strings.TrimSpace(method.ID), authMethodCursorLogin) {
			return true
		}
	}
	return false
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

func commandCandidates() []commandSpec {
	return []commandSpec{
		{command: "agent", label: "agent acp"},
		{command: "cursor-agent", label: "cursor-agent acp"},
	}
}

func joinedCommandNames() string {
	names := make([]string, 0, len(commandCandidates()))
	for _, spec := range commandCandidates() {
		if name := strings.TrimSpace(spec.command); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// Name returns the provider identifier.
func (c *Client) Name() string {
	if c == nil || c.Client == nil {
		return agents.AgentIDCursor
	}
	return c.Client.Name()
}

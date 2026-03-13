package kimi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	"github.com/beyond5959/ngent/internal/agents/acpsession"
	"github.com/beyond5959/ngent/internal/agents/acpstdio"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
)

const (
	methodSessionSetConfigOption = "session/set_config_option"

	defaultPermissionTimeout = 15 * time.Second
)

// Config configures the Kimi CLI ACP stdio provider.
type Config = agentutil.Config

// Client runs one Kimi ACP process per Stream call.
type Client struct {
	*agentutil.State
	slashCommands agents.SlashCommandsCache
}

type commandSpec struct {
	mode  string
	label string
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs a Kimi ACP client.
func New(cfg Config) (*Client, error) {
	state, err := agentutil.NewState("kimi", cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		State: state,
	}, nil
}

// Preflight checks that the kimi binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary("kimi")
}

// Name returns the provider identifier.
func (c *Client) Name() string { return "kimi" }

// ConfigOptions queries ACP session config options.
func (c *Client) ConfigOptions(ctx context.Context) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("kimi: nil client")
	}
	if localCfg, err := loadLocalConfig(); err == nil {
		return localCfg.ConfigOptions(c.CurrentModelID(), c.CurrentConfigOverrides()), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.runConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), "", "")
}

// SlashCommands returns the latest slash-command snapshot for the current context.
func (c *Client) SlashCommands(ctx context.Context) ([]agents.SlashCommand, bool, error) {
	if c == nil {
		return nil, false, errors.New("kimi: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if commands, known := c.slashCommands.Snapshot(); known {
		return commands, true, nil
	}
	if _, err := c.runConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), "", ""); err != nil {
		return nil, false, err
	}
	commands, known := c.slashCommands.Snapshot()
	return commands, known, nil
}

// SetConfigOption applies one ACP session config option.
func (c *Client) SetConfigOption(ctx context.Context, configID, value string) ([]agents.ConfigOption, error) {
	if c == nil {
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

	options, err := c.runConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), configID, value)
	if err != nil {
		return nil, err
	}
	c.ApplyConfigOptionResult(configID, value, options)
	return options, nil
}

// ListSessions queries ACP session/list for the current cwd.
func (c *Client) ListSessions(ctx context.Context, req agents.SessionListRequest) (agents.SessionListResult, error) {
	if c == nil {
		return agents.SessionListResult{}, errors.New("kimi: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	conn, cleanup, _, initResult, err := c.openConn(ctx, c.CurrentModelID(), c.CurrentConfigOverrides())
	if err != nil {
		return agents.SessionListResult{}, err
	}
	defer cleanup()

	caps := acpsession.ParseInitializeCapabilities(initResult)
	if !caps.CanList || !caps.CanLoad {
		return agents.SessionListResult{}, agents.ErrSessionListUnsupported
	}

	params := map[string]any{
		"cwd":        kimiSessionCWD(c, req.CWD),
		"mcpServers": []any{},
	}
	if cursor := strings.TrimSpace(req.Cursor); cursor != "" {
		params["cursor"] = cursor
	}
	result, err := conn.Call(ctx, "session/list", params)
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("kimi: session/list: %w", err)
	}
	return acpsession.ParseSessionListResult(result)
}

// Stream spawns Kimi in ACP mode, runs one turn, and streams deltas via onDelta.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("kimi: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("kimi: onDelta callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	modelID := c.CurrentModelID()
	configOverrides := c.CurrentConfigOverrides()

	conn, cleanup, _, initResult, err := c.openConn(ctx, modelID, configOverrides)
	if err != nil {
		return agents.StopReasonEndTurn, err
	}
	defer cleanup()
	caps := acpsession.ParseInitializeCapabilities(initResult)

	streamCtx := c.slashCommands.WrapContext(ctx)
	markPromptStarted := agents.InstallACPStdioNotificationHandler(conn, streamCtx, onDelta)

	sessionID := c.CurrentSessionID()
	initialOptions := []agents.ConfigOption(nil)
	if sessionID != "" {
		if !caps.CanLoad {
			return agents.StopReasonEndTurn, agents.ErrSessionLoadUnsupported
		}
		if _, err := conn.Call(ctx, "session/load", kimiSessionLoadParams(c, sessionID)); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("kimi: session/load: %w", err)
		}
	} else {
		newResult, err := conn.Call(ctx, "session/new", sessionNewParams(c.Dir(), modelID))
		if err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("kimi: session/new: %w", err)
		}
		sessionID = acpstdio.ParseSessionID(newResult)
		if sessionID == "" {
			return agents.StopReasonEndTurn, errors.New("kimi: session/new returned empty sessionId")
		}
		initialOptions = acpmodel.ExtractConfigOptions(newResult)
	}
	if _, err := c.applyConfigOverrides(ctx, conn, sessionID, initialOptions, configOverrides); err != nil {
		return agents.StopReasonEndTurn, err
	}
	if caps.CanLoad {
		c.SetSessionID(sessionID)
		if err := agents.NotifySessionBound(streamCtx, sessionID); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("kimi: report session bound: %w", err)
		}
	}

	permHandler, hasPermHandler := agents.PermissionHandlerFromContext(streamCtx)
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
			return buildDeclinedPermissionResponse(req.Options)
		}

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

	stopCancelWatch := make(chan struct{})
	defer close(stopCancelWatch)
	go func() {
		select {
		case <-ctx.Done():
			c.sendCancel(conn, sessionID)
		case <-stopCancelWatch:
		}
	}()

	promptParams := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": input}},
	}
	if modelID != "" {
		promptParams["model"] = modelID
	}

	markPromptStarted()
	promptResult, err := conn.Call(ctx, "session/prompt", promptParams)
	if err != nil {
		if ctx.Err() != nil {
			return agents.StopReasonCancelled, nil
		}
		return agents.StopReasonEndTurn, fmt.Errorf("kimi: session/prompt: %w", err)
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

func (c *Client) runConfigSession(
	ctx context.Context,
	modelID string,
	configOverrides map[string]string,
	configID, value string,
) ([]agents.ConfigOption, error) {
	sessionModelID := modelID
	if strings.EqualFold(configID, "model") && value != "" {
		sessionModelID = value
	}

	conn, cleanup, _, _, err := c.openConn(ctx, sessionModelID, configOverrides)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	configCtx := c.slashCommands.WrapContext(ctx)
	_ = agents.InstallACPStdioNotificationHandler(conn, configCtx, func(string) error { return nil })

	newResult, err := conn.Call(ctx, "session/new", sessionNewParams(c.Dir(), sessionModelID))
	if err != nil {
		return nil, fmt.Errorf("kimi: config options session/new: %w", err)
	}
	sessionID := acpstdio.ParseSessionID(newResult)
	if sessionID == "" {
		return nil, errors.New("kimi: config options session/new returned empty sessionId")
	}

	options, err := c.applyConfigOverrides(ctx, conn, sessionID, acpmodel.ExtractConfigOptions(newResult), configOverrides)
	if err != nil {
		return nil, err
	}
	if configID == "" {
		return options, nil
	}
	if strings.EqualFold(configID, "model") {
		return options, nil
	}
	setResult, err := conn.Call(ctx, methodSessionSetConfigOption, map[string]any{
		"sessionId": sessionID,
		"configId":  configID,
		"value":     value,
	})
	if err != nil {
		return nil, fmt.Errorf("kimi: config options session/set_config_option: %w", err)
	}

	updated := acpmodel.ExtractConfigOptions(setResult)
	if len(updated) == 0 {
		return options, nil
	}
	return updated, nil
}

func (c *Client) applyConfigOverrides(
	ctx context.Context,
	conn *acpstdio.Conn,
	sessionID string,
	options []agents.ConfigOption,
	overrides map[string]string,
) ([]agents.ConfigOption, error) {
	if len(overrides) == 0 {
		return options, nil
	}

	configIDs := make([]string, 0, len(overrides))
	for configID := range overrides {
		configIDs = append(configIDs, configID)
	}
	sort.Strings(configIDs)

	current := options
	for _, configID := range configIDs {
		value := strings.TrimSpace(overrides[configID])
		if value == "" {
			continue
		}
		setResult, err := conn.Call(ctx, methodSessionSetConfigOption, map[string]any{
			"sessionId": sessionID,
			"configId":  configID,
			"value":     value,
		})
		if err != nil {
			return nil, fmt.Errorf("kimi: session/set_config_option(%s): %w", configID, err)
		}
		if updated := acpmodel.ExtractConfigOptions(setResult); len(updated) > 0 {
			current = updated
		}
	}
	return current, nil
}

func (c *Client) openConn(
	ctx context.Context,
	modelID string,
	configOverrides map[string]string,
) (*acpstdio.Conn, func(), string, json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	specs := commandCandidates()
	var attemptErrors []string
	for idx, spec := range specs {
		conn, cleanup, initResult, err := c.openConnWithCommand(ctx, spec, modelID, configOverrides)
		if err == nil {
			return conn, cleanup, spec.label, initResult, nil
		}
		attemptErrors = append(attemptErrors, err.Error())
		if idx == len(specs)-1 || !shouldRetryACPStartup(err) {
			break
		}
	}

	return nil, nil, "", nil, fmt.Errorf(
		"kimi: failed to start ACP mode (%s)",
		strings.Join(attemptErrors, "; "),
	)
}

func (c *Client) openConnWithCommand(
	ctx context.Context,
	spec commandSpec,
	modelID string,
	configOverrides map[string]string,
) (*acpstdio.Conn, func(), json.RawMessage, error) {
	cmd := exec.Command("kimi", spec.args(modelID, kimiThinkingArg(modelID, configOverrides))...)
	cmd.Dir = c.Dir()
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("kimi: %s open stdin pipe: %w", spec.label, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("kimi: %s open stdout pipe: %w", spec.label, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("kimi: %s open stderr pipe: %w", spec.label, err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("kimi: %s start process: %w", spec.label, err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := acpstdio.NewConn(stdin, stdout, "kimi")
	cleanup := func() {
		conn.Close()
		acpstdio.TerminateProcess(cmd, errCh, 2*time.Second)
	}

	initResult, err := conn.Call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	})
	if err != nil {
		cleanup()
		return nil, nil, nil, fmt.Errorf("kimi: %s initialize: %w", spec.label, err)
	}

	return conn, cleanup, initResult, nil
}

func commandCandidates() []commandSpec {
	// Official Kimi docs currently show both `kimi acp` and `kimi --acp`.
	return []commandSpec{
		{mode: "subcommand", label: "kimi acp"},
		{mode: "flag", label: "kimi --acp"},
	}
}

func (s commandSpec) args(modelID, thinkingArg string) []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(modelID) != "" {
		args = append(args, "--model", strings.TrimSpace(modelID))
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

func sessionNewParams(dir, modelID string) map[string]any {
	params := map[string]any{
		"cwd":        dir,
		"mcpServers": []any{},
	}
	modelID = strings.TrimSpace(modelID)
	if modelID != "" {
		// Kimi's ACP runtime may derive model selection from process startup
		// flags, but sending both fields keeps handshake responses aligned when
		// the server also honors session/new model hints.
		params["model"] = modelID
		params["modelId"] = modelID
	}
	return params
}

func kimiSessionCWD(c *Client, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return c.Dir()
}

func kimiSessionLoadParams(c *Client, sessionID string) map[string]any {
	return map[string]any{
		"sessionId":  strings.TrimSpace(sessionID),
		"cwd":        c.Dir(),
		"mcpServers": []any{},
	}
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

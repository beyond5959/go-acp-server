package qwen

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

// Config configures the Qwen CLI ACP stdio provider.
type Config = agentutil.Config

// Client runs one qwen --acp process per Stream call.
type Client struct {
	*agentutil.State
	slashCommands agents.SlashCommandsCache
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs a Qwen ACP client.
func New(cfg Config) (*Client, error) {
	state, err := agentutil.NewState("qwen", cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		State: state,
	}, nil
}

// Preflight checks that the qwen binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary("qwen")
}

// Name returns the provider identifier.
func (c *Client) Name() string { return "qwen" }

// ConfigOptions queries ACP session config options.
func (c *Client) ConfigOptions(ctx context.Context) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("qwen: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.runConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), "", "")
}

// SlashCommands returns the latest slash-command snapshot for the current context.
func (c *Client) SlashCommands(ctx context.Context) ([]agents.SlashCommand, bool, error) {
	if c == nil {
		return nil, false, errors.New("qwen: nil client")
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
		return nil, errors.New("qwen: nil client")
	}
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" {
		return nil, errors.New("qwen: configID is required")
	}
	if value == "" {
		return nil, errors.New("qwen: value is required")
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
		return agents.SessionListResult{}, errors.New("qwen: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.Command("qwen", "--acp")
	cmd.Dir = c.Dir()
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("qwen: session list open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("qwen: session list open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("qwen: session list open stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return agents.SessionListResult{}, fmt.Errorf("qwen: session list start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := acpstdio.NewConn(stdin, stdout, "qwen")
	defer conn.Close()
	defer acpstdio.TerminateProcess(cmd, errCh, 2*time.Second)

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
		return agents.SessionListResult{}, fmt.Errorf("qwen: session list initialize: %w", err)
	}

	caps := acpsession.ParseInitializeCapabilities(initResult)
	if !caps.CanList || !caps.CanLoad {
		return agents.SessionListResult{}, agents.ErrSessionListUnsupported
	}

	params := map[string]any{
		"cwd":        qwenSessionCWD(c, req.CWD),
		"mcpServers": []any{},
	}
	if cursor := strings.TrimSpace(req.Cursor); cursor != "" {
		params["cursor"] = cursor
	}

	result, err := conn.Call(ctx, "session/list", params)
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("qwen: session/list: %w", err)
	}
	return acpsession.ParseSessionListResult(result)
}

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

	modelID := c.CurrentModelID()
	configOverrides := c.CurrentConfigOverrides()

	cmd := exec.Command("qwen", "--acp")
	cmd.Dir = c.Dir()
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
		return agents.StopReasonEndTurn, fmt.Errorf("qwen: initialize: %w", err)
	}
	caps := acpsession.ParseInitializeCapabilities(initResult)

	streamCtx := c.slashCommands.WrapContext(ctx)
	markPromptStarted := agents.InstallACPStdioNotificationHandler(conn, streamCtx, onDelta)

	// 2) session/load or session/new
	sessionID := c.CurrentSessionID()
	initialOptions := []agents.ConfigOption(nil)
	if sessionID != "" {
		if !caps.CanLoad {
			return agents.StopReasonEndTurn, agents.ErrSessionLoadUnsupported
		}
		if _, err := conn.Call(ctx, "session/load", qwenSessionLoadParams(c, sessionID)); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("qwen: session/load: %w", err)
		}
	} else {
		newResult, err := conn.Call(ctx, "session/new", qwenSessionNewParams(c, modelID))
		if err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("qwen: session/new: %w", err)
		}
		sessionID = acpstdio.ParseSessionID(newResult)
		if sessionID == "" {
			return agents.StopReasonEndTurn, errors.New("qwen: session/new returned empty sessionId")
		}
		initialOptions = acpmodel.ExtractConfigOptions(newResult)
	}
	if _, err := c.applyConfigOverrides(ctx, conn, sessionID, initialOptions, configOverrides); err != nil {
		return agents.StopReasonEndTurn, err
	}
	if caps.CanLoad {
		c.SetSessionID(sessionID)
		if err := agents.NotifySessionBound(streamCtx, sessionID); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("qwen: report session bound: %w", err)
		}
	}

	// 3) wire permission requests with fail-closed default.
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
	if modelID != "" {
		promptParams["model"] = modelID
	}

	markPromptStarted()
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

func qwenSessionCWD(c *Client, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return c.Dir()
}

func qwenSessionNewParams(c *Client, modelID string) map[string]any {
	params := map[string]any{
		"cwd":        c.Dir(),
		"mcpServers": []any{},
	}
	modelID = strings.TrimSpace(modelID)
	if modelID != "" {
		params["model"] = modelID
		params["modelId"] = modelID
	}
	return params
}

func qwenSessionLoadParams(c *Client, sessionID string) map[string]any {
	return map[string]any{
		"sessionId":  strings.TrimSpace(sessionID),
		"cwd":        c.Dir(),
		"mcpServers": []any{},
	}
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

func (c *Client) runConfigSession(
	ctx context.Context,
	modelID string,
	configOverrides map[string]string,
	configID, value string,
) ([]agents.ConfigOption, error) {
	cmd := exec.Command("qwen", "--acp")
	cmd.Dir = c.Dir()
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("qwen: config options open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("qwen: config options open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("qwen: config options open stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("qwen: config options start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := acpstdio.NewConn(stdin, stdout, "qwen")
	defer conn.Close()
	defer acpstdio.TerminateProcess(cmd, errCh, 2*time.Second)

	if _, err := conn.Call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("qwen: config options initialize: %w", err)
	}

	configCtx := c.slashCommands.WrapContext(ctx)
	_ = agents.InstallACPStdioNotificationHandler(conn, configCtx, func(string) error { return nil })

	newParams := map[string]any{
		"cwd":        c.Dir(),
		"mcpServers": []any{},
	}
	if modelID != "" {
		newParams["model"] = modelID
		newParams["modelId"] = modelID
	}
	newResult, err := conn.Call(ctx, "session/new", newParams)
	if err != nil {
		return nil, fmt.Errorf("qwen: config options session/new: %w", err)
	}
	sessionID := acpstdio.ParseSessionID(newResult)
	if sessionID == "" {
		return nil, errors.New("qwen: config options session/new returned empty sessionId")
	}

	options, err := c.applyConfigOverrides(ctx, conn, sessionID, acpmodel.ExtractConfigOptions(newResult), configOverrides)
	if err != nil {
		return nil, err
	}
	if configID == "" {
		return options, nil
	}
	setResult, err := conn.Call(ctx, methodSessionSetConfigOption, map[string]any{
		"sessionId": sessionID,
		"configId":  configID,
		"value":     value,
	})
	if err != nil {
		return nil, fmt.Errorf("qwen: config options session/set_config_option: %w", err)
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
			return nil, fmt.Errorf("qwen: session/set_config_option(%s): %w", configID, err)
		}
		if updated := acpmodel.ExtractConfigOptions(setResult); len(updated) > 0 {
			current = updated
		}
	}
	return current, nil
}

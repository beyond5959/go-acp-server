package opencode

import (
	"context"
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
)

// Config configures the OpenCode ACP stdio provider.
type Config = agentutil.Config

// Client runs one opencode acp process per Stream call.
type Client struct {
	*agentutil.State
	slashCommands agents.SlashCommandsCache
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs an OpenCode ACP client.
func New(cfg Config) (*Client, error) {
	state, err := agentutil.NewState("opencode", cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		State: state,
	}, nil
}

// Preflight checks that the opencode binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary("opencode")
}

// Name returns the provider identifier.
func (c *Client) Name() string { return "opencode" }

// ConfigOptions queries ACP session config options.
func (c *Client) ConfigOptions(ctx context.Context) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("opencode: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.runConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), "", "")
}

// SlashCommands returns the latest slash-command snapshot for the current context.
func (c *Client) SlashCommands(ctx context.Context) ([]agents.SlashCommand, bool, error) {
	if c == nil {
		return nil, false, errors.New("opencode: nil client")
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
		return agents.SessionListResult{}, errors.New("opencode: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.Command("opencode", "acp", "--cwd", c.Dir())
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("opencode: session list open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("opencode: session list open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("opencode: session list open stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return agents.SessionListResult{}, fmt.Errorf("opencode: session list start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := acpstdio.NewConn(stdin, stdout, "opencode")
	defer conn.Close()
	defer acpstdio.TerminateProcess(cmd, errCh, 2*time.Second)

	initResult, err := conn.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "ngent",
			"version": "0.1.0",
		},
		"protocolVersion": 1,
	})
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("opencode: session list initialize: %w", err)
	}

	caps := acpsession.ParseInitializeCapabilities(initResult)
	if !caps.CanList || !caps.CanLoad {
		return agents.SessionListResult{}, agents.ErrSessionListUnsupported
	}

	params := map[string]any{
		"cwd":        opencodeSessionCWD(c, req.CWD),
		"mcpServers": []any{},
	}
	if cursor := strings.TrimSpace(req.Cursor); cursor != "" {
		params["cursor"] = cursor
	}
	result, err := conn.Call(ctx, "session/list", params)
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("opencode: session/list: %w", err)
	}
	return acpsession.ParseSessionListResult(result)
}

// Stream spawns opencode acp, runs one turn, and streams deltas via onDelta.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("opencode: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("opencode: onDelta callback is required")
	}

	modelID := c.CurrentModelID()
	configOverrides := c.CurrentConfigOverrides()

	cmd := exec.Command("opencode", "acp", "--cwd", c.Dir())
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
	initResult, err := conn.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "ngent",
			"version": "0.1.0",
		},
		"protocolVersion": 1,
	})
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: initialize: %w", err)
	}
	caps := acpsession.ParseInitializeCapabilities(initResult)

	streamCtx := c.slashCommands.WrapContext(ctx)
	markPromptStarted := agents.InstallACPStdioNotificationHandler(conn, streamCtx, onDelta)

	// 2. session/load or session/new.
	sessionID := c.CurrentSessionID()
	initialOptions := []agents.ConfigOption(nil)
	if sessionID != "" {
		if !caps.CanLoad {
			return agents.StopReasonEndTurn, agents.ErrSessionLoadUnsupported
		}
		if _, err := conn.Call(ctx, "session/load", opencodeSessionLoadParams(c, sessionID)); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("opencode: session/load: %w", err)
		}
	} else {
		newResult, err := conn.Call(ctx, "session/new", opencodeSessionNewParams(c))
		if err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("opencode: session/new: %w", err)
		}
		sessionID = acpstdio.ParseSessionID(newResult)
		if sessionID == "" {
			return agents.StopReasonEndTurn, errors.New("opencode: session/new returned empty sessionId")
		}
		initialOptions = acpmodel.ExtractConfigOptions(newResult)
	}
	if _, err := c.applyConfigOverrides(ctx, conn, sessionID, initialOptions, configOverrides); err != nil {
		return agents.StopReasonEndTurn, err
	}
	if caps.CanLoad {
		c.SetSessionID(sessionID)
		if err := agents.NotifySessionBound(streamCtx, sessionID); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("opencode: report session bound: %w", err)
		}
	}

	// 4. session/prompt.
	promptParams := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": input}},
	}
	if modelID != "" {
		promptParams["modelId"] = modelID
	}

	markPromptStarted()
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

func opencodeSessionCWD(c *Client, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return c.Dir()
}

func opencodeSessionNewParams(c *Client) map[string]any {
	return map[string]any{
		"cwd":        c.Dir(),
		"mcpServers": []any{},
	}
}

func opencodeSessionLoadParams(c *Client, sessionID string) map[string]any {
	return map[string]any{
		"sessionId":  strings.TrimSpace(sessionID),
		"cwd":        c.Dir(),
		"mcpServers": []any{},
	}
}

func (c *Client) runConfigSession(
	ctx context.Context,
	modelID string,
	configOverrides map[string]string,
	configID, value string,
) ([]agents.ConfigOption, error) {
	cmd := exec.Command("opencode", "acp", "--cwd", c.Dir())
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("opencode: config options open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("opencode: config options open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("opencode: config options open stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("opencode: config options start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := acpstdio.NewConn(stdin, stdout, "opencode")
	defer conn.Close()
	defer acpstdio.TerminateProcess(cmd, errCh, 2*time.Second)

	if _, err := conn.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "ngent",
			"version": "0.1.0",
		},
		"protocolVersion": 1,
	}); err != nil {
		return nil, fmt.Errorf("opencode: config options initialize: %w", err)
	}

	configCtx := c.slashCommands.WrapContext(ctx)
	_ = agents.InstallACPStdioNotificationHandler(conn, configCtx, func(string) error { return nil })

	newParams := map[string]any{
		"cwd":        c.Dir(),
		"mcpServers": []any{},
	}
	if modelID != "" {
		// Some providers accept `model`, some accept `modelId`.
		newParams["model"] = modelID
		newParams["modelId"] = modelID
	}
	newResult, err := conn.Call(ctx, "session/new", newParams)
	if err != nil {
		return nil, fmt.Errorf("opencode: config options session/new: %w", err)
	}
	sessionID := acpstdio.ParseSessionID(newResult)
	if sessionID == "" {
		return nil, errors.New("opencode: config options session/new returned empty sessionId")
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
		return nil, fmt.Errorf("opencode: config options session/set_config_option: %w", err)
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
			return nil, fmt.Errorf("opencode: session/set_config_option(%s): %w", configID, err)
		}
		if updated := acpmodel.ExtractConfigOptions(setResult); len(updated) > 0 {
			current = updated
		}
	}
	return current, nil
}

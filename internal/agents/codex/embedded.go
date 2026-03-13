package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beyond5959/acp-adapter/pkg/codexacp"
	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	"github.com/beyond5959/ngent/internal/agents/acpsession"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
	"github.com/beyond5959/ngent/internal/observability"
)

const (
	jsonRPCVersion = "2.0"

	methodInitialize             = "initialize"
	methodSessionNew             = "session/new"
	methodSessionPrompt          = "session/prompt"
	methodSessionCancel          = "session/cancel"
	methodSessionSetConfigOption = "session/set_config_option"
	methodSessionUpdate          = "session/update"
	methodSessionRequestApproval = "session/request_permission"
)

const (
	defaultStartTimeout   = 30 * time.Second
	defaultRequestTimeout = 15 * time.Second

	stableSessionResolveRetries = 5
	stableSessionResolveDelay   = 150 * time.Millisecond
	initialSlashCommandsWait    = 250 * time.Millisecond
)

// Config configures one embedded codex runtime provider instance.
type Config struct {
	Dir             string
	ModelID         string
	SessionID       string
	ConfigOverrides map[string]string
	Name            string
	RuntimeConfig   codexacp.RuntimeConfig
	StartTimeout    time.Duration
	RequestTimeout  time.Duration
}

// Client streams turn output through one in-process codex-acp runtime.
type Client struct {
	*agentutil.State

	name string

	runtimeConfig  codexacp.RuntimeConfig
	startTimeout   time.Duration
	requestTimeout time.Duration

	initMu sync.Mutex
	mu     sync.Mutex
	closed bool

	runtime          *codexacp.EmbeddedRuntime
	sessionID        string
	runtimeSessionID string
	updateUnsub      func()

	configOptions      []agents.ConfigOption
	canLoadSession     bool
	slashCommands      []agents.SlashCommand
	slashCommandsKnown bool
	slashCommandsReady chan struct{}

	requestSeq uint64
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)
var _ io.Closer = (*Client)(nil)

// DefaultRuntimeConfig returns the default embedded runtime configuration.
func DefaultRuntimeConfig() codexacp.RuntimeConfig {
	cfg := codexacp.DefaultRuntimeConfig()
	cfg.InitialAuthMode = detectInitialAuthModeFromEnv()
	return cfg
}

func detectInitialAuthModeFromEnv() string {
	if strings.TrimSpace(os.Getenv("CODEX_API_KEY")) != "" {
		return "codex_api_key"
	}
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		return "openai_api_key"
	}
	if subscriptionEnabled(os.Getenv("CHATGPT_SUBSCRIPTION_ACTIVE")) {
		return "chatgpt_subscription"
	}
	return ""
}

func subscriptionEnabled(raw string) bool {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return true
	}

	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// Preflight checks whether runtime prerequisites are available on the host.
func Preflight(cfg codexacp.RuntimeConfig) error {
	command := strings.TrimSpace(cfg.AppServerCommand)
	if command == "" {
		command = strings.TrimSpace(codexacp.DefaultRuntimeConfig().AppServerCommand)
	}
	if command == "" {
		return errors.New("codex app-server command is empty")
	}
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("codex app-server command %q not found: %w", command, err)
	}
	return nil
}

// New constructs one embedded codex provider.
func New(cfg Config) (*Client, error) {
	runtimeCfg := cfg.RuntimeConfig
	if strings.TrimSpace(runtimeCfg.AppServerCommand) == "" &&
		len(runtimeCfg.AppServerArgs) == 0 &&
		strings.TrimSpace(runtimeCfg.LogLevel) == "" &&
		strings.TrimSpace(runtimeCfg.PatchApplyMode) == "" &&
		!runtimeCfg.TraceJSON &&
		strings.TrimSpace(runtimeCfg.TraceJSONFile) == "" &&
		!runtimeCfg.RetryTurnOnCrash &&
		len(runtimeCfg.Profiles) == 0 &&
		strings.TrimSpace(runtimeCfg.DefaultProfile) == "" &&
		strings.TrimSpace(runtimeCfg.InitialAuthMode) == "" {
		runtimeCfg = DefaultRuntimeConfig()
	}
	if err := Preflight(runtimeCfg); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "codex-embedded"
	}

	startTimeout := cfg.StartTimeout
	if startTimeout <= 0 {
		startTimeout = defaultStartTimeout
	}
	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}

	state, err := agentutil.NewState("codex", agentutil.Config{
		Dir:             cfg.Dir,
		ModelID:         cfg.ModelID,
		SessionID:       cfg.SessionID,
		ConfigOverrides: cfg.ConfigOverrides,
	})
	if err != nil {
		return nil, err
	}

	return &Client{
		State:          state,
		name:           name,
		runtimeConfig:  runtimeCfg,
		startTimeout:   startTimeout,
		requestTimeout: requestTimeout,
	}, nil
}

// Name returns provider name.
func (c *Client) Name() string {
	if c == nil || c.name == "" {
		return "codex-embedded"
	}
	return c.name
}

// ConfigOptions returns current ACP session config options.
func (c *Client) ConfigOptions(ctx context.Context) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("codex: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, _, _, err := c.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("codex: initialize runtime: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return acpmodel.CloneConfigOptions(c.configOptions), nil
}

// SlashCommands returns the latest slash-command snapshot after runtime init.
func (c *Client) SlashCommands(ctx context.Context) ([]agents.SlashCommand, bool, error) {
	if c == nil {
		return nil, false, errors.New("codex: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, _, _, err := c.ensureInitialized(ctx); err != nil {
		return nil, false, fmt.Errorf("codex: initialize runtime: %w", err)
	}
	c.waitForInitialSlashCommands(ctx)

	c.mu.Lock()
	known := c.slashCommandsKnown
	commands := agents.CloneSlashCommands(c.slashCommands)
	c.mu.Unlock()
	return commands, known, nil
}

// ListSessions queries ACP session/list for the current cwd.
func (c *Client) ListSessions(ctx context.Context, req agents.SessionListRequest) (agents.SessionListResult, error) {
	if c == nil {
		return agents.SessionListResult{}, errors.New("codex: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	startCtx, cancel := context.WithTimeout(ctx, c.startTimeout)
	defer cancel()

	runtime, caps, err := c.startRuntime(startCtx)
	if err != nil {
		return agents.SessionListResult{}, err
	}
	defer runtime.Close()

	if !caps.CanList || !caps.CanLoad {
		return agents.SessionListResult{}, agents.ErrSessionListUnsupported
	}

	rawResult, err := c.listSessionsRaw(startCtx, runtime, req)
	if err != nil {
		return agents.SessionListResult{}, err
	}
	return normalizeCodexSessionListResult(rawResult), nil
}

func (c *Client) listSessionsRaw(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	req agents.SessionListRequest,
) (agents.SessionListResult, error) {
	if runtime == nil {
		return agents.SessionListResult{}, errors.New("codex: embedded runtime is nil")
	}

	params := map[string]any{
		"cwd":        codexSessionCWD(c, req.CWD),
		"mcpServers": []any{},
	}
	if cursor := strings.TrimSpace(req.Cursor); cursor != "" {
		params["cursor"] = cursor
	}

	result, err := c.clientRequest(ctx, runtime, "session/list", params)
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("codex: session/list: %w", err)
	}
	return acpsession.ParseSessionListResult(result.Result)
}

// SetConfigOption applies one ACP session config option and returns latest options.
func (c *Client) SetConfigOption(ctx context.Context, configID, value string) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("codex: nil client")
	}
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" {
		return nil, errors.New("codex: configID is required")
	}
	if value == "" {
		return nil, errors.New("codex: value is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runtime, sessionID, _, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, fmt.Errorf("codex: initialize runtime: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	resp, err := c.clientRequest(reqCtx, runtime, methodSessionSetConfigOption, map[string]any{
		"sessionId": sessionID,
		"configId":  configID,
		"value":     value,
	})
	if err != nil {
		return nil, fmt.Errorf("codex: session/set_config_option failed: %w", err)
	}

	options := acpmodel.ExtractConfigOptions(resp.Result)
	c.mu.Lock()
	c.configOptions = acpmodel.CloneConfigOptions(options)
	c.mu.Unlock()
	c.ApplyConfigOptionResult(configID, value, options)
	return acpmodel.CloneConfigOptions(options), nil
}

// Close closes the embedded runtime.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	runtime := c.runtime
	c.runtime = nil
	c.sessionID = ""
	c.runtimeSessionID = ""
	updateUnsub := c.updateUnsub
	c.updateUnsub = nil
	c.configOptions = nil
	c.canLoadSession = false
	c.slashCommands = nil
	c.slashCommandsKnown = false
	slashCommandsReady := c.slashCommandsReady
	c.slashCommandsReady = nil
	c.mu.Unlock()

	if updateUnsub != nil {
		updateUnsub()
	}
	closeReadySignal(slashCommandsReady)
	if runtime != nil {
		return runtime.Close()
	}
	return nil
}

// Stream sends one prompt to embedded runtime and emits deltas.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("codex: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("codex: onDelta callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		runtime, sessionID, stableSessionID, err := c.ensureInitialized(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return agents.StopReasonCancelled, nil
			}
			return agents.StopReasonEndTurn, fmt.Errorf("codex: initialize runtime: %w", err)
		}
		c.waitForInitialSlashCommands(ctx)
		if err := c.notifyCachedSlashCommands(ctx); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("codex: report slash commands: %w", err)
		}
		requestedSessionID := c.CurrentSessionID()
		deferInitialBinding := codexShouldDeferInitialSessionBinding(requestedSessionID, sessionID, stableSessionID)
		if c.supportsLoadSession() && !deferInitialBinding {
			if err := agents.NotifySessionBound(ctx, stableSessionID); err != nil {
				return agents.StopReasonEndTurn, fmt.Errorf("codex: report session bound: %w", err)
			}
		}

		stopReason, streamErr := c.streamOnce(ctx, runtime, sessionID, input, onDelta)
		if streamErr == nil {
			if c.supportsLoadSession() && requestedSessionID == "" {
				resolvedSessionID := c.resolveStableSessionIDAfterPrompt(ctx, runtime, sessionID, stableSessionID)
				c.setStableSessionID(resolvedSessionID)
				if deferInitialBinding || resolvedSessionID != stableSessionID {
					if err := agents.NotifySessionBound(ctx, resolvedSessionID); err != nil {
						return agents.StopReasonEndTurn, fmt.Errorf("codex: report session bound: %w", err)
					}
				}
			}
			return stopReason, nil
		}
		if !isRetryableTurnStartError(streamErr) || attempt == maxAttempts {
			return stopReason, streamErr
		}

		c.resetRuntime()
	}

	return agents.StopReasonEndTurn, errors.New("codex: retry loop exited unexpectedly")
}

func (c *Client) streamOnce(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	sessionID string,
	input string,
	onDelta func(delta string) error,
) (agents.StopReason, error) {
	updates, unsubscribe := runtime.SubscribeUpdates(256)
	defer unsubscribe()

	promptCtx, promptCancel := context.WithCancel(ctx)
	defer promptCancel()

	var stopWatchOnce sync.Once
	stopWatch := make(chan struct{})
	stopCancelWatcher := func() {
		stopWatchOnce.Do(func() {
			close(stopWatch)
		})
	}
	defer stopCancelWatcher()

	go func() {
		select {
		case <-promptCtx.Done():
			c.sendSessionCancel(runtime, sessionID)
		case <-stopWatch:
		}
	}()

	type promptResult struct {
		response codexacp.RPCMessage
		err      error
	}
	promptDone := make(chan promptResult, 1)
	go func() {
		resp, reqErr := c.clientRequest(promptCtx, runtime, methodSessionPrompt, map[string]any{
			"sessionId": sessionID,
			"prompt":    input,
		})
		promptDone <- promptResult{response: resp, err: reqErr}
	}()

	for {
		select {
		case <-ctx.Done():
			stopCancelWatcher()
			return agents.StopReasonCancelled, nil
		case result := <-promptDone:
			stopCancelWatcher()
			if result.err != nil {
				if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) || ctx.Err() != nil {
					return agents.StopReasonCancelled, nil
				}
				return agents.StopReasonEndTurn, fmt.Errorf("codex: session/prompt failed: %w", result.err)
			}

			stopReason, parseErr := parsePromptStopReason(result.response.Result)
			if parseErr != nil {
				return agents.StopReasonEndTurn, parseErr
			}
			if stopReason == "cancelled" {
				return agents.StopReasonCancelled, nil
			}
			return agents.StopReasonEndTurn, nil
		case msg, ok := <-updates:
			if !ok {
				stopCancelWatcher()
				if ctx.Err() != nil {
					return agents.StopReasonCancelled, nil
				}
				return agents.StopReasonEndTurn, errors.New("codex: embedded updates channel closed")
			}

			if err := c.handleUpdate(ctx, runtime, msg, onDelta); err != nil {
				stopCancelWatcher()
				return agents.StopReasonEndTurn, err
			}
		}
	}
}

func isRetryableTurnStartError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "session/prompt rpc error code=-32000 message=turn/start failed")
}

func (c *Client) resetRuntime() {
	c.mu.Lock()
	runtime := c.runtime
	c.runtime = nil
	c.sessionID = ""
	c.runtimeSessionID = ""
	updateUnsub := c.updateUnsub
	c.updateUnsub = nil
	c.configOptions = nil
	c.canLoadSession = false
	c.slashCommands = nil
	c.slashCommandsKnown = false
	slashCommandsReady := c.slashCommandsReady
	c.slashCommandsReady = nil
	c.mu.Unlock()

	if updateUnsub != nil {
		updateUnsub()
	}
	closeReadySignal(slashCommandsReady)
	if runtime != nil {
		_ = runtime.Close()
	}
}

func (c *Client) handleUpdate(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	msg codexacp.RPCMessage,
	onDelta func(delta string) error,
) error {
	observability.LogACPMessage(c.Name(), "inbound", msg)

	if msg.Method == methodSessionUpdate {
		update, err := agents.ParseACPUpdate(msg.Params)
		if err != nil {
			return fmt.Errorf("codex: %w", err)
		}
		switch update.Type {
		case agents.ACPUpdateTypeMessageChunk:
			if update.Delta == "" {
				return nil
			}
			if err := onDelta(update.Delta); err != nil {
				c.sendSessionCancel(runtime, c.currentSessionID())
				return err
			}
			return nil
		case agents.ACPUpdateTypePlan:
			handler, ok := agents.PlanHandlerFromContext(ctx)
			if !ok {
				return nil
			}
			if err := handler(ctx, update.PlanEntries); err != nil {
				c.sendSessionCancel(runtime, c.currentSessionID())
				return err
			}
		case agents.ACPUpdateTypeAvailableCommands:
			c.cacheSlashCommands(update.Commands)
			if err := agents.NotifySlashCommands(ctx, update.Commands); err != nil {
				c.sendSessionCancel(runtime, c.currentSessionID())
				return err
			}
		}
		return nil
	}

	if msg.Method == methodSessionRequestApproval {
		return c.handlePermissionRequest(ctx, runtime, msg)
	}

	if msg.Method != "" && msg.ID != nil {
		c.sendSessionCancel(runtime, c.currentSessionID())
		return fmt.Errorf("codex: unsupported embedded request method %q", msg.Method)
	}
	return nil
}

func (c *Client) handlePermissionRequest(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	msg codexacp.RPCMessage,
) error {
	if msg.ID == nil {
		return errors.New("codex: permission request missing id")
	}

	rawParams := map[string]any{}
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &rawParams); err != nil {
			return fmt.Errorf("codex: decode permission params: %w", err)
		}
	}

	request := agents.PermissionRequest{
		RequestID: idToString(*msg.ID),
		Approval:  mapString(rawParams, "approval"),
		Command:   mapString(rawParams, "command"),
		RawParams: rawParams,
	}

	outcome := agents.PermissionOutcomeDeclined
	if handler, ok := agents.PermissionHandlerFromContext(ctx); ok {
		resp, err := handler(ctx, request)
		if err == nil {
			switch resp.Outcome {
			case agents.PermissionOutcomeApproved, agents.PermissionOutcomeDeclined, agents.PermissionOutcomeCancelled:
				outcome = resp.Outcome
			}
		}
	}

	respondCtx, cancel := context.WithTimeout(context.Background(), c.requestTimeout)
	defer cancel()
	if err := runtime.RespondPermission(
		respondCtx,
		*msg.ID,
		codexacp.PermissionDecision{Outcome: string(outcome)},
	); err != nil {
		return fmt.Errorf("codex: respond permission outcome: %w", err)
	}
	observability.LogACPMessage(c.Name(), "outbound", map[string]any{
		"jsonrpc": jsonRPCVersion,
		"id":      *msg.ID,
		"result": map[string]any{
			"outcome": string(outcome),
		},
	})
	return nil
}

func (c *Client) sendSessionCancel(runtime *codexacp.EmbeddedRuntime, sessionID string) {
	if runtime == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), c.requestTimeout)
	defer cancel()
	_, _ = c.clientRequest(cancelCtx, runtime, methodSessionCancel, map[string]any{
		"sessionId": sessionID,
	})
}

func (c *Client) currentSessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtimeSessionID
}

func (c *Client) supportsLoadSession() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canLoadSession
}

func (c *Client) setStableSessionID(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}

	c.mu.Lock()
	c.sessionID = sessionID
	c.mu.Unlock()
	c.SetSessionID(sessionID)
}

func (c *Client) ensureInitialized(ctx context.Context) (*codexacp.EmbeddedRuntime, string, string, error) {
	c.initMu.Lock()
	defer c.initMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, "", "", errors.New("codex: client is closed")
	}
	if c.runtime != nil && c.runtimeSessionID != "" && c.sessionID != "" {
		runtime := c.runtime
		sessionID := c.runtimeSessionID
		stableSessionID := c.sessionID
		c.mu.Unlock()
		return runtime, sessionID, stableSessionID, nil
	}
	c.mu.Unlock()

	startCtx, cancel := context.WithTimeout(ctx, c.startTimeout)
	defer cancel()

	runtime, caps, err := c.startRuntime(startCtx)
	if err != nil {
		return nil, "", "", err
	}
	c.installUpdateMonitor(runtime)

	requestedSessionID := c.CurrentSessionID()
	sessionID := ""
	stableSessionID := ""
	configOptions := []agents.ConfigOption(nil)
	if requestedSessionID != "" {
		if !caps.CanLoad {
			c.clearUpdateMonitor()
			_ = runtime.Close()
			return nil, "", "", agents.ErrSessionLoadUnsupported
		}
		session, err := c.findSessionInRuntime(startCtx, runtime, c.Dir(), requestedSessionID)
		if err != nil {
			c.clearUpdateMonitor()
			_ = runtime.Close()
			return nil, "", "", err
		}
		sessionID = codexLoadSessionID(session)
		stableSessionID = session.SessionID
		if _, err := c.clientRequest(startCtx, runtime, "session/load", map[string]any{
			"sessionId":  sessionID,
			"cwd":        c.Dir(),
			"mcpServers": []any{},
		}); err != nil {
			c.clearUpdateMonitor()
			_ = runtime.Close()
			return nil, "", "", fmt.Errorf("codex: session/load failed: %w", err)
		}
	} else {
		newParams := map[string]any{
			"cwd": c.Dir(),
		}
		if modelID := c.CurrentModelID(); modelID != "" {
			newParams["model"] = modelID
		}
		sessionResp, err := c.clientRequest(startCtx, runtime, methodSessionNew, newParams)
		if err != nil {
			c.clearUpdateMonitor()
			_ = runtime.Close()
			return nil, "", "", err
		}

		sessionID, err = parseSessionID(sessionResp.Result)
		if err != nil {
			c.clearUpdateMonitor()
			_ = runtime.Close()
			return nil, "", "", err
		}
		stableSessionID, err = c.resolveStableSessionID(startCtx, runtime, sessionID)
		if err != nil {
			c.clearUpdateMonitor()
			_ = runtime.Close()
			return nil, "", "", err
		}
		configOptions = acpmodel.ExtractConfigOptions(sessionResp.Result)
	}
	if stableSessionID == "" {
		stableSessionID = sessionID
	}
	configOptions, err = c.applySessionSelections(startCtx, runtime, sessionID, configOptions)
	if err != nil {
		c.clearUpdateMonitor()
		_ = runtime.Close()
		return nil, "", "", err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.clearUpdateMonitor()
		_ = runtime.Close()
		return nil, "", "", errors.New("codex: client is closed")
	}
	if c.runtime != nil && c.runtimeSessionID != "" && c.sessionID != "" {
		existingRuntime := c.runtime
		existingSessionID := c.runtimeSessionID
		existingStableSessionID := c.sessionID
		c.mu.Unlock()
		c.clearUpdateMonitor()
		_ = runtime.Close()
		return existingRuntime, existingSessionID, existingStableSessionID, nil
	}

	c.runtime = runtime
	c.sessionID = stableSessionID
	c.runtimeSessionID = sessionID
	c.configOptions = acpmodel.CloneConfigOptions(configOptions)
	c.canLoadSession = caps.CanLoad
	c.mu.Unlock()
	return runtime, sessionID, stableSessionID, nil
}

func (c *Client) resolveStableSessionID(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	rawSessionID string,
) (string, error) {
	rawSessionID = strings.TrimSpace(rawSessionID)
	if rawSessionID == "" {
		return "", errors.New("codex: raw session id is required")
	}

	cursor := ""
	for {
		result, err := c.listSessionsRaw(ctx, runtime, agents.SessionListRequest{
			CWD:    c.Dir(),
			Cursor: cursor,
		})
		if err != nil {
			return "", err
		}
		for _, session := range result.Sessions {
			if strings.TrimSpace(session.SessionID) != rawSessionID {
				continue
			}
			stableSessionID := codexStableSessionID(session)
			if stableSessionID == "" {
				break
			}
			return stableSessionID, nil
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			break
		}
	}

	return rawSessionID, nil
}

func (c *Client) resolveStableSessionIDAfterPrompt(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	rawSessionID string,
	fallbackSessionID string,
) string {
	rawSessionID = strings.TrimSpace(rawSessionID)
	fallbackSessionID = strings.TrimSpace(fallbackSessionID)
	if rawSessionID == "" {
		return fallbackSessionID
	}
	if fallbackSessionID != "" && fallbackSessionID != rawSessionID {
		return fallbackSessionID
	}

	resolvedSessionID := fallbackSessionID
	for attempt := 0; attempt < stableSessionResolveRetries; attempt++ {
		nextSessionID, err := c.resolveStableSessionID(ctx, runtime, rawSessionID)
		if err == nil {
			nextSessionID = strings.TrimSpace(nextSessionID)
			if nextSessionID != "" {
				resolvedSessionID = nextSessionID
			}
			if nextSessionID != "" && nextSessionID != rawSessionID {
				return nextSessionID
			}
		}

		if attempt == stableSessionResolveRetries-1 {
			break
		}
		timer := time.NewTimer(stableSessionResolveDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if resolvedSessionID != "" {
				return resolvedSessionID
			}
			return rawSessionID
		case <-timer.C:
		}
	}

	if resolvedSessionID != "" {
		return resolvedSessionID
	}
	return rawSessionID
}

func (c *Client) applySessionSelections(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	sessionID string,
	options []agents.ConfigOption,
) ([]agents.ConfigOption, error) {
	current := options

	if modelID := strings.TrimSpace(c.CurrentModelID()); modelID != "" &&
		strings.TrimSpace(acpmodel.CurrentValueForConfig(current, "model")) != modelID {
		resp, err := c.clientRequest(ctx, runtime, methodSessionSetConfigOption, map[string]any{
			"sessionId": sessionID,
			"configId":  "model",
			"value":     modelID,
		})
		if err != nil {
			return nil, fmt.Errorf("codex: session/set_config_option(model) failed: %w", err)
		}
		if updated := acpmodel.ExtractConfigOptions(resp.Result); len(updated) > 0 {
			current = updated
		}
	}

	overrides := c.CurrentConfigOverrides()
	if len(overrides) == 0 {
		return current, nil
	}

	configIDs := make([]string, 0, len(overrides))
	for configID := range overrides {
		configIDs = append(configIDs, configID)
	}
	sort.Strings(configIDs)

	for _, configID := range configIDs {
		value := strings.TrimSpace(overrides[configID])
		if value == "" {
			continue
		}
		resp, err := c.clientRequest(ctx, runtime, methodSessionSetConfigOption, map[string]any{
			"sessionId": sessionID,
			"configId":  configID,
			"value":     value,
		})
		if err != nil {
			return nil, fmt.Errorf("codex: session/set_config_option(%s) failed: %w", configID, err)
		}
		if updated := acpmodel.ExtractConfigOptions(resp.Result); len(updated) > 0 {
			current = updated
		}
	}
	return current, nil
}

func (c *Client) startRuntime(
	ctx context.Context,
) (*codexacp.EmbeddedRuntime, acpsession.Capabilities, error) {
	runtime := codexacp.NewEmbeddedRuntime(c.runtimeConfig)
	if err := runtime.Start(context.Background()); err != nil {
		_ = runtime.Close()
		return nil, acpsession.Capabilities{}, err
	}

	initResp, err := c.clientRequest(ctx, runtime, methodInitialize, map[string]any{
		"client": map[string]any{
			"name": "ngent",
		},
	})
	if err != nil {
		_ = runtime.Close()
		return nil, acpsession.Capabilities{}, err
	}
	return runtime, acpsession.ParseInitializeCapabilities(initResp.Result), nil
}

func codexSessionCWD(c *Client, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return c.Dir()
}

func (c *Client) clientRequest(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	method string,
	params any,
) (codexacp.RPCMessage, error) {
	if runtime == nil {
		return codexacp.RPCMessage{}, errors.New("codex: embedded runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	id := c.nextRequestID()
	msg := codexacp.RPCMessage{
		JSONRPC: jsonRPCVersion,
		ID:      &id,
		Method:  method,
	}

	if params != nil {
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return codexacp.RPCMessage{}, fmt.Errorf("codex: marshal %s params: %w", method, err)
		}
		msg.Params = paramsJSON
	}
	observability.LogACPMessage(c.Name(), "outbound", msg)

	response, err := runtime.ClientRequest(ctx, msg)
	if err != nil {
		return codexacp.RPCMessage{}, err
	}
	observability.LogACPMessage(c.Name(), "inbound", response)
	if response.Error != nil {
		return codexacp.RPCMessage{}, fmt.Errorf(
			"codex: %s rpc error code=%d message=%s",
			method,
			response.Error.Code,
			strings.TrimSpace(response.Error.Message),
		)
	}
	return response, nil
}

func (c *Client) nextRequestID() json.RawMessage {
	id := atomic.AddUint64(&c.requestSeq, 1)
	raw := strconv.Quote(fmt.Sprintf("srv-%d", id))
	return json.RawMessage(raw)
}

func parseSessionID(raw json.RawMessage) (string, error) {
	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("codex: decode session/new result: %w", err)
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return "", errors.New("codex: session/new returned empty sessionId")
	}
	return sessionID, nil
}

func parsePromptStopReason(raw json.RawMessage) (string, error) {
	var payload struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("codex: decode session/prompt result: %w", err)
	}
	stopReason := strings.TrimSpace(payload.StopReason)
	if stopReason == "" {
		stopReason = string(agents.StopReasonEndTurn)
	}
	return stopReason, nil
}

func mapString(values map[string]any, key string) string {
	value, _ := values[key]
	text, _ := value.(string)
	return text
}

func idToString(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asNumber float64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return strconv.FormatFloat(asNumber, 'f', -1, 64)
	}
	return strings.TrimSpace(string(raw))
}

func (c *Client) installUpdateMonitor(runtime *codexacp.EmbeddedRuntime) {
	if runtime == nil {
		return
	}

	updates, unsubscribe := runtime.SubscribeUpdates(256)
	ready := make(chan struct{})

	c.mu.Lock()
	prevUnsub := c.updateUnsub
	prevReady := c.slashCommandsReady
	c.updateUnsub = unsubscribe
	c.slashCommands = nil
	c.slashCommandsKnown = false
	c.slashCommandsReady = ready
	c.mu.Unlock()

	if prevUnsub != nil {
		prevUnsub()
	}
	closeReadySignal(prevReady)

	go c.monitorUpdates(updates, ready)
}

func (c *Client) monitorUpdates(updates <-chan codexacp.RPCMessage, ready chan struct{}) {
	defer closeReadySignal(ready)

	for msg := range updates {
		if msg.Method != methodSessionUpdate || len(msg.Params) == 0 {
			continue
		}
		update, err := agents.ParseACPUpdate(msg.Params)
		if err != nil || update.Type != agents.ACPUpdateTypeAvailableCommands {
			continue
		}
		c.mu.Lock()
		if c.slashCommandsReady == ready {
			c.slashCommands = agents.CloneSlashCommands(update.Commands)
			c.slashCommandsKnown = true
		}
		c.mu.Unlock()
		closeReadySignal(ready)
	}
}

func (c *Client) clearUpdateMonitor() {
	c.mu.Lock()
	updateUnsub := c.updateUnsub
	c.updateUnsub = nil
	slashCommandsReady := c.slashCommandsReady
	c.slashCommandsReady = nil
	c.slashCommands = nil
	c.slashCommandsKnown = false
	c.mu.Unlock()

	if updateUnsub != nil {
		updateUnsub()
	}
	closeReadySignal(slashCommandsReady)
}

func (c *Client) cacheSlashCommands(commands []agents.SlashCommand) {
	c.mu.Lock()
	c.slashCommands = agents.CloneSlashCommands(commands)
	c.slashCommandsKnown = true
	c.mu.Unlock()
}

func (c *Client) waitForInitialSlashCommands(ctx context.Context) {
	c.mu.Lock()
	if c.slashCommandsKnown || c.slashCommandsReady == nil {
		c.mu.Unlock()
		return
	}
	ready := c.slashCommandsReady
	c.mu.Unlock()

	timer := time.NewTimer(initialSlashCommandsWait)
	defer timer.Stop()

	select {
	case <-ready:
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (c *Client) notifyCachedSlashCommands(ctx context.Context) error {
	c.mu.Lock()
	known := c.slashCommandsKnown
	commands := agents.CloneSlashCommands(c.slashCommands)
	c.mu.Unlock()
	if !known {
		return nil
	}
	return agents.NotifySlashCommands(ctx, commands)
}

func closeReadySignal(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case <-ch:
		return
	default:
		close(ch)
	}
}

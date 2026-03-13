package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beyond5959/acp-adapter/pkg/claudeacp"
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
)

// Config configures one embedded Claude runtime provider instance.
type Config struct {
	Dir             string
	ModelID         string
	SessionID       string
	ConfigOverrides map[string]string
	Name            string
	RuntimeConfig   claudeacp.RuntimeConfig
	StartTimeout    time.Duration
	RequestTimeout  time.Duration
}

// Client streams turn output through one in-process claude-acp runtime.
type Client struct {
	*agentutil.State

	name string

	runtimeConfig  claudeacp.RuntimeConfig
	startTimeout   time.Duration
	requestTimeout time.Duration

	initMu sync.Mutex
	mu     sync.Mutex
	closed bool

	runtime   *claudeacp.EmbeddedRuntime
	sessionID string

	configOptions  []agents.ConfigOption
	canLoadSession bool

	requestSeq uint64
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ io.Closer = (*Client)(nil)

// DefaultRuntimeConfig returns the default embedded Claude runtime configuration.
func DefaultRuntimeConfig() claudeacp.RuntimeConfig {
	return claudeacp.DefaultRuntimeConfig()
}

// Preflight checks whether the claude binary is available in PATH.
func Preflight() error {
	cfg := claudeacp.DefaultRuntimeConfig()
	bin := strings.TrimSpace(cfg.ClaudeBin)
	if bin == "" {
		bin = "claude"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("claude: binary %q not found in PATH: %w", bin, err)
	}
	return nil
}

// New constructs one embedded Claude provider.
func New(cfg Config) (*Client, error) {
	runtimeCfg := cfg.RuntimeConfig
	if strings.TrimSpace(runtimeCfg.ClaudeBin) == "" &&
		strings.TrimSpace(runtimeCfg.DefaultModel) == "" &&
		runtimeCfg.MaxTurns == 0 &&
		!runtimeCfg.SkipPerms &&
		strings.TrimSpace(runtimeCfg.AllowedTools) == "" &&
		!runtimeCfg.TraceJSON &&
		strings.TrimSpace(runtimeCfg.TraceJSONFile) == "" &&
		strings.TrimSpace(runtimeCfg.LogLevel) == "" &&
		strings.TrimSpace(runtimeCfg.PatchApplyMode) == "" &&
		len(runtimeCfg.Profiles) == 0 &&
		strings.TrimSpace(runtimeCfg.DefaultProfile) == "" {
		runtimeCfg = DefaultRuntimeConfig()
	}
	if err := Preflight(); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "claude-embedded"
	}

	startTimeout := cfg.StartTimeout
	if startTimeout <= 0 {
		startTimeout = defaultStartTimeout
	}
	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}

	state, err := agentutil.NewState("claude", agentutil.Config{
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
		return "claude-embedded"
	}
	return c.name
}

// ConfigOptions returns current ACP session config options.
func (c *Client) ConfigOptions(ctx context.Context) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("claude: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, _, err := c.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("claude: initialize runtime: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return acpmodel.CloneConfigOptions(c.configOptions), nil
}

// ListSessions queries ACP session/list for the current cwd.
func (c *Client) ListSessions(ctx context.Context, req agents.SessionListRequest) (agents.SessionListResult, error) {
	if c == nil {
		return agents.SessionListResult{}, errors.New("claude: nil client")
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

	params := map[string]any{
		"cwd":        claudeSessionCWD(c, req.CWD),
		"mcpServers": []any{},
	}
	if cursor := strings.TrimSpace(req.Cursor); cursor != "" {
		params["cursor"] = cursor
	}

	result, err := c.clientRequest(startCtx, runtime, "session/list", params)
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("claude: session/list: %w", err)
	}
	return acpsession.ParseSessionListResult(result.Result)
}

// SetConfigOption applies one ACP session config option and returns latest options.
func (c *Client) SetConfigOption(ctx context.Context, configID, value string) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("claude: nil client")
	}
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" {
		return nil, errors.New("claude: configID is required")
	}
	if value == "" {
		return nil, errors.New("claude: value is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runtime, sessionID, err := c.ensureInitialized(ctx)
	if err != nil {
		return nil, fmt.Errorf("claude: initialize runtime: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()
	resp, err := c.clientRequest(reqCtx, runtime, methodSessionSetConfigOption, map[string]any{
		"sessionId": sessionID,
		"configId":  configID,
		"value":     value,
	})
	if err != nil {
		return nil, fmt.Errorf("claude: session/set_config_option failed: %w", err)
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
	c.configOptions = nil
	c.canLoadSession = false
	c.mu.Unlock()

	if runtime != nil {
		return runtime.Close()
	}
	return nil
}

// Stream sends one prompt to the embedded Claude runtime and emits deltas.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("claude: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("claude: onDelta callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		runtime, sessionID, err := c.ensureInitialized(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return agents.StopReasonCancelled, nil
			}
			return agents.StopReasonEndTurn, fmt.Errorf("claude: initialize runtime: %w", err)
		}
		if c.supportsLoadSession() {
			if err := agents.NotifySessionBound(ctx, sessionID); err != nil {
				return agents.StopReasonEndTurn, fmt.Errorf("claude: report session bound: %w", err)
			}
		}

		stopReason, streamErr := c.streamOnce(ctx, runtime, sessionID, input, onDelta)
		if streamErr == nil {
			return stopReason, nil
		}
		if !isRetryableTurnStartError(streamErr) || attempt == maxAttempts {
			return stopReason, streamErr
		}

		c.resetRuntime()
	}

	return agents.StopReasonEndTurn, errors.New("claude: retry loop exited unexpectedly")
}

func (c *Client) streamOnce(
	ctx context.Context,
	runtime *claudeacp.EmbeddedRuntime,
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
		response claudeacp.RPCMessage
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
				return agents.StopReasonEndTurn, fmt.Errorf("claude: session/prompt failed: %w", result.err)
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
				return agents.StopReasonEndTurn, errors.New("claude: embedded updates channel closed")
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
	c.configOptions = nil
	c.mu.Unlock()

	if runtime != nil {
		_ = runtime.Close()
	}
}

func (c *Client) handleUpdate(
	ctx context.Context,
	runtime *claudeacp.EmbeddedRuntime,
	msg claudeacp.RPCMessage,
	onDelta func(delta string) error,
) error {
	observability.LogACPMessage(c.Name(), "inbound", msg)

	if msg.Method == methodSessionUpdate {
		update, err := agents.ParseACPUpdate(msg.Params)
		if err != nil {
			return fmt.Errorf("claude: %w", err)
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
		return fmt.Errorf("claude: unsupported embedded request method %q", msg.Method)
	}
	return nil
}

func (c *Client) handlePermissionRequest(
	ctx context.Context,
	runtime *claudeacp.EmbeddedRuntime,
	msg claudeacp.RPCMessage,
) error {
	if msg.ID == nil {
		return errors.New("claude: permission request missing id")
	}

	rawParams := map[string]any{}
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &rawParams); err != nil {
			return fmt.Errorf("claude: decode permission params: %w", err)
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
		claudeacp.PermissionDecision{Outcome: string(outcome)},
	); err != nil {
		return fmt.Errorf("claude: respond permission outcome: %w", err)
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

func (c *Client) sendSessionCancel(runtime *claudeacp.EmbeddedRuntime, sessionID string) {
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
	return c.sessionID
}

func (c *Client) supportsLoadSession() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canLoadSession
}

func (c *Client) ensureInitialized(ctx context.Context) (*claudeacp.EmbeddedRuntime, string, error) {
	c.initMu.Lock()
	defer c.initMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, "", errors.New("claude: client is closed")
	}
	if c.runtime != nil && c.sessionID != "" {
		runtime := c.runtime
		sessionID := c.sessionID
		c.mu.Unlock()
		return runtime, sessionID, nil
	}
	c.mu.Unlock()

	startCtx, cancel := context.WithTimeout(ctx, c.startTimeout)
	defer cancel()

	runtime, caps, err := c.startRuntime(startCtx)
	if err != nil {
		return nil, "", err
	}

	sessionID := c.CurrentSessionID()
	configOptions := []agents.ConfigOption(nil)
	if sessionID != "" {
		if !caps.CanLoad {
			_ = runtime.Close()
			return nil, "", agents.ErrSessionLoadUnsupported
		}
		if _, err := c.clientRequest(startCtx, runtime, "session/load", map[string]any{
			"sessionId":  sessionID,
			"cwd":        c.Dir(),
			"mcpServers": []any{},
		}); err != nil {
			_ = runtime.Close()
			return nil, "", fmt.Errorf("claude: session/load failed: %w", err)
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
			_ = runtime.Close()
			return nil, "", err
		}

		sessionID, err = parseSessionID(sessionResp.Result)
		if err != nil {
			_ = runtime.Close()
			return nil, "", err
		}
		configOptions = acpmodel.ExtractConfigOptions(sessionResp.Result)
	}
	configOptions, err = c.applySessionSelections(startCtx, runtime, sessionID, configOptions)
	if err != nil {
		_ = runtime.Close()
		return nil, "", err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = runtime.Close()
		return nil, "", errors.New("claude: client is closed")
	}
	if c.runtime != nil && c.sessionID != "" {
		_ = runtime.Close()
		return c.runtime, c.sessionID, nil
	}

	c.runtime = runtime
	c.sessionID = sessionID
	c.configOptions = acpmodel.CloneConfigOptions(configOptions)
	c.canLoadSession = caps.CanLoad
	return c.runtime, c.sessionID, nil
}

func (c *Client) applySessionSelections(
	ctx context.Context,
	runtime *claudeacp.EmbeddedRuntime,
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
			return nil, fmt.Errorf("claude: session/set_config_option(model) failed: %w", err)
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
			return nil, fmt.Errorf("claude: session/set_config_option(%s) failed: %w", configID, err)
		}
		if updated := acpmodel.ExtractConfigOptions(resp.Result); len(updated) > 0 {
			current = updated
		}
	}
	return current, nil
}

func (c *Client) startRuntime(
	ctx context.Context,
) (*claudeacp.EmbeddedRuntime, acpsession.Capabilities, error) {
	runtime := claudeacp.NewEmbeddedRuntime(c.runtimeConfig)
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

func claudeSessionCWD(c *Client, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return c.Dir()
}

func (c *Client) clientRequest(
	ctx context.Context,
	runtime *claudeacp.EmbeddedRuntime,
	method string,
	params any,
) (claudeacp.RPCMessage, error) {
	if runtime == nil {
		return claudeacp.RPCMessage{}, errors.New("claude: embedded runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	id := c.nextRequestID()
	msg := claudeacp.RPCMessage{
		JSONRPC: jsonRPCVersion,
		ID:      &id,
		Method:  method,
	}

	if params != nil {
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return claudeacp.RPCMessage{}, fmt.Errorf("claude: marshal %s params: %w", method, err)
		}
		msg.Params = paramsJSON
	}
	observability.LogACPMessage(c.Name(), "outbound", msg)

	response, err := runtime.ClientRequest(ctx, msg)
	if err != nil {
		return claudeacp.RPCMessage{}, err
	}
	observability.LogACPMessage(c.Name(), "inbound", response)
	if response.Error != nil {
		return claudeacp.RPCMessage{}, fmt.Errorf(
			"claude: %s rpc error code=%d message=%s",
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
		return "", fmt.Errorf("claude: decode session/new result: %w", err)
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		return "", errors.New("claude: session/new returned empty sessionId")
	}
	return sessionID, nil
}

func parsePromptStopReason(raw json.RawMessage) (string, error) {
	var payload struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("claude: decode session/prompt result: %w", err)
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

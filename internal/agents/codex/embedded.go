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

// Config configures one embedded codex runtime provider instance.
type Config struct {
	Dir             string
	ModelID         string
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

	runtime   *codexacp.EmbeddedRuntime
	sessionID string

	configOptions []agents.ConfigOption

	requestSeq uint64
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
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
	if _, _, err := c.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("codex: initialize runtime: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return acpmodel.CloneConfigOptions(c.configOptions), nil
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

	runtime, sessionID, err := c.ensureInitialized(ctx)
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
	c.configOptions = nil
	c.mu.Unlock()

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
		runtime, sessionID, err := c.ensureInitialized(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return agents.StopReasonCancelled, nil
			}
			return agents.StopReasonEndTurn, fmt.Errorf("codex: initialize runtime: %w", err)
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
	c.configOptions = nil
	c.mu.Unlock()

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
	return c.sessionID
}

func (c *Client) ensureInitialized(ctx context.Context) (*codexacp.EmbeddedRuntime, string, error) {
	c.initMu.Lock()
	defer c.initMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, "", errors.New("codex: client is closed")
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

	runtime := codexacp.NewEmbeddedRuntime(c.runtimeConfig)
	// Runtime lifecycle is controlled by client Close/reset, not startup timeout context.
	if err := runtime.Start(context.Background()); err != nil {
		_ = runtime.Close()
		return nil, "", err
	}

	if _, err := c.clientRequest(startCtx, runtime, methodInitialize, map[string]any{
		"client": map[string]any{
			"name": "ngent",
		},
	}); err != nil {
		_ = runtime.Close()
		return nil, "", err
	}

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

	sessionID, err := parseSessionID(sessionResp.Result)
	if err != nil {
		_ = runtime.Close()
		return nil, "", err
	}
	configOptions := acpmodel.ExtractConfigOptions(sessionResp.Result)
	configOptions, err = c.applyConfigOverrides(startCtx, runtime, sessionID, configOptions)
	if err != nil {
		_ = runtime.Close()
		return nil, "", err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = runtime.Close()
		return nil, "", errors.New("codex: client is closed")
	}
	if c.runtime != nil && c.sessionID != "" {
		_ = runtime.Close()
		return c.runtime, c.sessionID, nil
	}

	c.runtime = runtime
	c.sessionID = sessionID
	c.configOptions = acpmodel.CloneConfigOptions(configOptions)
	return c.runtime, c.sessionID, nil
}

func (c *Client) applyConfigOverrides(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	sessionID string,
	options []agents.ConfigOption,
) ([]agents.ConfigOption, error) {
	overrides := c.CurrentConfigOverrides()
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

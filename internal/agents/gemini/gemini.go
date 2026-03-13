package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	"github.com/beyond5959/ngent/internal/agents/acpsession"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
)

const (
	jsonRPCVersion               = "2.0"
	methodNotFound               = -32601
	methodSessionSetConfigOption = "session/set_config_option"
)

// Config configures the Gemini CLI ACP stdio provider.
type Config = agentutil.Config

// Client runs one gemini --experimental-acp process per Stream call.
type Client struct {
	*agentutil.State
	slashCommands agents.SlashCommandsCache
}

var _ agents.Streamer = (*Client)(nil)
var _ agents.ConfigOptionManager = (*Client)(nil)
var _ agents.SessionLister = (*Client)(nil)
var _ agents.SlashCommandsProvider = (*Client)(nil)

// New constructs a Gemini CLI ACP client.
func New(cfg Config) (*Client, error) {
	state, err := agentutil.NewState("gemini", cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		State: state,
	}, nil
}

// Preflight checks that the gemini binary is available in PATH.
func Preflight() error {
	return agentutil.PreflightBinary("gemini")
}

// Name returns the provider identifier.
func (c *Client) Name() string { return "gemini" }

// ConfigOptions queries ACP session config options.
func (c *Client) ConfigOptions(ctx context.Context) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("gemini: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.runConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), "", "")
}

// SlashCommands returns the latest slash-command snapshot for the current context.
func (c *Client) SlashCommands(ctx context.Context) ([]agents.SlashCommand, bool, error) {
	if c == nil {
		return nil, false, errors.New("gemini: nil client")
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
		return nil, errors.New("gemini: nil client")
	}
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" {
		return nil, errors.New("gemini: configID is required")
	}
	if value == "" {
		return nil, errors.New("gemini: value is required")
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
		return agents.SessionListResult{}, errors.New("gemini: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cliHome, err := makeCLIHome()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("gemini: session list create CLI home: %w", err)
	}
	defer os.RemoveAll(cliHome)

	cmd := exec.Command("gemini", "--experimental-acp")
	cmd.Env = buildGeminiCLIEnv(cliHome)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("gemini: session list open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("gemini: session list open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("gemini: session list open stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return agents.SessionListResult{}, fmt.Errorf("gemini: session list start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := newRPCConn(stdin, stdout)
	defer conn.Close()
	defer terminateProcess(cmd, errCh)

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
		return agents.SessionListResult{}, fmt.Errorf("gemini: session list initialize: %w", err)
	}

	caps := acpsession.ParseInitializeCapabilities(initResult)
	if !caps.CanList || !caps.CanLoad {
		return agents.SessionListResult{}, agents.ErrSessionListUnsupported
	}

	params := map[string]any{
		"cwd":        geminiSessionCWD(c, req.CWD),
		"mcpServers": []any{},
	}
	if cursor := strings.TrimSpace(req.Cursor); cursor != "" {
		params["cursor"] = cursor
	}
	result, err := conn.Call(ctx, "session/list", params)
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("gemini: session/list: %w", err)
	}
	return acpsession.ParseSessionListResult(result)
}

// Stream spawns gemini --experimental-acp, runs one turn, and streams deltas via onDelta.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("gemini: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("gemini: onDelta callback is required")
	}

	modelID := c.CurrentModelID()
	configOverrides := c.CurrentConfigOverrides()

	// Create a fresh GEMINI_CLI_HOME to prevent Gemini CLI from writing
	// interactive auth prompts to stdout, which would corrupt the ACP stream.
	// We mirror the user's auth type from ~/.gemini/settings.json so that
	// OAuth and API-key users both work without re-authenticating.
	cliHome, err := makeCLIHome()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("gemini: create CLI home: %w", err)
	}
	defer os.RemoveAll(cliHome)

	cmd := exec.Command("gemini", "--experimental-acp")
	cmd.Env = buildGeminiCLIEnv(cliHome)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("gemini: open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("gemini: open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("gemini: open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("gemini: start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := newRPCConn(stdin, stdout)
	defer conn.Close()
	defer terminateProcess(cmd, errCh)

	// 1. initialize
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
		return agents.StopReasonEndTurn, fmt.Errorf("gemini: initialize: %w", err)
	}
	caps := acpsession.ParseInitializeCapabilities(initResult)

	streamCtx := c.slashCommands.WrapContext(ctx)
	handleNotification, markPromptStarted := agents.NewACPNotificationHandler(streamCtx, onDelta)
	conn.SetNotificationHandler(func(msg rpcMessage) error {
		return handleNotification(msg.Method, msg.Params)
	})

	// 2. session/load or session/new
	sessionID := c.CurrentSessionID()
	initialOptions := []agents.ConfigOption(nil)
	if sessionID != "" {
		if !caps.CanLoad {
			return agents.StopReasonEndTurn, agents.ErrSessionLoadUnsupported
		}
		if _, err := conn.Call(ctx, "session/load", geminiSessionLoadParams(c, sessionID)); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("gemini: session/load: %w", err)
		}
	} else {
		newResult, err := conn.Call(ctx, "session/new", geminiSessionNewParams(c, modelID))
		if err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("gemini: session/new: %w", err)
		}
		sessionID = parseSessionID(newResult)
		if sessionID == "" {
			return agents.StopReasonEndTurn, errors.New("gemini: session/new returned empty sessionId")
		}
		initialOptions = acpmodel.ExtractConfigOptions(newResult)
	}
	if _, err := c.applyConfigOverrides(ctx, conn, sessionID, initialOptions, configOverrides); err != nil {
		return agents.StopReasonEndTurn, err
	}
	if caps.CanLoad {
		c.SetSessionID(sessionID)
		if err := agents.NotifySessionBound(streamCtx, sessionID); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("gemini: report session bound: %w", err)
		}
	}

	// 4. Wire permission requests: session/request_permission → PermissionHandler.
	permHandler, hasHandler := agents.PermissionHandlerFromContext(streamCtx)
	conn.SetRequestHandler(func(method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "session/request_permission" {
			return nil, &rpcError{Code: methodNotFound, Message: "method not found"}
		}

		var req struct {
			SessionID string         `json:"sessionId"`
			ToolCall  map[string]any `json:"toolCall"`
			Options   []any          `json:"options"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return buildPermResponse("cancelled")
		}

		if !hasHandler {
			return buildPermResponse("reject_once")
		}

		resp, err := permHandler(streamCtx, agents.PermissionRequest{
			Approval:  extractToolTitle(req.ToolCall),
			Command:   extractToolKind(req.ToolCall),
			RawParams: map[string]any{"sessionId": req.SessionID, "toolCall": req.ToolCall},
		})
		if err != nil {
			return buildPermResponse("reject_once")
		}
		switch resp.Outcome {
		case agents.PermissionOutcomeApproved:
			return buildPermResponse("allow_once")
		case agents.PermissionOutcomeCancelled:
			return buildPermResponse("cancelled")
		default:
			return buildPermResponse("reject_once")
		}
	})

	// 5. session/prompt — blocks until the model finishes or ctx is cancelled.
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
			c.sendCancel(conn, sessionID)
			return agents.StopReasonCancelled, nil
		}
		return agents.StopReasonEndTurn, fmt.Errorf("gemini: session/prompt: %w", err)
	}

	if parseStopReason(promptResult) == "cancelled" {
		return agents.StopReasonCancelled, nil
	}
	return agents.StopReasonEndTurn, nil
}

func (c *Client) sendCancel(conn *rpcConn, sessionID string) {
	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn.Notify(cancelCtx, "session/cancel", map[string]any{"sessionId": sessionID})
}

func geminiSessionCWD(c *Client, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return c.Dir()
}

func geminiSessionNewParams(c *Client, modelID string) map[string]any {
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

func geminiSessionLoadParams(c *Client, sessionID string) map[string]any {
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
	cliHome, err := makeCLIHome()
	if err != nil {
		return nil, fmt.Errorf("gemini: config options create CLI home: %w", err)
	}
	defer os.RemoveAll(cliHome)

	cmd := exec.Command("gemini", "--experimental-acp")
	cmd.Env = buildGeminiCLIEnv(cliHome)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("gemini: config options open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("gemini: config options open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("gemini: config options open stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("gemini: config options start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := newRPCConn(stdin, stdout)
	defer conn.Close()
	defer terminateProcess(cmd, errCh)

	if _, err := conn.Call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  false,
				"writeTextFile": false,
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("gemini: config options initialize: %w", err)
	}

	configCtx := c.slashCommands.WrapContext(ctx)
	handleNotification, _ := agents.NewACPNotificationHandler(configCtx, func(string) error { return nil })
	conn.SetNotificationHandler(func(msg rpcMessage) error {
		return handleNotification(msg.Method, msg.Params)
	})

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
		return nil, fmt.Errorf("gemini: config options session/new: %w", err)
	}
	sessionID := parseSessionID(newResult)
	if sessionID == "" {
		return nil, errors.New("gemini: config options session/new returned empty sessionId")
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
		return nil, fmt.Errorf("gemini: config options session/set_config_option: %w", err)
	}

	updated := acpmodel.ExtractConfigOptions(setResult)
	if len(updated) == 0 {
		return options, nil
	}
	return updated, nil
}

func (c *Client) applyConfigOverrides(
	ctx context.Context,
	conn *rpcConn,
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
			return nil, fmt.Errorf("gemini: session/set_config_option(%s): %w", configID, err)
		}
		if updated := acpmodel.ExtractConfigOptions(setResult); len(updated) > 0 {
			current = updated
		}
	}
	return current, nil
}

// buildPermResponse constructs a RequestPermissionResponse for the given optionId.
// Use "cancelled" to signal cancellation; use an optionId string ("allow_once",
// "reject_once", etc.) to signal a selected outcome.
func buildPermResponse(optionID string) (json.RawMessage, error) {
	var outcome map[string]any
	if optionID == "cancelled" {
		outcome = map[string]any{"outcome": "cancelled"}
	} else {
		outcome = map[string]any{"outcome": "selected", "optionId": optionID}
	}
	b, err := json.Marshal(map[string]any{"outcome": outcome})
	return b, err
}

func extractToolTitle(toolCall map[string]any) string {
	if toolCall == nil {
		return ""
	}
	if v, ok := toolCall["title"].(string); ok {
		return v
	}
	return ""
}

func extractToolKind(toolCall map[string]any) string {
	if toolCall == nil {
		return ""
	}
	if v, ok := toolCall["kind"].(string); ok {
		return v
	}
	return ""
}

// makeCLIHome creates a temporary GEMINI_CLI_HOME directory whose settings.json
// mirrors the user's configured auth type. This prevents Gemini CLI from writing
// interactive auth prompts to stdout during the ACP handshake, which would
// corrupt the JSON-RPC stream. Credential files (OAuth tokens, account records)
// are copied from the user's ~/.gemini so existing sessions remain valid.
func makeCLIHome() (string, error) {
	tmp, err := os.MkdirTemp("", "gemini-cli-home-*")
	if err != nil {
		return "", err
	}
	geminiDir := filepath.Join(tmp, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o700); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}

	userHome, _ := os.UserHomeDir()
	srcGeminiDir := filepath.Join(userHome, ".gemini")

	// Determine auth type from user's settings, defaulting to oauth-personal
	// (the standard `gemini auth login` flow used by most users).
	authType := readUserAuthType(srcGeminiDir)

	settings, _ := json.Marshal(map[string]any{
		"selectedAuthType": authType,
		"security":         map[string]any{"auth": map[string]any{"selectedType": authType}},
	})
	if err := os.WriteFile(filepath.Join(geminiDir, "settings.json"), settings, 0o600); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}

	// Copy OAuth credential files so existing login sessions remain valid.
	for _, name := range []string{"oauth_creds.json", "google_accounts.json"} {
		_ = copyFile(filepath.Join(srcGeminiDir, name), filepath.Join(geminiDir, name))
	}

	return tmp, nil
}

// readUserAuthType determines the auth type to configure in the temporary
// GEMINI_CLI_HOME. Priority:
//  1. Use ~/.gemini/settings.json explicit selection when present.
//  2. Otherwise, if GEMINI_API_KEY is present in env, use "gemini-api-key".
//  3. Fall back to "oauth-personal" (the default `gemini auth login` flow).
func readUserAuthType(geminiDir string) string {
	data, err := os.ReadFile(filepath.Join(geminiDir, "settings.json"))
	if err == nil {
		var cfg struct {
			SelectedAuthType string `json:"selectedAuthType"`
			Security         struct {
				Auth struct {
					SelectedType string `json:"selectedType"`
				} `json:"auth"`
			} `json:"security"`
		}
		if err := json.Unmarshal(data, &cfg); err == nil {
			if t := strings.TrimSpace(cfg.Security.Auth.SelectedType); t != "" {
				return t
			}
			if t := strings.TrimSpace(cfg.SelectedAuthType); t != "" {
				return t
			}
		}
	}

	if os.Getenv("GEMINI_API_KEY") != "" {
		return "gemini-api-key"
	}
	return "oauth-personal"
}

// copyFile copies src to dst, creating dst if it does not exist.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// appendOrReplace sets KEY=value in env, replacing an existing entry if present.
func appendOrReplace(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, len(env))
	copy(result, env)
	for i, e := range result {
		if strings.HasPrefix(e, prefix) {
			result[i] = prefix + value
			return result
		}
	}
	return append(result, prefix+value)
}

func buildGeminiCLIEnv(cliHome string) []string {
	env := appendOrReplace(os.Environ(), "GEMINI_CLI_HOME", cliHome)
	// Keep endpoint routing deterministic: explicit parent env must win.
	if value, ok := os.LookupEnv("GOOGLE_GEMINI_BASE_URL"); ok {
		env = appendOrReplace(env, "GOOGLE_GEMINI_BASE_URL", value)
	}
	return env
}

// ── JSON-RPC 2.0 transport ──────────────────────────────────────────────────

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

type rpcConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan rpcMessage
	nextID    int64

	notifMu sync.RWMutex
	notif   func(rpcMessage) error

	reqMu sync.RWMutex
	reqFn func(method string, params json.RawMessage) (json.RawMessage, error)

	closeOnce sync.Once
	done      chan struct{}
	doneErrMu sync.RWMutex
	doneErr   error
}

func newRPCConn(stdin io.WriteCloser, stdout io.ReadCloser) *rpcConn {
	c := &rpcConn{
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[string]chan rpcMessage),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *rpcConn) Close() { c.closeWithErr(io.EOF) }

func (c *rpcConn) SetNotificationHandler(fn func(rpcMessage) error) {
	c.notifMu.Lock()
	c.notif = fn
	c.notifMu.Unlock()
}

// SetRequestHandler registers a handler for inbound requests from the agent
// (e.g. session/request_permission). The handler returns either a result or
// an *rpcError. Any other error type is wrapped as an internal error response.
func (c *rpcConn) SetRequestHandler(fn func(method string, params json.RawMessage) (json.RawMessage, error)) {
	c.reqMu.Lock()
	c.reqFn = fn
	c.reqMu.Unlock()
}

func (c *rpcConn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal %s params: %w", method, err)
	}

	id := atomic.AddInt64(&c.nextID, 1)
	idRaw := json.RawMessage(strconv.AppendInt(nil, id, 10))
	idKey := string(idRaw)
	respCh := make(chan rpcMessage, 1)

	c.pendingMu.Lock()
	c.pending[idKey] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
	}()

	if err := c.write(rpcMessage{
		JSONRPC: jsonRPCVersion,
		ID:      idRaw,
		Method:  method,
		Params:  paramsJSON,
	}); err != nil {
		return nil, err
	}

	select {
	case <-c.done:
		if e := c.doneError(); e != nil && !errors.Is(e, io.EOF) {
			return nil, e
		}
		return nil, errors.New("gemini: connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-respCh:
		if !ok {
			return nil, errors.New("gemini: connection closed")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("gemini: rpc %s error (%d): %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// Notify sends a JSON-RPC notification (no id, no response expected).
func (c *rpcConn) Notify(_ context.Context, method string, params any) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return
	}
	_ = c.write(rpcMessage{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  paramsJSON,
	})
}

func (c *rpcConn) write(msg rpcMessage) error {
	wire, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("gemini: marshal rpc message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(wire); err != nil {
		return fmt.Errorf("gemini: write rpc: %w", err)
	}
	if _, err := c.stdin.Write([]byte("\n")); err != nil {
		return fmt.Errorf("gemini: write rpc delimiter: %w", err)
	}
	return nil
}

func (c *rpcConn) readLoop() {
	rd := bufio.NewReader(c.stdout)
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			if e := c.consume(line); e != nil {
				c.closeWithErr(e)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.closeWithErr(io.EOF)
			} else {
				c.closeWithErr(fmt.Errorf("gemini: read stdout: %w", err))
			}
			return
		}
	}
}

func (c *rpcConn) consume(line []byte) error {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return nil
	}
	// Gemini CLI may write non-JSON text (e.g. auth status messages) to stdout
	// before or alongside ACP messages. Find the start of the JSON object.
	start := bytes.IndexByte(line, '{')
	if start < 0 {
		return nil // no JSON object on this line, skip
	}
	line = line[start:]
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return fmt.Errorf("gemini: decode rpc line: %w", err)
	}

	// Response: has id, no method.
	if msg.Method == "" && len(msg.ID) > 0 {
		key := string(msg.ID)
		c.pendingMu.Lock()
		ch, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.pendingMu.Unlock()
		if ok {
			ch <- msg
		}
		return nil
	}

	// Notification: has method, no id.
	if msg.Method != "" && len(msg.ID) == 0 {
		c.notifMu.RLock()
		fn := c.notif
		c.notifMu.RUnlock()
		if fn != nil {
			return fn(msg)
		}
		return nil
	}

	// Inbound request from agent (method + id): handle via registered handler
	// or reply method-not-found.
	if msg.Method != "" && len(msg.ID) > 0 {
		c.reqMu.RLock()
		fn := c.reqFn
		c.reqMu.RUnlock()

		if fn != nil {
			result, err := fn(msg.Method, msg.Params)
			if err != nil {
				var rpcErr *rpcError
				if errors.As(err, &rpcErr) {
					return c.write(rpcMessage{
						JSONRPC: jsonRPCVersion,
						ID:      msg.ID,
						Error:   rpcErr,
					})
				}
				return c.write(rpcMessage{
					JSONRPC: jsonRPCVersion,
					ID:      msg.ID,
					Error:   &rpcError{Code: -32603, Message: err.Error()},
				})
			}
			return c.write(rpcMessage{
				JSONRPC: jsonRPCVersion,
				ID:      msg.ID,
				Result:  result,
			})
		}

		return c.write(rpcMessage{
			JSONRPC: jsonRPCVersion,
			ID:      msg.ID,
			Error:   &rpcError{Code: methodNotFound, Message: "method not found"},
		})
	}

	return nil
}

func (c *rpcConn) closeWithErr(err error) {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()

		c.doneErrMu.Lock()
		c.doneErr = err
		c.doneErrMu.Unlock()

		c.pendingMu.Lock()
		for k, ch := range c.pending {
			close(ch)
			delete(c.pending, k)
		}
		c.pendingMu.Unlock()

		close(c.done)
	})
}

func (c *rpcConn) doneError() error {
	c.doneErrMu.RLock()
	defer c.doneErrMu.RUnlock()
	return c.doneErr
}

// ── helpers ──────────────────────────────────────────────────────────────────

func terminateProcess(cmd *exec.Cmd, errCh <-chan error) {
	select {
	case <-time.After(2 * time.Second):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
	case <-errCh:
	}
}

func parseSessionID(raw json.RawMessage) string {
	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.SessionID)
}

func parseStopReason(raw json.RawMessage) string {
	var payload struct {
		StopReason string `json:"stopReason"`
	}
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.StopReason)
}

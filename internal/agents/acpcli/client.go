package acpcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	"github.com/beyond5959/ngent/internal/agents/acpsession"
	"github.com/beyond5959/ngent/internal/agents/acpstdio"
	"github.com/beyond5959/ngent/internal/agents/agentutil"
)

const methodSessionSetConfigOption = "session/set_config_option"

// OpenPurpose identifies the ACP workflow that is opening a provider process.
type OpenPurpose string

const (
	OpenPurposeStream         OpenPurpose = "stream"
	OpenPurposeSessionList    OpenPurpose = "session list"
	OpenPurposeConfigOptions  OpenPurpose = "config options"
	OpenPurposeDiscoverModels OpenPurpose = "discover models"
	OpenPurposeTranscript     OpenPurpose = "transcript"
)

// OpenConnRequest captures the current ACP connection request.
type OpenConnRequest struct {
	Purpose         OpenPurpose
	ModelID         string
	ConfigOverrides map[string]string
}

// ConfigSessionPlan lets providers customize config-option probing.
type ConfigSessionPlan struct {
	SessionModelID string
	SkipSetConfig  bool
}

// Hooks describes the provider-specific behavior layered onto the shared ACP driver.
type Hooks struct {
	OpenConn                func(ctx context.Context, req OpenConnRequest) (*acpstdio.Conn, func(), json.RawMessage, error)
	SessionNewParams        func(modelID string) map[string]any
	SessionLoadParams       func(sessionID string) map[string]any
	SessionListParams       func(cwd, cursor string) map[string]any
	PromptParams            func(sessionID, input, modelID string) map[string]any
	DiscoverModelsParams    func(modelID string) map[string]any
	PrepareConfigSession    func(modelID string, overrides map[string]string, configID, value string) ConfigSessionPlan
	SelectSessionModel      func(ctx context.Context, conn *acpstdio.Conn, sessionID, modelID string, options []agents.ConfigOption) ([]agents.ConfigOption, error)
	HandlePermissionRequest func(ctx context.Context, params json.RawMessage, handler agents.PermissionHandler, hasHandler bool) (json.RawMessage, error)
	Cancel                  func(conn *acpstdio.Conn, sessionID string)
}

// Client implements the shared ACP CLI lifecycle for built-in providers.
type Client struct {
	*agentutil.State

	provider      string
	hooks         Hooks
	slashCommands agents.SlashCommandsCache
}

// ModelDiscoverer describes the client capability needed by shared DiscoverModels helpers.
type ModelDiscoverer interface {
	DiscoverModels(ctx context.Context) ([]agents.ModelOption, error)
}

// New constructs one shared ACP CLI client for a provider.
func New(provider string, cfg agentutil.Config, hooks Hooks) (*Client, error) {
	if hooks.OpenConn == nil {
		return nil, fmt.Errorf("%s: OpenConn hook is required", strings.TrimSpace(provider))
	}
	if hooks.SessionNewParams == nil {
		return nil, fmt.Errorf("%s: SessionNewParams hook is required", strings.TrimSpace(provider))
	}
	if hooks.SessionLoadParams == nil {
		return nil, fmt.Errorf("%s: SessionLoadParams hook is required", strings.TrimSpace(provider))
	}
	if hooks.SessionListParams == nil {
		return nil, fmt.Errorf("%s: SessionListParams hook is required", strings.TrimSpace(provider))
	}
	if hooks.PromptParams == nil {
		return nil, fmt.Errorf("%s: PromptParams hook is required", strings.TrimSpace(provider))
	}

	state, err := agentutil.NewState(provider, cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		State:    state,
		provider: strings.TrimSpace(provider),
		hooks:    hooks,
	}, nil
}

// DiscoverModelsWithClient constructs one provider client and returns its discovered model options.
func DiscoverModelsWithClient[T ModelDiscoverer](
	ctx context.Context,
	newClient func() (T, error),
) ([]agents.ModelOption, error) {
	client, err := newClient()
	if err != nil {
		return nil, err
	}
	return client.DiscoverModels(ctx)
}

// SessionNewParams returns ACP session/new params for providers that only need cwd and optional model selection.
func SessionNewParams(dir string) func(string) map[string]any {
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

// DiscoverModelsParams returns ACP session/new params for model discovery without forcing a model selection.
func DiscoverModelsParams(dir string) func(string) map[string]any {
	return func(string) map[string]any {
		return map[string]any{
			"cwd":        strings.TrimSpace(dir),
			"mcpServers": []any{},
		}
	}
}

// SessionLoadParams returns ACP session/load params for providers that only need session id and cwd.
func SessionLoadParams(dir string) func(string) map[string]any {
	return func(sessionID string) map[string]any {
		return map[string]any{
			"sessionId":  strings.TrimSpace(sessionID),
			"cwd":        strings.TrimSpace(dir),
			"mcpServers": []any{},
		}
	}
}

// SessionListParams returns ACP session/list params for providers that only need cwd, optional cursor, and empty MCP servers.
func SessionListParams(dir string) func(string, string) map[string]any {
	return func(cwd, cursor string) map[string]any {
		params := map[string]any{
			"cwd":        SessionCWD(dir, cwd),
			"mcpServers": []any{},
		}
		if cursor = strings.TrimSpace(cursor); cursor != "" {
			params["cursor"] = cursor
		}
		return params
	}
}

// SessionCWD chooses the explicit session cwd when present, otherwise falling back to the provider default dir.
func SessionCWD(dir, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		return cwd
	}
	return strings.TrimSpace(dir)
}

// Name returns the provider identifier.
func (c *Client) Name() string {
	if c == nil || c.provider == "" {
		return "acp"
	}
	return c.provider
}

// ConfigOptions queries ACP session config options.
func (c *Client) ConfigOptions(ctx context.Context) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New("acp: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.RunConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), "", "")
}

// SlashCommands returns the latest slash-command snapshot for the current context.
func (c *Client) SlashCommands(ctx context.Context) ([]agents.SlashCommand, bool, error) {
	if c == nil {
		return nil, false, errors.New(c.nameForError() + ": nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if commands, known := c.slashCommands.Snapshot(); known {
		return commands, true, nil
	}
	if _, err := c.RunConfigSession(ctx, c.CurrentModelID(), c.CurrentConfigOverrides(), "", ""); err != nil {
		return nil, false, err
	}
	commands, known := c.slashCommands.Snapshot()
	return commands, known, nil
}

// SetConfigOption applies one ACP session config option.
func (c *Client) SetConfigOption(ctx context.Context, configID, value string) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New(c.nameForError() + ": nil client")
	}
	configID = strings.TrimSpace(configID)
	value = strings.TrimSpace(value)
	if configID == "" {
		return nil, errors.New(c.nameForError() + ": configID is required")
	}
	if value == "" {
		return nil, errors.New(c.nameForError() + ": value is required")
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

// ListSessions queries ACP session/list for the current cwd.
func (c *Client) ListSessions(ctx context.Context, req agents.SessionListRequest) (agents.SessionListResult, error) {
	if c == nil {
		return agents.SessionListResult{}, errors.New(c.nameForError() + ": nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	conn, cleanup, initResult, err := c.hooks.OpenConn(ctx, OpenConnRequest{
		Purpose:         OpenPurposeSessionList,
		ModelID:         c.CurrentModelID(),
		ConfigOverrides: c.CurrentConfigOverrides(),
	})
	if err != nil {
		return agents.SessionListResult{}, err
	}
	defer cleanup()

	caps := acpsession.ParseInitializeCapabilities(initResult)
	if !caps.CanList || !caps.CanLoad {
		return agents.SessionListResult{}, agents.ErrSessionListUnsupported
	}

	result, err := conn.Call(ctx, "session/list", c.hooks.SessionListParams(req.CWD, req.Cursor))
	if err != nil {
		return agents.SessionListResult{}, fmt.Errorf("%s: session/list: %w", c.nameForError(), err)
	}
	return acpsession.ParseSessionListResult(result)
}

// Stream runs one ACP turn and emits deltas via onDelta.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New(c.nameForError() + ": nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New(c.nameForError() + ": onDelta callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	modelID := c.CurrentModelID()
	configOverrides := c.CurrentConfigOverrides()
	conn, cleanup, initResult, err := c.hooks.OpenConn(ctx, OpenConnRequest{
		Purpose:         OpenPurposeStream,
		ModelID:         modelID,
		ConfigOverrides: configOverrides,
	})
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
		loadResult, err := conn.Call(ctx, "session/load", c.hooks.SessionLoadParams(sessionID))
		if err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("%s: session/load: %w", c.nameForError(), err)
		}
		initialOptions = acpmodel.ExtractConfigOptions(loadResult)
	} else {
		newResult, err := conn.Call(ctx, "session/new", c.hooks.SessionNewParams(modelID))
		if err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("%s: session/new: %w", c.nameForError(), err)
		}
		sessionID = acpstdio.ParseSessionID(newResult)
		if sessionID == "" {
			return agents.StopReasonEndTurn, errors.New(c.nameForError() + ": session/new returned empty sessionId")
		}
		initialOptions = acpmodel.ExtractConfigOptions(newResult)
	}

	if modelID != "" && c.hooks.SelectSessionModel != nil {
		selectedOptions, err := c.hooks.SelectSessionModel(ctx, conn, sessionID, modelID, initialOptions)
		if err != nil {
			return agents.StopReasonEndTurn, err
		}
		initialOptions = selectedOptions
	}
	initialOptions, err = c.applyConfigOverrides(ctx, conn, sessionID, initialOptions, configOverrides)
	if err != nil {
		return agents.StopReasonEndTurn, err
	}
	c.ApplyConfigOptionsSnapshot(initialOptions)
	if err := agents.NotifyConfigOptions(streamCtx, initialOptions); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("%s: report config options: %w", c.nameForError(), err)
	}
	if caps.CanLoad {
		c.SetSessionID(sessionID)
		if err := agents.NotifySessionBound(streamCtx, sessionID); err != nil {
			return agents.StopReasonEndTurn, fmt.Errorf("%s: report session bound: %w", c.nameForError(), err)
		}
	}

	if c.hooks.HandlePermissionRequest != nil {
		permHandler, hasPermHandler := agents.PermissionHandlerFromContext(streamCtx)
		conn.SetRequestHandler(func(method string, params json.RawMessage) (json.RawMessage, error) {
			if method != "session/request_permission" {
				return nil, &acpstdio.RPCError{Code: acpstdio.MethodNotFound, Message: "method not found"}
			}
			return c.hooks.HandlePermissionRequest(streamCtx, params, permHandler, hasPermHandler)
		})
	}

	stopCancelWatch := make(chan struct{})
	defer close(stopCancelWatch)
	if c.hooks.Cancel != nil {
		go func() {
			select {
			case <-ctx.Done():
				c.hooks.Cancel(conn, sessionID)
			case <-stopCancelWatch:
			}
		}()
	}

	markPromptStarted()
	promptResult, err := conn.Call(ctx, "session/prompt", c.hooks.PromptParams(sessionID, input, modelID))
	if err != nil {
		if ctx.Err() != nil {
			if c.hooks.Cancel != nil {
				c.hooks.Cancel(conn, sessionID)
			}
			return agents.StopReasonCancelled, nil
		}
		return agents.StopReasonEndTurn, fmt.Errorf("%s: session/prompt: %w", c.nameForError(), err)
	}
	if acpstdio.ParseStopReason(promptResult) == "cancelled" {
		return agents.StopReasonCancelled, nil
	}
	return agents.StopReasonEndTurn, nil
}

// DiscoverModels queries ACP model options through session/new.
func (c *Client) DiscoverModels(ctx context.Context) ([]agents.ModelOption, error) {
	if c == nil {
		return nil, errors.New(c.nameForError() + ": nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	modelID := c.CurrentModelID()
	configOverrides := c.CurrentConfigOverrides()
	conn, cleanup, _, err := c.hooks.OpenConn(ctx, OpenConnRequest{
		Purpose:         OpenPurposeDiscoverModels,
		ModelID:         modelID,
		ConfigOverrides: configOverrides,
	})
	if err != nil {
		return nil, err
	}
	defer cleanup()

	paramsFn := c.hooks.DiscoverModelsParams
	if paramsFn == nil {
		paramsFn = c.hooks.SessionNewParams
	}
	newResult, err := conn.Call(ctx, "session/new", paramsFn(modelID))
	if err != nil {
		return nil, fmt.Errorf("%s: discover models session/new: %w", c.nameForError(), err)
	}
	return acpmodel.ExtractModelOptions(newResult), nil
}

// LoadSessionTranscript replays one ACP session through session/load.
func (c *Client) LoadSessionTranscript(
	ctx context.Context,
	req agents.SessionTranscriptRequest,
) (agents.SessionTranscriptResult, error) {
	if c == nil {
		return agents.SessionTranscriptResult{}, errors.New(c.nameForError() + ": nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	session, err := agents.FindSessionByID(ctx, c, req.CWD, req.SessionID)
	if err != nil {
		return agents.SessionTranscriptResult{}, err
	}

	conn, cleanup, initResult, err := c.hooks.OpenConn(ctx, OpenConnRequest{
		Purpose:         OpenPurposeTranscript,
		ModelID:         c.CurrentModelID(),
		ConfigOverrides: c.CurrentConfigOverrides(),
	})
	if err != nil {
		return agents.SessionTranscriptResult{}, err
	}
	defer cleanup()

	caps := acpsession.ParseInitializeCapabilities(initResult)
	if !caps.CanLoad {
		return agents.SessionTranscriptResult{}, agents.ErrSessionLoadUnsupported
	}

	collector := agents.NewACPTranscriptCollector()
	conn.SetNotificationHandler(func(msg acpstdio.Message) error {
		if msg.Method != "session/update" || len(msg.Params) == 0 {
			return nil
		}
		return collector.HandleRawUpdate(msg.Params)
	})

	loadResult, err := conn.Call(ctx, "session/load", c.hooks.SessionLoadParams(session.SessionID))
	if err != nil {
		return agents.SessionTranscriptResult{}, fmt.Errorf("%s: session/load: %w", c.nameForError(), err)
	}
	result := collector.Result()
	result.ConfigOptions = acpmodel.ExtractConfigOptions(loadResult)
	return agents.CloneSessionTranscriptResult(result), nil
}

// RunConfigSession executes one ACP config query/update session.
func (c *Client) RunConfigSession(
	ctx context.Context,
	modelID string,
	configOverrides map[string]string,
	configID, value string,
) ([]agents.ConfigOption, error) {
	if c == nil {
		return nil, errors.New(c.nameForError() + ": nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	plan := ConfigSessionPlan{SessionModelID: modelID}
	if c.hooks.PrepareConfigSession != nil {
		plan = c.hooks.PrepareConfigSession(modelID, configOverrides, configID, value)
		if strings.TrimSpace(plan.SessionModelID) == "" {
			plan.SessionModelID = modelID
		}
	}

	conn, cleanup, _, err := c.hooks.OpenConn(ctx, OpenConnRequest{
		Purpose:         OpenPurposeConfigOptions,
		ModelID:         plan.SessionModelID,
		ConfigOverrides: configOverrides,
	})
	if err != nil {
		return nil, err
	}
	defer cleanup()

	configCtx := c.slashCommands.WrapContext(ctx)
	_ = agents.InstallACPStdioNotificationHandler(conn, configCtx, func(string) error { return nil })

	newResult, err := conn.Call(ctx, "session/new", c.hooks.SessionNewParams(plan.SessionModelID))
	if err != nil {
		return nil, fmt.Errorf("%s: config options session/new: %w", c.nameForError(), err)
	}
	sessionID := acpstdio.ParseSessionID(newResult)
	if sessionID == "" {
		return nil, errors.New(c.nameForError() + ": config options session/new returned empty sessionId")
	}

	options := acpmodel.ExtractConfigOptions(newResult)
	if strings.TrimSpace(plan.SessionModelID) != "" && c.hooks.SelectSessionModel != nil {
		options, err = c.hooks.SelectSessionModel(ctx, conn, sessionID, plan.SessionModelID, options)
		if err != nil {
			return nil, err
		}
	}
	options, err = c.applyConfigOverrides(ctx, conn, sessionID, options, configOverrides)
	if err != nil {
		return nil, err
	}
	if configID == "" || plan.SkipSetConfig {
		return options, nil
	}

	setResult, err := conn.Call(ctx, methodSessionSetConfigOption, map[string]any{
		"sessionId": sessionID,
		"configId":  configID,
		"value":     value,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: config options session/set_config_option: %w", c.nameForError(), err)
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
			return nil, fmt.Errorf("%s: session/set_config_option(%s): %w", c.nameForError(), configID, err)
		}
		if updated := acpmodel.ExtractConfigOptions(setResult); len(updated) > 0 {
			current = updated
		}
	}
	return current, nil
}

func (c *Client) nameForError() string {
	name := strings.TrimSpace(c.Name())
	if name == "" {
		return "acp"
	}
	return name
}

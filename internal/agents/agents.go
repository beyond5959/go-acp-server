package agents

import "context"

// ByteRange marks byte offsets for one referenced resource window.
type ByteRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// PromptContentBlock is one structured content block in a turn prompt.
type PromptContentBlock struct {
	Type     string     `json:"type,omitempty"`
	Text     string     `json:"text,omitempty"`
	Data     string     `json:"data,omitempty"`
	URI      string     `json:"uri,omitempty"`
	Path     string     `json:"path,omitempty"`
	Name     string     `json:"name,omitempty"`
	MimeType string     `json:"mimeType,omitempty"`
	Range    *ByteRange `json:"range,omitempty"`
}

// PromptResource is one file or URI reference attached to a turn prompt.
type PromptResource struct {
	Name     string     `json:"name,omitempty"`
	URI      string     `json:"uri,omitempty"`
	Path     string     `json:"path,omitempty"`
	MimeType string     `json:"mimeType,omitempty"`
	Text     string     `json:"text,omitempty"`
	Data     string     `json:"data,omitempty"`
	Range    *ByteRange `json:"range,omitempty"`
}

// TurnPromptConfig carries per-turn runtime overrides for the agent.
type TurnPromptConfig struct {
	Profile            string `json:"profile,omitempty"`
	ApprovalPolicy     string `json:"approvalPolicy,omitempty"`
	Sandbox            string `json:"sandbox,omitempty"`
	Personality        string `json:"personality,omitempty"`
	SystemInstructions string `json:"systemInstructions,omitempty"`
}

type turnContentContextKey struct{}
type turnResourcesContextKey struct{}
type turnPromptConfigContextKey struct{}

// WithTurnContent binds structured content blocks to the turn context.
func WithTurnContent(ctx context.Context, content []PromptContentBlock) context.Context {
	if len(content) == 0 {
		return ctx
	}
	return context.WithValue(ctx, turnContentContextKey{}, content)
}

// TurnContentFromContext retrieves content blocks from context, if present.
func TurnContentFromContext(ctx context.Context) []PromptContentBlock {
	if ctx == nil {
		return nil
	}
	content, _ := ctx.Value(turnContentContextKey{}).([]PromptContentBlock)
	return content
}

// WithTurnResources binds file/URI resources to the turn context.
func WithTurnResources(ctx context.Context, resources []PromptResource) context.Context {
	if len(resources) == 0 {
		return ctx
	}
	return context.WithValue(ctx, turnResourcesContextKey{}, resources)
}

// TurnResourcesFromContext retrieves resources from context, if present.
func TurnResourcesFromContext(ctx context.Context) []PromptResource {
	if ctx == nil {
		return nil
	}
	resources, _ := ctx.Value(turnResourcesContextKey{}).([]PromptResource)
	return resources
}

// WithTurnPromptConfig binds per-turn prompt config overrides to the context.
func WithTurnPromptConfig(ctx context.Context, cfg TurnPromptConfig) context.Context {
	return context.WithValue(ctx, turnPromptConfigContextKey{}, cfg)
}

// TurnPromptConfigFromContext retrieves the per-turn prompt config from context, if present.
func TurnPromptConfigFromContext(ctx context.Context) (TurnPromptConfig, bool) {
	if ctx == nil {
		return TurnPromptConfig{}, false
	}
	cfg, ok := ctx.Value(turnPromptConfigContextKey{}).(TurnPromptConfig)
	return cfg, ok
}

// AdapterInfo describes the ACP adapter identity returned on initialize.
type AdapterInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// StopReason represents why a streamed turn stopped.
type StopReason string

const (
	// StopReasonEndTurn means the stream finished normally.
	StopReasonEndTurn StopReason = "end_turn"
	// StopReasonCancelled means the stream was cancelled by context.
	StopReasonCancelled StopReason = "cancelled"
)

// Streamer emits message deltas until completion or cancellation.
type Streamer interface {
	Name() string
	Stream(ctx context.Context, input string, onDelta func(delta string) error) (StopReason, error)
}

// ModelOption describes one selectable model entry reported by an agent.
type ModelOption struct {
	ID   string
	Name string
}

// ConfigOptionValue is one selectable value for a session config option.
type ConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// ConfigOption describes one ACP session config option.
type ConfigOption struct {
	ID           string              `json:"id"`
	Category     string              `json:"category,omitempty"`
	Name         string              `json:"name,omitempty"`
	Description  string              `json:"description,omitempty"`
	Type         string              `json:"type,omitempty"`
	CurrentValue string              `json:"currentValue"`
	Options      []ConfigOptionValue `json:"options,omitempty"`
}

// ConfigOptionManager exposes ACP session config option querying/updating.
type ConfigOptionManager interface {
	ConfigOptions(ctx context.Context) ([]ConfigOption, error)
	SetConfigOption(ctx context.Context, configID, value string) ([]ConfigOption, error)
}

// ConfigOptionsHandler receives a config-options snapshot pushed during a turn.
type ConfigOptionsHandler func(ctx context.Context, options []ConfigOption) error

type configOptionsHandlerContextKey struct{}

// WithConfigOptionsHandler binds one per-turn config-options callback to context.
func WithConfigOptionsHandler(ctx context.Context, handler ConfigOptionsHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, configOptionsHandlerContextKey{}, handler)
}

// ConfigOptionsHandlerFromContext gets the config-options callback from context, if present.
func ConfigOptionsHandlerFromContext(ctx context.Context) (ConfigOptionsHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(configOptionsHandlerContextKey{}).(ConfigOptionsHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

// NotifyConfigOptions reports a config-options snapshot to the active callback, if any.
func NotifyConfigOptions(ctx context.Context, options []ConfigOption) error {
	handler, ok := ConfigOptionsHandlerFromContext(ctx)
	if !ok {
		return nil
	}
	return handler(ctx, options)
}

// PermissionOutcome is the client decision for one permission request.
type PermissionOutcome string

const (
	// PermissionOutcomeApproved allows the requested action.
	PermissionOutcomeApproved PermissionOutcome = "approved"
	// PermissionOutcomeDeclined denies the requested action (fail-closed default).
	PermissionOutcomeDeclined PermissionOutcome = "declined"
	// PermissionOutcomeCancelled cancels the requested action.
	PermissionOutcomeCancelled PermissionOutcome = "cancelled"
)

// PermissionRequest contains one provider-originated permission request.
type PermissionRequest struct {
	RequestID string
	// Approval is the permission kind: "command", "file", "network", or "mcp".
	Approval string
	Command  string
	// Files contains file paths affected by a file-kind approval.
	Files []string
	// Host, Protocol, Port describe the target for a network-kind approval.
	Host     string
	Protocol string
	Port     int
	// MCPServer and MCPTool identify the tool for an mcp-kind approval.
	MCPServer string
	MCPTool   string
	// Message is an optional human-readable description from the agent.
	Message   string
	RawParams map[string]any
}

// PermissionResponse returns the outcome back to the provider.
type PermissionResponse struct {
	Outcome PermissionOutcome
}

// PermissionHandler is called by providers when user approval is needed.
type PermissionHandler func(ctx context.Context, req PermissionRequest) (PermissionResponse, error)

type permissionHandlerContextKey struct{}

// WithPermissionHandler binds one per-turn permission callback to context.
func WithPermissionHandler(ctx context.Context, handler PermissionHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, permissionHandlerContextKey{}, handler)
}

// PermissionHandlerFromContext gets permission callback from context, if present.
func PermissionHandlerFromContext(ctx context.Context) (PermissionHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(permissionHandlerContextKey{}).(PermissionHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

// TodoItem is one structured checklist item emitted by an agent.
type TodoItem struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// TodoUpdateHandler receives agent TODO list updates for the active turn.
type TodoUpdateHandler func(ctx context.Context, items []TodoItem) error

type todoUpdateHandlerContextKey struct{}

// WithTodoUpdateHandler binds one per-turn todo callback to context.
func WithTodoUpdateHandler(ctx context.Context, handler TodoUpdateHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, todoUpdateHandlerContextKey{}, handler)
}

// TodoUpdateHandlerFromContext gets todo callback from context, if present.
func TodoUpdateHandlerFromContext(ctx context.Context) (TodoUpdateHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(todoUpdateHandlerContextKey{}).(TodoUpdateHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

// NotifyTodoUpdate dispatches todo items to the handler bound in ctx, if any.
func NotifyTodoUpdate(ctx context.Context, items []TodoItem) error {
	handler, ok := TodoUpdateHandlerFromContext(ctx)
	if !ok {
		return nil
	}
	return handler(ctx, items)
}

package agents

import "context"

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

// PromptStreamer emits message deltas from one structured ACP prompt payload.
type PromptStreamer interface {
	StreamPrompt(ctx context.Context, prompt Prompt, onDelta func(delta string) error) (StopReason, error)
}

// StreamPrompt dispatches a prompt through the richer PromptStreamer path when available.
func StreamPrompt(ctx context.Context, streamer Streamer, prompt Prompt, onDelta func(delta string) error) (StopReason, error) {
	if promptStreamer, ok := streamer.(PromptStreamer); ok {
		return promptStreamer.StreamPrompt(ctx, prompt, onDelta)
	}
	return streamer.Stream(ctx, prompt.LegacyText(), onDelta)
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

// PermissionOption is one selectable permission option advertised by a provider.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

// ConfigOptionManager exposes ACP session config option querying/updating.
type ConfigOptionManager interface {
	ConfigOptions(ctx context.Context) ([]ConfigOption, error)
	SetConfigOption(ctx context.Context, configID, value string) ([]ConfigOption, error)
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
	Approval  string
	Command   string
	Options   []PermissionOption
	RawParams map[string]any
}

// PermissionResponse returns the outcome back to the provider.
type PermissionResponse struct {
	Outcome          PermissionOutcome
	SelectedOptionID string
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

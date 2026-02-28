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

package agents

import "context"

// LifecycleHandler receives a lifecycle event type string for the active turn.
// The eventType matches one of the ACPUpdateType* lifecycle constants.
type LifecycleHandler func(ctx context.Context, eventType string) error

type lifecycleHandlerContextKey struct{}

// WithLifecycleHandler binds one per-turn lifecycle callback to context.
func WithLifecycleHandler(ctx context.Context, handler LifecycleHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, lifecycleHandlerContextKey{}, handler)
}

// LifecycleHandlerFromContext gets lifecycle callback from context, if present.
func LifecycleHandlerFromContext(ctx context.Context) (LifecycleHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(lifecycleHandlerContextKey{}).(LifecycleHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

// NotifyLifecycle reports a lifecycle event to the active callback, if any.
func NotifyLifecycle(ctx context.Context, eventType string) error {
	handler, ok := LifecycleHandlerFromContext(ctx)
	if !ok {
		return nil
	}
	return handler(ctx, eventType)
}

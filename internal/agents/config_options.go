package agents

import (
	"context"
	"strings"
)

// ConfigOptionsHandler receives one session config snapshot observed during a real turn.
type ConfigOptionsHandler func(ctx context.Context, options []ConfigOption) error

type configOptionsHandlerContextKey struct{}

// WithConfigOptionsHandler binds one config-options callback to context.
func WithConfigOptionsHandler(ctx context.Context, handler ConfigOptionsHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, configOptionsHandlerContextKey{}, handler)
}

// ConfigOptionsHandlerFromContext gets config-options callback from context, if present.
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

// NotifyConfigOptions reports one real session config snapshot to the active callback, if any.
func NotifyConfigOptions(ctx context.Context, options []ConfigOption) error {
	handler, ok := ConfigOptionsHandlerFromContext(ctx)
	if !ok || len(options) == 0 {
		return nil
	}
	return handler(ctx, CloneConfigOptions(options))
}

// CloneConfigOptions returns a trimmed deep copy of config options for safe sharing.
func CloneConfigOptions(options []ConfigOption) []ConfigOption {
	if len(options) == 0 {
		return nil
	}

	cloned := make([]ConfigOption, 0, len(options))
	for _, option := range options {
		id := strings.TrimSpace(option.ID)
		if id == "" {
			continue
		}

		copyOption := ConfigOption{
			ID:           id,
			Category:     strings.TrimSpace(option.Category),
			Name:         strings.TrimSpace(option.Name),
			Description:  strings.TrimSpace(option.Description),
			Type:         strings.TrimSpace(option.Type),
			CurrentValue: strings.TrimSpace(option.CurrentValue),
		}
		if len(option.Options) > 0 {
			copyOption.Options = make([]ConfigOptionValue, 0, len(option.Options))
			for _, value := range option.Options {
				trimmedValue := strings.TrimSpace(value.Value)
				if trimmedValue == "" {
					continue
				}
				copyOption.Options = append(copyOption.Options, ConfigOptionValue{
					Value:       trimmedValue,
					Name:        strings.TrimSpace(value.Name),
					Description: strings.TrimSpace(value.Description),
				})
			}
		}

		cloned = append(cloned, copyOption)
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

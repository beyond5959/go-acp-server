package agents

import (
	"context"
	"strings"
)

// SlashCommand describes one ACP slash command exposed by an agent.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputHint   string `json:"inputHint,omitempty"`
}

// SlashCommandsHandler receives the latest slash-command snapshot.
type SlashCommandsHandler func(ctx context.Context, commands []SlashCommand) error

// SlashCommandsProvider exposes the latest slash-command snapshot, if known.
type SlashCommandsProvider interface {
	SlashCommands(ctx context.Context) ([]SlashCommand, bool, error)
}

type slashCommandsHandlerContextKey struct{}

// WithSlashCommandsHandler binds one slash-command callback to context.
func WithSlashCommandsHandler(ctx context.Context, handler SlashCommandsHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, slashCommandsHandlerContextKey{}, handler)
}

// SlashCommandsHandlerFromContext gets slash-command callback from context, if present.
func SlashCommandsHandlerFromContext(ctx context.Context) (SlashCommandsHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(slashCommandsHandlerContextKey{}).(SlashCommandsHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

// NotifySlashCommands reports the latest slash commands to the active callback, if any.
func NotifySlashCommands(ctx context.Context, commands []SlashCommand) error {
	handler, ok := SlashCommandsHandlerFromContext(ctx)
	if !ok {
		return nil
	}
	return handler(ctx, CloneSlashCommands(commands))
}

// CloneSlashCommands returns a trimmed deduplicated deep copy of commands.
func CloneSlashCommands(commands []SlashCommand) []SlashCommand {
	if len(commands) == 0 {
		return nil
	}

	cloned := make([]SlashCommand, 0, len(commands))
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		cloned = append(cloned, SlashCommand{
			Name:        name,
			Description: strings.TrimSpace(command.Description),
			InputHint:   strings.TrimSpace(command.InputHint),
		})
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

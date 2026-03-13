package agents

import (
	"context"
	"sync"
)

// SlashCommandsCache keeps the latest provider-level slash-command snapshot.
type SlashCommandsCache struct {
	mu       sync.Mutex
	commands []SlashCommand
	known    bool
}

// Store saves the latest slash-command snapshot.
func (c *SlashCommandsCache) Store(commands []SlashCommand) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.commands = CloneSlashCommands(commands)
	c.known = true
	c.mu.Unlock()
}

// Snapshot returns the cached slash-command snapshot, if known.
func (c *SlashCommandsCache) Snapshot() ([]SlashCommand, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return CloneSlashCommands(c.commands), c.known
}

// WrapContext caches every slash-command update before forwarding it.
func (c *SlashCommandsCache) WrapContext(ctx context.Context) context.Context {
	if c == nil {
		return ctx
	}

	handler, hasHandler := SlashCommandsHandlerFromContext(ctx)
	return WithSlashCommandsHandler(ctx, func(commandsCtx context.Context, commands []SlashCommand) error {
		c.Store(commands)
		if !hasHandler {
			return nil
		}
		return handler(commandsCtx, commands)
	})
}

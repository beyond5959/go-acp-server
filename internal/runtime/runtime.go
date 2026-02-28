package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrActiveTurnExists means another turn is currently running on the thread.
	ErrActiveTurnExists = errors.New("runtime: active turn already exists for thread")
	// ErrTurnNotActive means the turn is not tracked as active.
	ErrTurnNotActive = errors.New("runtime: turn is not active")
)

type activeTurn struct {
	threadID string
	turnID   string
	cancel   context.CancelFunc
}

// TurnController manages active turn lifecycle and cancellation.
type TurnController struct {
	mu       sync.Mutex
	cond     *sync.Cond
	byThread map[string]activeTurn
	byTurn   map[string]activeTurn
}

// NewTurnController constructs a new active-turn controller.
func NewTurnController() *TurnController {
	controller := &TurnController{
		byThread: make(map[string]activeTurn),
		byTurn:   make(map[string]activeTurn),
	}
	controller.cond = sync.NewCond(&controller.mu)
	return controller
}

// Activate registers a running turn; one active turn is allowed per thread.
func (c *TurnController) Activate(threadID, turnID string, cancel context.CancelFunc) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.byThread[threadID]; exists {
		return ErrActiveTurnExists
	}

	entry := activeTurn{threadID: threadID, turnID: turnID, cancel: cancel}
	c.byThread[threadID] = entry
	c.byTurn[turnID] = entry
	return nil
}

// Release removes the running turn from controller maps.
func (c *TurnController) Release(threadID, turnID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.byTurn[turnID]
	if !ok {
		return
	}
	if entry.threadID != threadID {
		return
	}

	delete(c.byTurn, turnID)
	delete(c.byThread, threadID)
	c.cond.Broadcast()
}

// Cancel requests cancellation for an active turn.
func (c *TurnController) Cancel(turnID string) error {
	c.mu.Lock()
	entry, ok := c.byTurn[turnID]
	c.mu.Unlock()
	if !ok {
		return ErrTurnNotActive
	}

	if entry.cancel != nil {
		entry.cancel()
	}
	return nil
}

// IsThreadActive reports whether a thread has an active turn.
func (c *TurnController) IsThreadActive(threadID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.byThread[threadID]
	return ok
}

// ActiveCount returns currently active turn count.
func (c *TurnController) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byTurn)
}

// CancelAll requests cancellation for all active turns.
func (c *TurnController) CancelAll() int {
	c.mu.Lock()
	entries := make([]activeTurn, 0, len(c.byTurn))
	for _, entry := range c.byTurn {
		entries = append(entries, entry)
	}
	c.mu.Unlock()

	cancelled := 0
	for _, entry := range entries {
		if entry.cancel != nil {
			entry.cancel()
			cancelled++
		}
	}
	return cancelled
}

// WaitForIdle blocks until no active turns remain or context is cancelled.
func (c *TurnController) WaitForIdle(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		c.mu.Lock()
		idle := len(c.byTurn) == 0
		c.mu.Unlock()
		if idle {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

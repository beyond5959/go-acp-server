package agents

import (
	"context"
	"time"
)

const (
	defaultChunkSize = 3
	defaultDelay     = 20 * time.Millisecond
)

// FakeAgent is a deterministic streaming agent for local tests.
type FakeAgent struct {
	chunkSize int
	delay     time.Duration
}

// NewFakeAgent creates a fake streaming agent with defaults.
func NewFakeAgent() *FakeAgent {
	return &FakeAgent{
		chunkSize: defaultChunkSize,
		delay:     defaultDelay,
	}
}

// NewFakeAgentWithConfig creates a fake agent with custom pacing.
func NewFakeAgentWithConfig(chunkSize int, delay time.Duration) *FakeAgent {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	if delay < 10*time.Millisecond {
		delay = 10 * time.Millisecond
	}
	if delay > 50*time.Millisecond {
		delay = 50 * time.Millisecond
	}
	return &FakeAgent{chunkSize: chunkSize, delay: delay}
}

// Name returns the provider name.
func (a *FakeAgent) Name() string {
	return "fake"
}

// Stream emits input in chunks with a fixed delay.
func (a *FakeAgent) Stream(ctx context.Context, input string, onDelta func(delta string) error) (StopReason, error) {
	if a == nil {
		a = NewFakeAgent()
	}

	runes := []rune(input)
	if len(runes) == 0 {
		select {
		case <-ctx.Done():
			return StopReasonCancelled, nil
		default:
		}
		if err := onDelta(""); err != nil {
			return StopReasonEndTurn, err
		}
		return StopReasonEndTurn, nil
	}

	for start := 0; start < len(runes); start += a.chunkSize {
		end := start + a.chunkSize
		if end > len(runes) {
			end = len(runes)
		}

		select {
		case <-ctx.Done():
			return StopReasonCancelled, nil
		case <-time.After(a.delay):
		}

		select {
		case <-ctx.Done():
			return StopReasonCancelled, nil
		default:
		}

		if err := onDelta(string(runes[start:end])); err != nil {
			return StopReasonEndTurn, err
		}
	}

	return StopReasonEndTurn, nil
}

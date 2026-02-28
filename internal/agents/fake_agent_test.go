package agents

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFakeAgentStream(t *testing.T) {
	agent := NewFakeAgentWithConfig(2, 10*time.Millisecond)
	ctx := context.Background()

	parts := make([]string, 0)
	reason, err := agent.Stream(ctx, "abcdef", func(delta string) error {
		parts = append(parts, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() unexpected error: %v", err)
	}
	if reason != StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want %q", reason, StopReasonEndTurn)
	}
	if got, want := strings.Join(parts, ""), "abcdef"; got != want {
		t.Fatalf("joined delta = %q, want %q", got, want)
	}
}

func TestFakeAgentCancel(t *testing.T) {
	agent := NewFakeAgentWithConfig(1, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	count := 0
	reason, err := agent.Stream(ctx, strings.Repeat("x", 50), func(delta string) error {
		count++
		if count == 1 {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream() unexpected error: %v", err)
	}
	if reason != StopReasonCancelled {
		t.Fatalf("stop reason = %q, want %q", reason, StopReasonCancelled)
	}
}

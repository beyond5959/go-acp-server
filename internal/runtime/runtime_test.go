package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTurnControllerActivateCancelRelease(t *testing.T) {
	controller := NewTurnController()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := controller.Activate("th-1", "tu-1", cancel); err != nil {
		t.Fatalf("Activate() unexpected error: %v", err)
	}
	if !controller.IsThreadActive("th-1") {
		t.Fatalf("thread should be active")
	}

	if err := controller.Activate("th-1", "tu-2", cancel); !errors.Is(err, ErrActiveTurnExists) {
		t.Fatalf("second Activate() error = %v, want %v", err, ErrActiveTurnExists)
	}

	if err := controller.Cancel("tu-1"); err != nil {
		t.Fatalf("Cancel() unexpected error: %v", err)
	}

	controller.Release("th-1", "tu-1")
	if controller.IsThreadActive("th-1") {
		t.Fatalf("thread should be inactive after release")
	}

	if err := controller.Cancel("tu-1"); !errors.Is(err, ErrTurnNotActive) {
		t.Fatalf("Cancel() after release error = %v, want %v", err, ErrTurnNotActive)
	}

}

func TestTurnControllerWaitForIdleAndCancelAll(t *testing.T) {
	controller := NewTurnController()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := controller.Activate("th-1", "tu-1", cancel); err != nil {
		t.Fatalf("Activate() unexpected error: %v", err)
	}
	if got := controller.ActiveCount(); got != 1 {
		t.Fatalf("ActiveCount() = %d, want 1", got)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer waitCancel()
	if err := controller.WaitForIdle(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForIdle() error = %v, want %v", err, context.DeadlineExceeded)
	}

	cancelled := controller.CancelAll()
	if cancelled != 1 {
		t.Fatalf("CancelAll() = %d, want 1", cancelled)
	}

	controller.Release("th-1", "tu-1")
	waitCtx2, waitCancel2 := context.WithTimeout(context.Background(), 1*time.Second)
	defer waitCancel2()
	if err := controller.WaitForIdle(waitCtx2); err != nil {
		t.Fatalf("WaitForIdle() after release unexpected error: %v", err)
	}
}

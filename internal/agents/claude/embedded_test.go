package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// skipIfNoClaude skips the test when the claude binary is not in PATH.
func skipIfNoClaude(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude binary not in PATH")
	}
}

func TestPreflight_BinaryPresent(t *testing.T) {
	skipIfNoClaude(t)
	if err := Preflight(); err != nil {
		t.Fatalf("Preflight() unexpected error: %v", err)
	}
}

func TestPreflight_BinaryMissing(t *testing.T) {
	// Override PATH to an empty temp dir so 'claude' is not found.
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)
	t.Setenv("CLAUDE_BIN", "") // ensure DefaultRuntimeConfig uses PATH lookup
	err := Preflight()
	if err == nil {
		t.Fatal("expected error when claude binary is not in PATH")
	}
}

func TestNew_BinaryMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)
	t.Setenv("CLAUDE_BIN", "")
	_, err := New(Config{Dir: "/tmp"})
	if err == nil {
		t.Fatal("expected error when claude binary is missing")
	}
}

func TestNew_WithBinary(t *testing.T) {
	skipIfNoClaude(t)
	c, err := New(Config{
		Dir:  "/tmp",
		Name: "test-claude",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Name() != "test-claude" {
		t.Errorf("unexpected name: got %q, want %q", c.Name(), "test-claude")
	}
}

func TestNew_DefaultName(t *testing.T) {
	skipIfNoClaude(t)
	c, err := New(Config{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Name() != "claude-embedded" {
		t.Errorf("unexpected default name: got %q, want %q", c.Name(), "claude-embedded")
	}
}

func TestNew_DefaultTimeouts(t *testing.T) {
	skipIfNoClaude(t)
	c, err := New(Config{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.startTimeout != defaultStartTimeout {
		t.Errorf("unexpected startTimeout: got %v, want %v", c.startTimeout, defaultStartTimeout)
	}
	if c.requestTimeout != defaultRequestTimeout {
		t.Errorf("unexpected requestTimeout: got %v, want %v", c.requestTimeout, defaultRequestTimeout)
	}
}

func TestNew_CustomTimeouts(t *testing.T) {
	skipIfNoClaude(t)
	c, err := New(Config{
		Dir:            "/tmp",
		StartTimeout:   3 * time.Second,
		RequestTimeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.startTimeout != 3*time.Second {
		t.Errorf("unexpected startTimeout: got %v, want %v", c.startTimeout, 3*time.Second)
	}
	if c.requestTimeout != 20*time.Second {
		t.Errorf("unexpected requestTimeout: got %v, want %v", c.requestTimeout, 20*time.Second)
	}
}

func TestClose_Nil(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil {
		t.Errorf("unexpected error on nil Close: %v", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	skipIfNoClaude(t)
	c, err := New(Config{Dir: "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestDefaultRuntimeConfig_ClaudeBin(t *testing.T) {
	t.Setenv("CLAUDE_BIN", "")
	cfg := DefaultRuntimeConfig()
	// When CLAUDE_BIN is unset, ClaudeBin should default to "claude".
	if cfg.ClaudeBin != "claude" {
		t.Errorf("expected ClaudeBin=\"claude\", got %q", cfg.ClaudeBin)
	}
}

func TestDefaultRuntimeConfig_CustomBin(t *testing.T) {
	t.Setenv("CLAUDE_BIN", "/usr/local/bin/claude")
	cfg := DefaultRuntimeConfig()
	if cfg.ClaudeBin != "/usr/local/bin/claude" {
		t.Errorf("expected ClaudeBin from env, got %q", cfg.ClaudeBin)
	}
	_ = os.Unsetenv("CLAUDE_BIN")
}

// TestClaudeE2ESmoke performs a real turn using the embedded Claude runtime.
// Run with: E2E_CLAUDE=1 go test ./internal/agents/claude/ -run TestClaudeE2ESmoke -v -timeout 120s
func TestClaudeE2ESmoke(t *testing.T) {
	if os.Getenv("E2E_CLAUDE") != "1" {
		t.Skip("set E2E_CLAUDE=1 to run")
	}
	if err := Preflight(); err != nil {
		t.Skipf("claude not available: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	c, err := New(Config{Dir: cwd})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	var builder strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	reason, err := c.Stream(ctx, "Reply with exactly the word PONG and nothing else.", func(delta string) error {
		fmt.Print(delta)
		builder.WriteString(delta)
		return nil
	})
	fmt.Println()

	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	t.Logf("StopReason: %s", reason)
	t.Logf("Response: %q", builder.String())

	if reason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", reason, "end_turn")
	}
	if builder.Len() == 0 {
		t.Error("no response text received")
	}
}

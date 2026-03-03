package agentutil_test

import (
	"strings"
	"testing"

	"github.com/beyond5959/go-acp-server/internal/agents/agentutil"
)

func TestRequireDir(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		_, err := agentutil.RequireDir("qwen", "  ")
		if err == nil {
			t.Fatalf("RequireDir() error = nil, want non-nil")
		}
		if got, want := err.Error(), "qwen: Dir is required"; got != want {
			t.Fatalf("RequireDir() error = %q, want %q", got, want)
		}
	})

	t.Run("trimmed dir", func(t *testing.T) {
		got, err := agentutil.RequireDir("opencode", "  /tmp/workspace  ")
		if err != nil {
			t.Fatalf("RequireDir() unexpected error: %v", err)
		}
		if want := "/tmp/workspace"; got != want {
			t.Fatalf("RequireDir() dir = %q, want %q", got, want)
		}
	})
}

func TestPreflightBinary(t *testing.T) {
	t.Run("empty binary", func(t *testing.T) {
		err := agentutil.PreflightBinary(" ")
		if err == nil {
			t.Fatalf("PreflightBinary() error = nil, want non-nil")
		}
		if got, want := err.Error(), "binary name is required"; got != want {
			t.Fatalf("PreflightBinary() error = %q, want %q", got, want)
		}
	})

	t.Run("missing binary", func(t *testing.T) {
		err := agentutil.PreflightBinary("definitely-missing-binary-for-go-acp-server-tests")
		if err == nil {
			t.Fatalf("PreflightBinary() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "binary not found in PATH") {
			t.Fatalf("PreflightBinary() error = %q, want contains %q", err.Error(), "binary not found in PATH")
		}
	})
}

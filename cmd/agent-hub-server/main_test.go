package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/code-agent-hub-server/internal/runtime"
)

func TestValidateListenAddr(t *testing.T) {
	tests := []struct {
		name        string
		listenAddr  string
		allowPublic bool
		wantErr     bool
		wantPort    int
	}{
		{
			name:        "loopback_default_allowed",
			listenAddr:  "127.0.0.1:8686",
			allowPublic: false,
			wantErr:     false,
			wantPort:    8686,
		},
		{
			name:        "localhost_allowed",
			listenAddr:  "localhost:8080",
			allowPublic: false,
			wantErr:     false,
			wantPort:    8080,
		},
		{
			name:        "public_ipv4_denied_without_flag",
			listenAddr:  "0.0.0.0:8686",
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "public_ipv6_denied_without_flag",
			listenAddr:  "[::]:8686",
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "public_ipv4_allowed_with_flag",
			listenAddr:  "0.0.0.0:8686",
			allowPublic: true,
			wantErr:     false,
			wantPort:    8686,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotPort, err := validateListenAddr(tt.listenAddr, tt.allowPublic)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateListenAddr(%q, %v) error = nil, want non-nil", tt.listenAddr, tt.allowPublic)
				}
				return
			}

			if err != nil {
				t.Fatalf("validateListenAddr(%q, %v) unexpected error: %v", tt.listenAddr, tt.allowPublic, err)
			}
			if gotPort != tt.wantPort {
				t.Fatalf("port = %d, want %d", gotPort, tt.wantPort)
			}
		})
	}
}

func TestResolveAllowedRoots(t *testing.T) {
	t.Run("default to cwd when empty", func(t *testing.T) {
		roots, err := resolveAllowedRoots(nil)
		if err != nil {
			t.Fatalf("resolveAllowedRoots(nil) unexpected error: %v", err)
		}
		if got, want := len(roots), 1; got != want {
			t.Fatalf("len(roots) = %d, want %d", got, want)
		}
		if !filepath.IsAbs(roots[0]) {
			t.Fatalf("root %q is not absolute", roots[0])
		}
	})

	t.Run("reject non-absolute root", func(t *testing.T) {
		_, err := resolveAllowedRoots([]string{"relative/root"})
		if err == nil {
			t.Fatalf("resolveAllowedRoots should fail for non-absolute root")
		}
	})
}

func TestResolveCodexACPConfig(t *testing.T) {
	t.Run("empty bin means unconfigured", func(t *testing.T) {
		cfg, err := resolveCodexACPConfig("", "")
		if err != nil {
			t.Fatalf("resolveCodexACPConfig() unexpected error: %v", err)
		}
		if cfg.Bin != "" {
			t.Fatalf("cfg.Bin = %q, want empty", cfg.Bin)
		}
		if len(cfg.Args) != 0 {
			t.Fatalf("len(cfg.Args) = %d, want 0", len(cfg.Args))
		}
	})

	t.Run("reject non absolute bin", func(t *testing.T) {
		_, err := resolveCodexACPConfig("relative/codex-acp-go", "")
		if err == nil {
			t.Fatalf("resolveCodexACPConfig should fail for non-absolute bin path")
		}
	})

	t.Run("parse absolute bin and args", func(t *testing.T) {
		cfg, err := resolveCodexACPConfig("/tmp/codex-acp-go", "--foo bar --enable")
		if err != nil {
			t.Fatalf("resolveCodexACPConfig() unexpected error: %v", err)
		}
		if cfg.Bin != "/tmp/codex-acp-go" {
			t.Fatalf("cfg.Bin = %q, want %q", cfg.Bin, "/tmp/codex-acp-go")
		}
		if got, want := len(cfg.Args), 3; got != want {
			t.Fatalf("len(cfg.Args) = %d, want %d", got, want)
		}
	})
}

func TestSupportedAgentsCodexStatus(t *testing.T) {
	agentsWithoutBin := supportedAgents("")
	if len(agentsWithoutBin) == 0 {
		t.Fatalf("supportedAgents returned empty list")
	}
	if agentsWithoutBin[0].ID != "codex" {
		t.Fatalf("agents[0].ID = %q, want %q", agentsWithoutBin[0].ID, "codex")
	}
	if agentsWithoutBin[0].Status != "unconfigured" {
		t.Fatalf("codex status without bin = %q, want %q", agentsWithoutBin[0].Status, "unconfigured")
	}

	agentsWithBin := supportedAgents("/tmp/codex-acp-go")
	if agentsWithBin[0].Status != "available" {
		t.Fatalf("codex status with bin = %q, want %q", agentsWithBin[0].Status, "available")
	}
}

func TestGracefulShutdownForceCancelsTurns(t *testing.T) {
	controller := runtime.NewTurnController()
	cancelled := make(chan struct{}, 1)
	cancelFn := func() {
		select {
		case cancelled <- struct{}{}:
		default:
		}
	}

	if err := controller.Activate("th-1", "tu-1", cancelFn); err != nil {
		t.Fatalf("Activate() unexpected error: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gracefulShutdown(context.Background(), logger, &http.Server{}, controller, 50*time.Millisecond)

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatalf("turn cancel function was not called")
	}
}

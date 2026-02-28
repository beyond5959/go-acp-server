package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/beyond5959/go-acp-server/internal/runtime"
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

func TestSupportedAgentsCodexStatus(t *testing.T) {
	agentsUnavailable := supportedAgents(false)
	if len(agentsUnavailable) == 0 {
		t.Fatalf("supportedAgents returned empty list")
	}
	if agentsUnavailable[0].ID != "codex" {
		t.Fatalf("agents[0].ID = %q, want %q", agentsUnavailable[0].ID, "codex")
	}
	if agentsUnavailable[0].Status != "unavailable" {
		t.Fatalf("codex unavailable status = %q, want %q", agentsUnavailable[0].Status, "unavailable")
	}

	agentsAvailable := supportedAgents(true)
	if agentsAvailable[0].Status != "available" {
		t.Fatalf("codex available status = %q, want %q", agentsAvailable[0].Status, "available")
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

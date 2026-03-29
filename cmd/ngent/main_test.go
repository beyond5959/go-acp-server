package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/ngent/internal/observability"
	"github.com/beyond5959/ngent/internal/runtime"
)

func TestResolveListenAddr(t *testing.T) {
	tests := []struct {
		name           string
		port           int
		allowPublic    bool
		wantErr        bool
		wantPort       int
		wantListenAddr string
	}{
		{
			name:           "loopback_when_public_disabled",
			port:           8686,
			allowPublic:    false,
			wantErr:        false,
			wantPort:       8686,
			wantListenAddr: "127.0.0.1:8686",
		},
		{
			name:           "public_when_public_enabled",
			port:           8686,
			allowPublic:    true,
			wantErr:        false,
			wantPort:       8686,
			wantListenAddr: "0.0.0.0:8686",
		},
		{
			name:        "invalid_port_zero",
			port:        0,
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "invalid_port_too_high",
			port:        65536,
			allowPublic: false,
			wantErr:     true,
		},
		{
			name:        "invalid_port_negative",
			port:        -1,
			allowPublic: false,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotListenAddr, gotPort, err := resolveListenAddr(tt.port, tt.allowPublic)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveListenAddr(%d, %v) error = nil, want non-nil", tt.port, tt.allowPublic)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveListenAddr(%d, %v) unexpected error: %v", tt.port, tt.allowPublic, err)
			}
			if gotPort != tt.wantPort {
				t.Fatalf("port = %d, want %d", gotPort, tt.wantPort)
			}
			if gotListenAddr != tt.wantListenAddr {
				t.Fatalf("listenAddr = %q, want %q", gotListenAddr, tt.wantListenAddr)
			}
		})
	}
}

func TestLogStartupPreflight(t *testing.T) {
	t.Run("skip missing binary warning", func(t *testing.T) {
		var buf bytes.Buffer
		logger := observability.NewLoggerWithWriter(&buf, observability.LevelInfo)

		logStartupPreflight(logger, "startup.qwen_unavailable", &exec.Error{Name: "qwen", Err: exec.ErrNotFound})

		if got := strings.TrimSpace(buf.String()); got != "" {
			t.Fatalf("logStartupPreflight() wrote %q, want empty output", got)
		}
	})

	t.Run("warn on other preflight error", func(t *testing.T) {
		var buf bytes.Buffer
		logger := observability.NewLoggerWithWriter(&buf, observability.LevelInfo)

		logStartupPreflight(logger, "startup.qwen_unavailable", errors.New("permission denied"))

		got := buf.String()
		if !strings.Contains(got, "WARN:") {
			t.Fatalf("log output = %q, want WARN level", got)
		}
		if !strings.Contains(got, "startup.qwen_unavailable") {
			t.Fatalf("log output = %q, want startup event", got)
		}
		if !strings.Contains(got, `error="permission denied"`) {
			t.Fatalf("log output = %q, want error payload", got)
		}
	})
}

func TestResolveAllowedRoots(t *testing.T) {
	roots, err := resolveAllowedRoots()
	if err != nil {
		t.Fatalf("resolveAllowedRoots() unexpected error: %v", err)
	}
	if got, want := len(roots), 1; got != want {
		t.Fatalf("len(roots) = %d, want %d", got, want)
	}
	if !filepath.IsAbs(roots[0]) {
		t.Fatalf("root %q is not absolute", roots[0])
	}
}

func TestResolveModelDiscoveryDir(t *testing.T) {
	root := t.TempDir()
	if got := resolveModelDiscoveryDir([]string{root}); got != root {
		t.Fatalf("resolveModelDiscoveryDir() = %q, want %q", got, root)
	}

	t.Run("fallback to cwd when roots missing", func(t *testing.T) {
		got := resolveModelDiscoveryDir([]string{filepath.Join(root, "missing")})
		if strings.TrimSpace(got) == "" {
			t.Fatalf("resolveModelDiscoveryDir() returned empty path")
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("resolveModelDiscoveryDir() = %q, want absolute path", got)
		}
	})
}

func TestExtractConfigOverrides(t *testing.T) {
	got := extractConfigOverrides(`{
		"modelId":"gpt-5",
		"configOverrides":{
			"thought_level":"high",
			" empty ":" ",
			"non_string": 1
		}
	}`)
	if len(got) != 1 {
		t.Fatalf("len(configOverrides) = %d, want 1", len(got))
	}
	if got["thought_level"] != "high" {
		t.Fatalf("thought_level = %q, want %q", got["thought_level"], "high")
	}
}

func TestSupportedAgentsOnlyIncludesAvailableAgents(t *testing.T) {
	agentsUnavailable := supportedAgents(false, false, false, false, false, false, false, false)
	if got := len(agentsUnavailable); got != 0 {
		t.Fatalf("len(agentsUnavailable) = %d, want 0", got)
	}

	agentsSubset := supportedAgents(true, false, false, true, false, false, false, false)
	if got, want := len(agentsSubset), 2; got != want {
		t.Fatalf("len(agentsSubset) = %d, want %d", got, want)
	}
	if agentsSubset[0].ID != "codex" {
		t.Fatalf("agentsSubset[0].ID = %q, want %q", agentsSubset[0].ID, "codex")
	}
	if agentsSubset[0].Status != "available" {
		t.Fatalf("agentsSubset[0].Status = %q, want %q", agentsSubset[0].Status, "available")
	}
	if agentsSubset[1].ID != "kimi" {
		t.Fatalf("agentsSubset[1].ID = %q, want %q", agentsSubset[1].ID, "kimi")
	}
	if agentsSubset[1].Status != "available" {
		t.Fatalf("agentsSubset[1].Status = %q, want %q", agentsSubset[1].Status, "available")
	}

	agentsAvailable := supportedAgents(true, true, true, true, true, true, true, true)
	if got, want := len(agentsAvailable), 8; got != want {
		t.Fatalf("len(agentsAvailable) = %d, want %d", got, want)
	}
	for i, wantID := range []string{"codex", "claude", "gemini", "kimi", "qwen", "opencode", "blackbox", "cursor"} {
		if agentsAvailable[i].ID != wantID {
			t.Fatalf("agentsAvailable[%d].ID = %q, want %q", i, agentsAvailable[i].ID, wantID)
		}
		if agentsAvailable[i].Status != "available" {
			t.Fatalf("agentsAvailable[%d].Status = %q, want %q", i, agentsAvailable[i].Status, "available")
		}
	}
}

func TestResolveDefaultDataPath(t *testing.T) {
	const home = "/tmp/test-home-db-default"
	t.Setenv("HOME", home)

	got, err := resolveDefaultDataPath()
	if err != nil {
		t.Fatalf("resolveDefaultDataPath() unexpected error: %v", err)
	}

	want := filepath.Join(home, ".ngent")
	if got != want {
		t.Fatalf("resolveDefaultDataPath() = %q, want %q", got, want)
	}
}

func TestEnsureDataPath(t *testing.T) {
	t.Run("create nested dir", func(t *testing.T) {
		tmp := t.TempDir()
		dataPath := filepath.Join(tmp, "nested", "dir")
		if err := ensureDataPath(dataPath); err != nil {
			t.Fatalf("ensureDataPath(%q) unexpected error: %v", dataPath, err)
		}

		info, err := os.Stat(dataPath)
		if err != nil {
			t.Fatalf("os.Stat(%q): %v", dataPath, err)
		}
		if !info.IsDir() {
			t.Fatalf("path %q is not a directory", dataPath)
		}
	})

	t.Run("reject empty path", func(t *testing.T) {
		if err := ensureDataPath("   "); err == nil {
			t.Fatalf("ensureDataPath should fail for empty path")
		}
	})
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

	if err := controller.Activate("th-1", "ses-1", "tu-1", cancelFn); err != nil {
		t.Fatalf("Activate() unexpected error: %v", err)
	}

	logger := observability.NewLoggerWithWriter(io.Discard, observability.LevelInfo)
	gracefulShutdown(context.Background(), logger, &http.Server{}, controller, 50*time.Millisecond)

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatalf("turn cancel function was not called")
	}
}

func TestGetLANURLReturnsFalseForLoopback(t *testing.T) {
	url, ok := getLANURL("127.0.0.1:8686")
	if ok {
		t.Fatalf("getLANURL should return false for loopback")
	}
	if url != "" {
		t.Fatalf("expected empty URL for loopback, got %q", url)
	}
}

func TestPrintQRCodeDoesNothingForEmptyURL(t *testing.T) {
	var out bytes.Buffer
	printQRCode(&out, "")
	if got := out.String(); got != "" {
		t.Fatalf("printQRCode should write nothing for empty URL, got:\n%s", got)
	}
}

func TestPrintLogoSkipsANSIForNonTTYWriter(t *testing.T) {
	var out bytes.Buffer
	printLogo(&out)

	got := out.String()
	if got != startupLogoASCII {
		t.Fatalf("printLogo() = %q, want plain ASCII logo", got)
	}
	if strings.Contains(got, startupLogoANSIInk) {
		t.Fatalf("printLogo() unexpectedly emitted ANSI color: %q", got)
	}
}

func TestRenderStartupLogoWithANSI(t *testing.T) {
	got := renderStartupLogo(true)

	if !strings.Contains(got, startupLogoANSIInk) {
		t.Fatalf("renderStartupLogo(true) missing ink-green prefix: %q", got)
	}
	if !strings.Contains(got, startupLogoASCII) {
		t.Fatalf("renderStartupLogo(true) missing ASCII art body: %q", got)
	}
	if !strings.HasSuffix(got, startupLogoANSIReset) {
		t.Fatalf("renderStartupLogo(true) missing ANSI reset suffix: %q", got)
	}
}

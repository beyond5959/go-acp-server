package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/code-agent-hub-server/internal/agents"
	"github.com/example/code-agent-hub-server/internal/agents/acp"
	"github.com/example/code-agent-hub-server/internal/storage"
)

func TestE2ECodexSmoke(t *testing.T) {
	if strings.TrimSpace(os.Getenv("E2E_CODEX")) != "1" {
		t.Skip("skip codex smoke test: set E2E_CODEX=1 and CODEX_ACP_GO_BIN=/abs/path/to/codex-acp-go")
	}

	codexBin := strings.TrimSpace(os.Getenv("CODEX_ACP_GO_BIN"))
	if codexBin == "" {
		t.Skip("skip codex smoke test: E2E_CODEX=1 but CODEX_ACP_GO_BIN is empty")
	}
	if !filepath.IsAbs(codexBin) {
		t.Skipf("skip codex smoke test: CODEX_ACP_GO_BIN must be absolute, got %q", codexBin)
	}
	if _, err := os.Stat(codexBin); err != nil {
		t.Skipf("skip codex smoke test: CODEX_ACP_GO_BIN is not accessible: %v", err)
	}

	codexArgs := strings.Fields(strings.TrimSpace(os.Getenv("CODEX_ACP_GO_ARGS")))

	root := t.TempDir()
	h := newTestServer(t, testServerOptions{
		allowedRoots: []string{root},
		turnAgentFactory: func(thread storage.Thread) (agents.Streamer, error) {
			return acp.New(acp.Config{
				Command: codexBin,
				Args:    codexArgs,
				Dir:     thread.CWD,
				Name:    "codex-acp-go",
			})
		},
		permissionTimeout: 20 * time.Second,
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)

	streamResultCh := make(chan httpTurnStreamResult, 1)
	go func() {
		streamResultCh <- runTurnStreamRequest(t, ts.URL, "client-a", threadID, "Say hello in one short sentence.")
	}()

	deadline := time.After(90 * time.Second)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	resolved := make(map[string]struct{})
	var result httpTurnStreamResult
	waitDone := false
	for !waitDone {
		select {
		case result = <-streamResultCh:
			waitDone = true
		case <-ticker.C:
			history := getHistoryWithEventsHTTP(t, ts.URL, "client-a", threadID)
			if len(history.Turns) == 0 {
				continue
			}
			lastTurn := history.Turns[len(history.Turns)-1]
			for _, event := range lastTurn.Events {
				if event.Type != "permission_required" {
					continue
				}
				permissionID := stringField(event.Data, "permissionId")
				if permissionID == "" {
					continue
				}
				if _, ok := resolved[permissionID]; ok {
					continue
				}
				resolved[permissionID] = struct{}{}
				_, _ = postPermissionDecision(t, ts.URL, "client-a", permissionID, "approved")
			}
		case <-deadline:
			t.Fatalf("codex smoke turn timed out waiting for stream completion")
		}
	}

	if result.StatusCode != 200 {
		t.Fatalf("codex smoke turn status = %d, want 200, body=%s", result.StatusCode, result.Body)
	}

	events := parseSSEEvents(t, result.Body)
	deltaCount := 0
	for _, ev := range events {
		if ev.Event == "message_delta" && stringField(ev.Data, "delta") != "" {
			deltaCount++
		}
	}
	if deltaCount < 1 {
		t.Fatalf("codex smoke expected >=1 message_delta, got %d", deltaCount)
	}
}

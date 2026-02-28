package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beyond5959/go-acp-server/internal/agents"
	"github.com/beyond5959/go-acp-server/internal/agents/acp"
	runtimectl "github.com/beyond5959/go-acp-server/internal/runtime"
	"github.com/beyond5959/go-acp-server/internal/storage"
)

func TestHealthz(t *testing.T) {
	h := newTestServer(t, testServerOptions{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !body.OK {
		t.Fatalf("ok = %v, want true", body.OK)
	}
}

func TestRequestCompletionLogIncludesPathIPAndStatus(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	h := newTestServer(t, testServerOptions{logger: logger})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "198.51.100.23:53001"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	entry := map[string]any{}
	found := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		candidate := map[string]any{}
		if err := json.Unmarshal([]byte(line), &candidate); err != nil {
			continue
		}
		if candidate["msg"] == "http.request.completed" {
			entry = candidate
			found = true
		}
	}
	if !found {
		t.Fatalf("missing http.request.completed log entry, logs:\n%s", logBuf.String())
	}

	if got := fmt.Sprintf("%v", entry["method"]); got != http.MethodGet {
		t.Fatalf("log method = %q, want %q", got, http.MethodGet)
	}
	if got := fmt.Sprintf("%v", entry["path"]); got != "/healthz" {
		t.Fatalf("log path = %q, want %q", got, "/healthz")
	}
	if got := fmt.Sprintf("%v", entry["ip"]); got != "198.51.100.23" {
		t.Fatalf("log ip = %q, want %q", got, "198.51.100.23")
	}
	if got := int(entry["statusCode"].(float64)); got != http.StatusOK {
		t.Fatalf("log statusCode = %d, want %d", got, http.StatusOK)
	}
	if got := fmt.Sprintf("%v", entry["requestTime"]); strings.TrimSpace(got) == "" {
		t.Fatalf("log requestTime is empty")
	}
}

func TestV1Agents(t *testing.T) {
	h := newTestServer(t, testServerOptions{})

	req := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	req.Header.Set("X-Client-ID", "client-a")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rr.Code, http.StatusOK)
	}

	var body struct {
		Agents []AgentInfo `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(body.Agents) != 2 {
		t.Fatalf("len(agents) = %d, want 2", len(body.Agents))
	}
	if body.Agents[0].ID != "codex" {
		t.Fatalf("agents[0].id = %q, want %q", body.Agents[0].ID, "codex")
	}
	if got := body.Agents[0].Status; got != "available" && got != "unavailable" {
		t.Fatalf("agents[0].status = %q, want available|unavailable", got)
	}
	if body.Agents[0].Status == "unconfigured" {
		t.Fatalf("agents[0].status must not be unconfigured")
	}
}

func TestV1RequiresClientID(t *testing.T) {
	h := newTestServer(t, testServerOptions{})

	req := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rr.Code, http.StatusBadRequest)
	}

	assertErrorCode(t, rr.Body.Bytes(), "INVALID_ARGUMENT")
}

func TestV1AuthToggle(t *testing.T) {
	h := newTestServer(t, testServerOptions{authToken: "secret-token"})

	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthRR := httptest.NewRecorder()
	h.ServeHTTP(healthRR, healthReq)
	if healthRR.Code != http.StatusOK {
		t.Fatalf("status code for /healthz = %d, want %d", healthRR.Code, http.StatusOK)
	}

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	unauthorizedReq.Header.Set("X-Client-ID", "client-a")
	unauthorizedRR := httptest.NewRecorder()
	h.ServeHTTP(unauthorizedRR, unauthorizedReq)
	if unauthorizedRR.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want %d", unauthorizedRR.Code, http.StatusUnauthorized)
	}
	assertErrorCode(t, unauthorizedRR.Body.Bytes(), "UNAUTHORIZED")

	authorizedReq := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	authorizedReq.Header.Set("X-Client-ID", "client-a")
	authorizedReq.Header.Set("Authorization", "Bearer secret-token")
	authorizedRR := httptest.NewRecorder()
	h.ServeHTTP(authorizedRR, authorizedReq)
	if authorizedRR.Code != http.StatusOK {
		t.Fatalf("status with token = %d, want %d", authorizedRR.Code, http.StatusOK)
	}
}

func TestCreateThreadValidationCWDAbsolute(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})

	body := map[string]any{"agent": "codex", "cwd": "relative/path"}
	rr := performJSONRequest(t, h, http.MethodPost, "/v1/threads", body, map[string]string{"X-Client-ID": "client-a"})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	assertErrorCode(t, rr.Body.Bytes(), "INVALID_ARGUMENT")
}

func TestCreateThreadValidationCWDAllowedRoots(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})

	body := map[string]any{"agent": "codex", "cwd": filepath.Join(t.TempDir(), "other")}
	rr := performJSONRequest(t, h, http.MethodPost, "/v1/threads", body, map[string]string{"X-Client-ID": "client-a"})

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", rr.Code, http.StatusForbidden)
	}
	assertErrorCode(t, rr.Body.Bytes(), "FORBIDDEN")
}

func TestCreateThreadValidationAgentAllowlist(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})

	body := map[string]any{"agent": "unknown", "cwd": root}
	rr := performJSONRequest(t, h, http.MethodPost, "/v1/threads", body, map[string]string{"X-Client-ID": "client-a"})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want %d", rr.Code, http.StatusBadRequest)
	}
	assertErrorCode(t, rr.Body.Bytes(), "INVALID_ARGUMENT")
}

func TestThreadsCreateListGetHappyPath(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})

	body := map[string]any{
		"agent":        "codex",
		"cwd":          filepath.Join(root, "workspace"),
		"title":        "demo",
		"agentOptions": map[string]any{"mode": "safe"},
	}

	createRR := performJSONRequest(t, h, http.MethodPost, "/v1/threads", body, map[string]string{"X-Client-ID": "client-a"})
	if createRR.Code != http.StatusOK {
		t.Fatalf("create status code = %d, want %d", createRR.Code, http.StatusOK)
	}

	threadID := extractThreadID(t, createRR.Body.Bytes())

	listRR := performJSONRequest(t, h, http.MethodGet, "/v1/threads", nil, map[string]string{"X-Client-ID": "client-a"})
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status code = %d, want %d", listRR.Code, http.StatusOK)
	}

	var listBody struct {
		Threads []struct {
			ThreadID string `json:"threadId"`
			Agent    string `json:"agent"`
			CWD      string `json:"cwd"`
		} `json:"threads"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if got, want := len(listBody.Threads), 1; got != want {
		t.Fatalf("len(threads) = %d, want %d", got, want)
	}
	if listBody.Threads[0].ThreadID != threadID {
		t.Fatalf("listed threadId = %q, want %q", listBody.Threads[0].ThreadID, threadID)
	}

	getRR := performJSONRequest(t, h, http.MethodGet, "/v1/threads/"+threadID, nil, map[string]string{"X-Client-ID": "client-a"})
	if getRR.Code != http.StatusOK {
		t.Fatalf("get status code = %d, want %d", getRR.Code, http.StatusOK)
	}
}

func TestThreadAccessAcrossClientsReturnsNotFound(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})

	createRR := performJSONRequest(t, h, http.MethodPost, "/v1/threads", map[string]any{"agent": "codex", "cwd": root}, map[string]string{"X-Client-ID": "client-a"})
	if createRR.Code != http.StatusOK {
		t.Fatalf("create status code = %d, want %d", createRR.Code, http.StatusOK)
	}
	threadID := extractThreadID(t, createRR.Body.Bytes())

	getRR := performJSONRequest(t, h, http.MethodGet, "/v1/threads/"+threadID, nil, map[string]string{"X-Client-ID": "client-b"})
	if getRR.Code != http.StatusNotFound {
		t.Fatalf("cross-client get status code = %d, want %d", getRR.Code, http.StatusNotFound)
	}
	assertErrorCode(t, getRR.Body.Bytes(), "NOT_FOUND")
}

func TestTurnsSSEAndHistory(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})

	threadID := createThreadForClient(t, h, "client-a", root)

	turnRR := performJSONRequest(t, h, http.MethodPost, "/v1/threads/"+threadID+"/turns", map[string]any{
		"input":  "hello streaming world",
		"stream": true,
	}, map[string]string{"X-Client-ID": "client-a"})

	if turnRR.Code != http.StatusOK {
		t.Fatalf("turn status code = %d, want %d", turnRR.Code, http.StatusOK)
	}
	if got := turnRR.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}

	events := parseSSEEvents(t, turnRR.Body.String())
	if len(events) < 3 {
		t.Fatalf("events count = %d, want >=3", len(events))
	}

	turnID := ""
	deltas := make([]string, 0)
	seenStarted := false
	seenCompleted := false
	for _, ev := range events {
		switch ev.Event {
		case "turn_started":
			seenStarted = true
			turnID = stringField(ev.Data, "turnId")
		case "message_delta":
			deltas = append(deltas, stringField(ev.Data, "delta"))
		case "turn_completed":
			seenCompleted = true
			if got := stringField(ev.Data, "stopReason"); got != "end_turn" {
				t.Fatalf("turn_completed.stopReason = %q, want %q", got, "end_turn")
			}
		}
	}

	if !seenStarted {
		t.Fatalf("missing turn_started event")
	}
	if !seenCompleted {
		t.Fatalf("missing turn_completed event")
	}
	if len(deltas) < 1 {
		t.Fatalf("message_delta count = %d, want >=1", len(deltas))
	}
	if turnID == "" {
		t.Fatalf("turnId is empty")
	}

	historyRR := performJSONRequest(t, h, http.MethodGet, "/v1/threads/"+threadID+"/history?includeEvents=true", nil, map[string]string{"X-Client-ID": "client-a"})
	if historyRR.Code != http.StatusOK {
		t.Fatalf("history status code = %d, want %d", historyRR.Code, http.StatusOK)
	}

	var history struct {
		Turns []struct {
			TurnID       string `json:"turnId"`
			ResponseText string `json:"responseText"`
			StopReason   string `json:"stopReason"`
			Events       []struct {
				Type string `json:"type"`
			} `json:"events"`
		} `json:"turns"`
	}
	if err := json.Unmarshal(historyRR.Body.Bytes(), &history); err != nil {
		t.Fatalf("unmarshal history: %v", err)
	}
	if got, want := len(history.Turns), 1; got != want {
		t.Fatalf("len(history.turns) = %d, want %d", got, want)
	}

	if history.Turns[0].TurnID != turnID {
		t.Fatalf("history turnId = %q, want %q", history.Turns[0].TurnID, turnID)
	}
	if got, want := history.Turns[0].ResponseText, strings.Join(deltas, ""); got != want {
		t.Fatalf("history responseText = %q, want %q", got, want)
	}
	if got := history.Turns[0].StopReason; got != "end_turn" {
		t.Fatalf("history stopReason = %q, want %q", got, "end_turn")
	}
	if len(history.Turns[0].Events) < 3 {
		t.Fatalf("history events count = %d, want >=3", len(history.Turns[0].Events))
	}
}

func TestCodexTurnWorksWithoutBinaryPathConfig(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)
	result := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "hello without codex binary path config")
	if result.StatusCode != http.StatusOK {
		t.Fatalf("turn stream status = %d, want %d", result.StatusCode, http.StatusOK)
	}

	events := parseSSEEvents(t, result.Body)
	seenDelta := false
	seenCompleted := false
	for _, ev := range events {
		if ev.Event == "message_delta" && stringField(ev.Data, "delta") != "" {
			seenDelta = true
		}
		if ev.Event == "turn_completed" {
			seenCompleted = true
		}
	}
	if !seenDelta {
		t.Fatalf("expected at least one message_delta event")
	}
	if !seenCompleted {
		t.Fatalf("expected turn_completed event")
	}
}

func TestTurnCancel(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)

	streamResultCh := make(chan httpTurnStreamResult, 1)
	go func() {
		streamResultCh <- runTurnStreamRequest(t, ts.URL, "client-a", threadID, strings.Repeat("cancel-me-", 60))
	}()

	turnID := waitForTurnID(t, ts.URL, "client-a", threadID, 4*time.Second)
	if turnID == "" {
		t.Fatalf("failed to observe running turn before timeout")
	}

	cancelStatus, cancelBody := postCancel(t, ts.URL, "client-a", turnID)
	if cancelStatus != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d, body=%s", cancelStatus, http.StatusOK, cancelBody)
	}

	streamResult := <-streamResultCh
	if streamResult.StatusCode != http.StatusOK {
		t.Fatalf("turn stream status = %d, want %d", streamResult.StatusCode, http.StatusOK)
	}

	events := parseSSEEvents(t, streamResult.Body)
	lastCompletedReason := ""
	for _, ev := range events {
		if ev.Event == "turn_completed" {
			lastCompletedReason = stringField(ev.Data, "stopReason")
		}
	}
	if lastCompletedReason != "cancelled" {
		t.Fatalf("turn_completed.stopReason = %q, want %q", lastCompletedReason, "cancelled")
	}

	history := getHistoryHTTP(t, ts.URL, "client-a", threadID, false)
	if len(history.Turns) == 0 {
		t.Fatalf("history turns is empty")
	}
	if got := history.Turns[len(history.Turns)-1].StopReason; got != "cancelled" {
		t.Fatalf("history stopReason = %q, want %q", got, "cancelled")
	}
}

func TestTurnConflictSingleActiveTurnPerThread(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)

	streamResultCh := make(chan httpTurnStreamResult, 1)
	go func() {
		streamResultCh <- runTurnStreamRequest(t, ts.URL, "client-a", threadID, strings.Repeat("long-running-", 50))
	}()

	turnID := waitForTurnID(t, ts.URL, "client-a", threadID, 4*time.Second)
	if turnID == "" {
		t.Fatalf("failed to observe running turn before timeout")
	}

	conflictStatus, conflictBody := postTurnRequest(t, ts.URL, "client-a", threadID, "second turn")
	if conflictStatus != http.StatusConflict {
		t.Fatalf("second turn status = %d, want %d, body=%s", conflictStatus, http.StatusConflict, conflictBody)
	}
	assertErrorCode(t, []byte(conflictBody), "CONFLICT")

	cancelStatus, cancelBody := postCancel(t, ts.URL, "client-a", turnID)
	if cancelStatus != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d, body=%s", cancelStatus, http.StatusOK, cancelBody)
	}

	streamResult := <-streamResultCh
	if streamResult.StatusCode != http.StatusOK {
		t.Fatalf("first turn status = %d, want %d", streamResult.StatusCode, http.StatusOK)
	}
}

func TestTurnAgentFactoryIsLazy(t *testing.T) {
	root := t.TempDir()
	var factoryCalls atomic.Int32
	h := newTestServer(t, testServerOptions{
		allowedRoots: []string{root},
		turnAgentFactory: func(thread storage.Thread) (agents.Streamer, error) {
			factoryCalls.Add(1)
			if thread.CWD != root {
				t.Fatalf("thread cwd = %q, want %q", thread.CWD, root)
			}
			return agents.NewFakeAgentWithConfig(3, 10*time.Millisecond), nil
		},
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)
	if got := factoryCalls.Load(); got != 0 {
		t.Fatalf("factory calls after thread creation = %d, want 0", got)
	}

	streamResult := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "hello from lazy factory")
	if streamResult.StatusCode != http.StatusOK {
		t.Fatalf("turn stream status = %d, want %d", streamResult.StatusCode, http.StatusOK)
	}

	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("factory calls after first turn = %d, want 1", got)
	}
}

func TestAgentIdleTTLReclaimsThreadAgent(t *testing.T) {
	root := t.TempDir()
	streamer := &countingClosableStreamer{}
	h := newTestServer(t, testServerOptions{
		allowedRoots: []string{root},
		agentIdleTTL: 200 * time.Millisecond,
		turnAgentFactory: func(thread storage.Thread) (agents.Streamer, error) {
			_ = thread
			return streamer, nil
		},
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)
	result := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "ttl check")
	if result.StatusCode != http.StatusOK {
		t.Fatalf("turn stream status = %d, want %d", result.StatusCode, http.StatusOK)
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if streamer.CloseCount() > 0 {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("agent was not reclaimed by idle TTL")
}

func TestMultiThreadParallelTurns(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID1 := createThreadHTTP(t, ts.URL, "client-a", root)
	threadID2 := createThreadHTTP(t, ts.URL, "client-a", root)

	ch1 := make(chan httpTurnStreamResult, 1)
	ch2 := make(chan httpTurnStreamResult, 1)
	go func() {
		ch1 <- runTurnStreamRequest(t, ts.URL, "client-a", threadID1, strings.Repeat("thread-one-", 30))
	}()
	go func() {
		ch2 <- runTurnStreamRequest(t, ts.URL, "client-a", threadID2, strings.Repeat("thread-two-", 30))
	}()

	r1 := <-ch1
	r2 := <-ch2
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("thread1 turn status = %d, want %d", r1.StatusCode, http.StatusOK)
	}
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("thread2 turn status = %d, want %d", r2.StatusCode, http.StatusOK)
	}
}

func TestTurnReturnsUpstreamUnavailableWhenProviderFails(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{
		allowedRoots: []string{root},
		turnAgentFactory: func(thread storage.Thread) (agents.Streamer, error) {
			_ = thread
			return nil, errors.New("provider unavailable")
		},
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)
	status, body := postTurnRequest(t, ts.URL, "client-a", threadID, "hello")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("turn status = %d, want %d, body=%s", status, http.StatusServiceUnavailable, body)
	}
	assertErrorCode(t, []byte(body), "UPSTREAM_UNAVAILABLE")
}

func TestCompactTimeoutCode(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{
		allowedRoots: []string{root},
		turnAgentFactory: func(thread storage.Thread) (agents.Streamer, error) {
			_ = thread
			return &errorStreamer{err: context.DeadlineExceeded}, nil
		},
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)
	status, body := doJSON(
		t,
		http.MethodPost,
		ts.URL+"/v1/threads/"+threadID+"/compact",
		map[string]any{"maxSummaryChars": 120},
		map[string]string{"X-Client-ID": "client-a"},
	)
	if status != http.StatusGatewayTimeout {
		t.Fatalf("compact status = %d, want %d, body=%s", status, http.StatusGatewayTimeout, body)
	}
	assertErrorCode(t, []byte(body), "TIMEOUT")
}

func TestTurnPermissionRequiredSSEEvent(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{
		allowedRoots:      []string{root},
		agent:             newFakeACPStreamer(t),
		permissionTimeout: 300 * time.Millisecond,
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)
	streamResult := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "permission please")
	if streamResult.StatusCode != http.StatusOK {
		t.Fatalf("turn stream status = %d, want %d", streamResult.StatusCode, http.StatusOK)
	}

	events := parseSSEEvents(t, streamResult.Body)
	seenPermissionRequired := false
	for _, ev := range events {
		if ev.Event != "permission_required" {
			continue
		}
		seenPermissionRequired = true
		if got := stringField(ev.Data, "permissionId"); got == "" {
			t.Fatalf("permission_required.permissionId is empty")
		}
		if got := stringField(ev.Data, "approval"); got != "command" {
			t.Fatalf("permission_required.approval = %q, want %q", got, "command")
		}
	}
	if !seenPermissionRequired {
		t.Fatalf("missing permission_required SSE event")
	}
}

func TestTurnPermissionApprovedContinuesAndCompletes(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{
		allowedRoots:      []string{root},
		agent:             newFakeACPStreamer(t),
		permissionTimeout: 2 * time.Second,
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)

	streamResultCh := make(chan httpTurnStreamResult, 1)
	go func() {
		streamResultCh <- runTurnStreamRequest(t, ts.URL, "client-a", threadID, "need approval")
	}()

	permissionID := waitForPermissionID(t, ts.URL, "client-a", threadID, 4*time.Second)
	if permissionID == "" {
		t.Fatalf("failed to observe permission_required before timeout")
	}

	permissionStatus, permissionBody := postPermissionDecision(t, ts.URL, "client-a", permissionID, "approved")
	if permissionStatus != http.StatusOK {
		t.Fatalf("permission decision status = %d, want %d, body=%s", permissionStatus, http.StatusOK, permissionBody)
	}

	streamResult := <-streamResultCh
	if streamResult.StatusCode != http.StatusOK {
		t.Fatalf("turn stream status = %d, want %d", streamResult.StatusCode, http.StatusOK)
	}

	events := parseSSEEvents(t, streamResult.Body)
	lastStopReason := ""
	for _, ev := range events {
		if ev.Event == "turn_completed" {
			lastStopReason = stringField(ev.Data, "stopReason")
		}
	}
	if lastStopReason != "end_turn" {
		t.Fatalf("turn_completed.stopReason = %q, want %q", lastStopReason, "end_turn")
	}

	history := getHistoryHTTP(t, ts.URL, "client-a", threadID, false)
	if len(history.Turns) == 0 {
		t.Fatalf("history turns is empty")
	}
	lastTurn := history.Turns[len(history.Turns)-1]
	if lastTurn.StopReason != "end_turn" {
		t.Fatalf("history stopReason = %q, want %q", lastTurn.StopReason, "end_turn")
	}
	if !strings.Contains(lastTurn.ResponseText, "after-permission") {
		t.Fatalf("history responseText = %q, want substring %q", lastTurn.ResponseText, "after-permission")
	}
}

func TestTurnPermissionTimeoutFailClosed(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{
		allowedRoots:      []string{root},
		agent:             newFakeACPStreamer(t),
		permissionTimeout: 250 * time.Millisecond,
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)
	streamResult := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "timeout permission")
	if streamResult.StatusCode != http.StatusOK {
		t.Fatalf("turn stream status = %d, want %d", streamResult.StatusCode, http.StatusOK)
	}

	events := parseSSEEvents(t, streamResult.Body)
	lastStopReason := ""
	for _, ev := range events {
		if ev.Event == "turn_completed" {
			lastStopReason = stringField(ev.Data, "stopReason")
		}
	}
	if lastStopReason != "cancelled" {
		t.Fatalf("turn_completed.stopReason = %q, want %q", lastStopReason, "cancelled")
	}

	history := getHistoryHTTP(t, ts.URL, "client-a", threadID, false)
	if len(history.Turns) == 0 {
		t.Fatalf("history turns is empty")
	}
	if got := history.Turns[len(history.Turns)-1].StopReason; got != "cancelled" {
		t.Fatalf("history stopReason = %q, want %q", got, "cancelled")
	}
}

func TestTurnPermissionSSEDisconnectFailClosed(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{
		allowedRoots:      []string{root},
		agent:             newFakeACPStreamer(t),
		permissionTimeout: 500 * time.Millisecond,
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)

	resp, cancelStream := startTurnStreamHTTP(t, ts.URL, "client-a", threadID, "disconnect before permission decision")
	eventsCh, doneCh := streamSSEEvents(resp.Body)

	var permissionID string
	deadline := time.After(4 * time.Second)
waitLoop:
	for {
		select {
		case ev, ok := <-eventsCh:
			if !ok {
				break waitLoop
			}
			if ev.Event == "permission_required" {
				permissionID = stringField(ev.Data, "permissionId")
				break waitLoop
			}
		case err := <-doneCh:
			t.Fatalf("stream ended before permission_required: %v", err)
		case <-deadline:
			t.Fatalf("timeout waiting for permission_required event")
		}
	}
	if permissionID == "" {
		t.Fatalf("permission_required.permissionId is empty")
	}

	cancelStream()
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close stream response body: %v", err)
	}
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("stream reader did not exit after disconnect")
	}

	status, stopReason := waitForTerminalTurn(t, ts.URL, "client-a", threadID, 4*time.Second)
	if status == "" {
		t.Fatalf("turn did not reach terminal status after disconnect")
	}
	if stopReason != "cancelled" {
		t.Fatalf("turn status/stopReason = %q/%q, want stopReason %q", status, stopReason, "cancelled")
	}
}

func TestInjectedPromptIncludesSummaryAndRecent(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)

	first := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "first user question")
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first turn status = %d, want %d", first.StatusCode, http.StatusOK)
	}

	second := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "second user question")
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second turn status = %d, want %d", second.StatusCode, http.StatusOK)
	}

	history := getHistoryHTTP(t, ts.URL, "client-a", threadID, false)
	if got, want := len(history.Turns), 2; got != want {
		t.Fatalf("len(history.turns) = %d, want %d", got, want)
	}

	secondResp := history.Turns[len(history.Turns)-1].ResponseText
	if !strings.Contains(secondResp, "[Conversation Summary]") {
		t.Fatalf("injected prompt missing summary section")
	}
	if !strings.Contains(secondResp, "[Recent Turns]") {
		t.Fatalf("injected prompt missing recent section")
	}
	if !strings.Contains(secondResp, "User: first user question") {
		t.Fatalf("injected prompt missing previous turn request text")
	}
	if !strings.Contains(secondResp, "[Current User Input]\nsecond user question") {
		t.Fatalf("injected prompt missing current input section")
	}
}

func TestComposeContextPromptFirstTurnPassThrough(t *testing.T) {
	input := "/mcp call demo_server demo_tool {}"

	got := composeContextPrompt("", nil, input, 1024)
	if got != input {
		t.Fatalf("first-turn prompt = %q, want %q", got, input)
	}

	truncated := composeContextPrompt("", nil, input, 12)
	if truncated != input[:12] {
		t.Fatalf("first-turn truncation = %q, want %q", truncated, input[:12])
	}
}

func TestCompactUpdatesSummaryAndAffectsNextTurn(t *testing.T) {
	root := t.TempDir()
	h := newTestServer(t, testServerOptions{allowedRoots: []string{root}})
	ts := httptest.NewServer(h)
	defer ts.Close()

	threadID := createThreadHTTP(t, ts.URL, "client-a", root)

	first := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "important decision for compact")
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first turn status = %d, want %d", first.StatusCode, http.StatusOK)
	}

	compactStatus, compactBody := doJSON(
		t,
		http.MethodPost,
		ts.URL+"/v1/threads/"+threadID+"/compact",
		map[string]any{"maxSummaryChars": 120},
		map[string]string{"X-Client-ID": "client-a"},
	)
	if compactStatus != http.StatusOK {
		t.Fatalf("compact status = %d, want %d, body=%s", compactStatus, http.StatusOK, compactBody)
	}
	var compactResp struct {
		TurnID       string `json:"turnId"`
		Summary      string `json:"summary"`
		SummaryChars int    `json:"summaryChars"`
	}
	if err := json.Unmarshal([]byte(compactBody), &compactResp); err != nil {
		t.Fatalf("unmarshal compact response: %v", err)
	}
	if compactResp.TurnID == "" {
		t.Fatalf("compact turnId is empty")
	}
	if compactResp.SummaryChars > 120 {
		t.Fatalf("summaryChars = %d, want <= 120", compactResp.SummaryChars)
	}
	if compactResp.Summary == "" {
		t.Fatalf("compact summary is empty")
	}

	threadStatus, threadBody := doJSON(t, http.MethodGet, ts.URL+"/v1/threads/"+threadID, nil, map[string]string{"X-Client-ID": "client-a"})
	if threadStatus != http.StatusOK {
		t.Fatalf("get thread status = %d, body=%s", threadStatus, threadBody)
	}
	var threadResp struct {
		Thread struct {
			Summary string `json:"summary"`
		} `json:"thread"`
	}
	if err := json.Unmarshal([]byte(threadBody), &threadResp); err != nil {
		t.Fatalf("unmarshal get thread response: %v", err)
	}
	if threadResp.Thread.Summary != compactResp.Summary {
		t.Fatalf("thread summary mismatch after compact")
	}

	next := runTurnStreamRequest(t, ts.URL, "client-a", threadID, "follow up after compact")
	if next.StatusCode != http.StatusOK {
		t.Fatalf("next turn status = %d, want %d", next.StatusCode, http.StatusOK)
	}
	history := getHistoryHTTP(t, ts.URL, "client-a", threadID, false)
	if got, want := len(history.Turns), 2; got != want {
		t.Fatalf("default history should hide internal turns: got %d, want %d", got, want)
	}
	lastResp := history.Turns[len(history.Turns)-1].ResponseText
	if !strings.Contains(lastResp, compactResp.Summary) {
		t.Fatalf("next injected prompt does not include compacted summary")
	}

	historyInternal := getHistoryWithInternalHTTP(t, ts.URL, "client-a", threadID, false)
	if got, want := len(historyInternal.Turns), 3; got != want {
		t.Fatalf("history(includeInternal) turns = %d, want %d", got, want)
	}
	internalCount := 0
	for _, turn := range historyInternal.Turns {
		if turn.IsInternal {
			internalCount++
		}
	}
	if internalCount != 1 {
		t.Fatalf("internal turn count = %d, want 1", internalCount)
	}
}

func TestRestartRecoveryWithInjectedContext(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "restart.db")

	serverOne, closeOne := newTestServerWithDBPath(t, dbPath, testServerOptions{allowedRoots: []string{root}})
	tsOne := httptest.NewServer(serverOne)
	threadID := createThreadHTTP(t, tsOne.URL, "client-a", root)

	first := runTurnStreamRequest(t, tsOne.URL, "client-a", threadID, "pre-restart message")
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first turn status = %d, want %d", first.StatusCode, http.StatusOK)
	}
	tsOne.Close()
	closeOne()

	serverTwo, closeTwo := newTestServerWithDBPath(t, dbPath, testServerOptions{allowedRoots: []string{root}})
	defer closeTwo()
	tsTwo := httptest.NewServer(serverTwo)
	defer tsTwo.Close()

	second := runTurnStreamRequest(t, tsTwo.URL, "client-a", threadID, "post-restart message")
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second turn status = %d, want %d", second.StatusCode, http.StatusOK)
	}

	history := getHistoryHTTP(t, tsTwo.URL, "client-a", threadID, false)
	if got, want := len(history.Turns), 2; got != want {
		t.Fatalf("len(history.turns) = %d, want %d", got, want)
	}
	lastResp := history.Turns[len(history.Turns)-1].ResponseText
	if !strings.Contains(lastResp, "User: pre-restart message") {
		t.Fatalf("restart-injected prompt missing pre-restart history")
	}
}

type testServerOptions struct {
	authToken         string
	allowedRoots      []string
	agent             agents.Streamer
	turnAgentFactory  TurnAgentFactory
	agentIdleTTL      time.Duration
	permissionTimeout time.Duration
	logger            *slog.Logger
}

func newTestServer(t *testing.T, opt testServerOptions) *Server {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "api.db")
	store, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("storage.New(%q): %v", dbPath, err)
	}
	allowedRoots := opt.allowedRoots
	if len(allowedRoots) == 0 {
		allowedRoots = []string{t.TempDir()}
	}

	streamAgent := opt.agent
	if streamAgent == nil {
		streamAgent = agents.NewFakeAgentWithConfig(3, 10*time.Millisecond)
	}

	turnAgentFactory := opt.turnAgentFactory
	if turnAgentFactory == nil {
		turnAgentFactory = func(thread storage.Thread) (agents.Streamer, error) {
			_ = thread
			return streamAgent, nil
		}
	}

	server := New(Config{
		AuthToken: opt.authToken,
		Agents: []AgentInfo{
			{ID: "codex", Name: "Codex", Status: "available"},
			{ID: "claude", Name: "Claude Code", Status: "unavailable"},
		},
		AllowedAgentIDs:   []string{"codex"},
		AllowedRoots:      allowedRoots,
		Store:             store,
		TurnController:    runtimectl.NewTurnController(),
		TurnAgentFactory:  turnAgentFactory,
		AgentIdleTTL:      opt.agentIdleTTL,
		PermissionTimeout: opt.permissionTimeout,
		Logger:            opt.logger,
	})
	t.Cleanup(func() {
		_ = server.Close()
		_ = store.Close()
	})
	return server
}

func newTestServerWithDBPath(t *testing.T, dbPath string, opt testServerOptions) (*Server, func()) {
	t.Helper()

	store, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("storage.New(%q): %v", dbPath, err)
	}

	allowedRoots := opt.allowedRoots
	if len(allowedRoots) == 0 {
		allowedRoots = []string{t.TempDir()}
	}

	streamAgent := opt.agent
	if streamAgent == nil {
		streamAgent = agents.NewFakeAgentWithConfig(3, 10*time.Millisecond)
	}

	turnAgentFactory := opt.turnAgentFactory
	if turnAgentFactory == nil {
		turnAgentFactory = func(thread storage.Thread) (agents.Streamer, error) {
			_ = thread
			return streamAgent, nil
		}
	}

	server := New(Config{
		AuthToken: opt.authToken,
		Agents: []AgentInfo{
			{ID: "codex", Name: "Codex", Status: "available"},
			{ID: "claude", Name: "Claude Code", Status: "unavailable"},
		},
		AllowedAgentIDs:   []string{"codex"},
		AllowedRoots:      allowedRoots,
		Store:             store,
		TurnController:    runtimectl.NewTurnController(),
		TurnAgentFactory:  turnAgentFactory,
		AgentIdleTTL:      opt.agentIdleTTL,
		PermissionTimeout: opt.permissionTimeout,
		Logger:            opt.logger,
	})
	return server, func() {
		_ = server.Close()
		_ = store.Close()
	}
}

func createThreadForClient(t *testing.T, h http.Handler, clientID, root string) string {
	t.Helper()
	createRR := performJSONRequest(t, h, http.MethodPost, "/v1/threads", map[string]any{"agent": "codex", "cwd": root}, map[string]string{"X-Client-ID": clientID})
	if createRR.Code != http.StatusOK {
		t.Fatalf("create thread status code = %d, want %d", createRR.Code, http.StatusOK)
	}
	return extractThreadID(t, createRR.Body.Bytes())
}

func performJSONRequest(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reqBody []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal request body: %v", err)
		}
		reqBody = encoded
	}

	req := httptest.NewRequest(method, path, bytes.NewReader(reqBody))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func extractThreadID(t *testing.T, payload []byte) string {
	t.Helper()
	var body struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("unmarshal threadId response: %v", err)
	}
	if body.ThreadID == "" {
		t.Fatalf("threadId is empty")
	}
	return body.ThreadID
}

type parsedSSEEvent struct {
	Event string
	Data  map[string]any
}

func parseSSEEvents(t *testing.T, raw string) []parsedSSEEvent {
	t.Helper()

	blocks := strings.Split(strings.TrimSpace(raw), "\n\n")
	events := make([]parsedSSEEvent, 0)
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		eventType := ""
		dataLine := ""
		for _, line := range lines {
			if strings.HasPrefix(line, "event: ") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			}
			if strings.HasPrefix(line, "data: ") {
				dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			}
		}
		if eventType == "" {
			continue
		}
		payload := make(map[string]any)
		if dataLine != "" {
			if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
				t.Fatalf("unmarshal sse data for event %q: %v", eventType, err)
			}
		}
		events = append(events, parsedSSEEvent{Event: eventType, Data: payload})
	}
	return events
}

func stringField(payload map[string]any, key string) string {
	v, _ := payload[key]
	s, _ := v.(string)
	return s
}

func assertErrorCode(t *testing.T, payload []byte, wantCode string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, wantCode)
	}
}

type httpTurnStreamResult struct {
	StatusCode int
	Body       string
}

type countingClosableStreamer struct {
	streamCalls atomic.Int32
	closeCalls  atomic.Int32
}

func (s *countingClosableStreamer) Name() string {
	return "counting-closable"
}

func (s *countingClosableStreamer) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	s.streamCalls.Add(1)
	if err := onDelta(input); err != nil {
		return agents.StopReasonEndTurn, err
	}
	return agents.StopReasonEndTurn, nil
}

func (s *countingClosableStreamer) Close() error {
	s.closeCalls.Add(1)
	return nil
}

func (s *countingClosableStreamer) CloseCount() int32 {
	return s.closeCalls.Load()
}

type errorStreamer struct {
	err error
}

func (s *errorStreamer) Name() string {
	return "error-streamer"
}

func (s *errorStreamer) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	_ = ctx
	_ = input
	_ = onDelta
	if s.err == nil {
		return agents.StopReasonEndTurn, nil
	}
	return agents.StopReasonEndTurn, s.err
}

func createThreadHTTP(t *testing.T, baseURL, clientID, root string) string {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, baseURL+"/v1/threads", map[string]any{"agent": "codex", "cwd": root}, map[string]string{"X-Client-ID": clientID})
	if status != http.StatusOK {
		t.Fatalf("create thread http status = %d, body=%s", status, body)
	}
	return extractThreadID(t, []byte(body))
}

func runTurnStreamRequest(t *testing.T, baseURL, clientID, threadID, input string) httpTurnStreamResult {
	t.Helper()
	status, body := doJSON(t, http.MethodPost, baseURL+"/v1/threads/"+threadID+"/turns", map[string]any{"input": input, "stream": true}, map[string]string{"X-Client-ID": clientID})
	return httpTurnStreamResult{StatusCode: status, Body: body}
}

func postTurnRequest(t *testing.T, baseURL, clientID, threadID, input string) (int, string) {
	t.Helper()
	return doJSON(t, http.MethodPost, baseURL+"/v1/threads/"+threadID+"/turns", map[string]any{"input": input, "stream": true}, map[string]string{"X-Client-ID": clientID})
}

func postCancel(t *testing.T, baseURL, clientID, turnID string) (int, string) {
	t.Helper()
	return doJSON(t, http.MethodPost, baseURL+"/v1/turns/"+turnID+"/cancel", map[string]any{}, map[string]string{"X-Client-ID": clientID})
}

func waitForTurnID(t *testing.T, baseURL, clientID, threadID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		history := getHistoryHTTP(t, baseURL, clientID, threadID, false)
		if len(history.Turns) > 0 {
			return history.Turns[len(history.Turns)-1].TurnID
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

func waitForPermissionID(t *testing.T, baseURL, clientID, threadID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		history := getHistoryWithEventsHTTP(t, baseURL, clientID, threadID)
		if len(history.Turns) == 0 {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		lastTurn := history.Turns[len(history.Turns)-1]
		for _, event := range lastTurn.Events {
			if event.Type != "permission_required" {
				continue
			}
			if permissionID := stringField(event.Data, "permissionId"); permissionID != "" {
				return permissionID
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

func waitForTerminalTurn(t *testing.T, baseURL, clientID, threadID string, timeout time.Duration) (status, stopReason string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	lastStatus := ""
	lastStopReason := ""
	for time.Now().Before(deadline) {
		history := getHistoryHTTP(t, baseURL, clientID, threadID, false)
		if len(history.Turns) > 0 {
			last := history.Turns[len(history.Turns)-1]
			lastStatus = last.Status
			lastStopReason = last.StopReason
			switch last.Status {
			case "completed", "cancelled", "failed":
				return last.Status, last.StopReason
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return lastStatus, lastStopReason
}

type historyResponse struct {
	Turns []struct {
		TurnID       string `json:"turnId"`
		IsInternal   bool   `json:"isInternal"`
		StopReason   string `json:"stopReason"`
		Status       string `json:"status"`
		ResponseText string `json:"responseText"`
	} `json:"turns"`
}

type historyWithEventsResponse struct {
	Turns []struct {
		TurnID string `json:"turnId"`
		Events []struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"events"`
	} `json:"turns"`
}

func getHistoryWithEventsHTTP(t *testing.T, baseURL, clientID, threadID string) historyWithEventsResponse {
	t.Helper()
	url := fmt.Sprintf("%s/v1/threads/%s/history?includeEvents=true", baseURL, threadID)
	status, body := doJSON(t, http.MethodGet, url, nil, map[string]string{"X-Client-ID": clientID})
	if status != http.StatusOK {
		t.Fatalf("history(includeEvents) status = %d, body=%s", status, body)
	}
	var resp historyWithEventsResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal history(includeEvents) response: %v", err)
	}
	return resp
}

func getHistoryHTTP(t *testing.T, baseURL, clientID, threadID string, includeEvents bool) historyResponse {
	t.Helper()
	url := fmt.Sprintf("%s/v1/threads/%s/history", baseURL, threadID)
	if includeEvents {
		url += "?includeEvents=true"
	}
	status, body := doJSON(t, http.MethodGet, url, nil, map[string]string{"X-Client-ID": clientID})
	if status != http.StatusOK {
		t.Fatalf("history status = %d, body=%s", status, body)
	}
	var resp historyResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal history response: %v", err)
	}
	return resp
}

func getHistoryWithInternalHTTP(t *testing.T, baseURL, clientID, threadID string, includeEvents bool) historyResponse {
	t.Helper()
	url := fmt.Sprintf("%s/v1/threads/%s/history?includeInternal=true", baseURL, threadID)
	if includeEvents {
		url += "&includeEvents=true"
	}
	status, body := doJSON(t, http.MethodGet, url, nil, map[string]string{"X-Client-ID": clientID})
	if status != http.StatusOK {
		t.Fatalf("history(includeInternal) status = %d, body=%s", status, body)
	}
	var resp historyResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal history(includeInternal) response: %v", err)
	}
	return resp
}

func postPermissionDecision(t *testing.T, baseURL, clientID, permissionID, outcome string) (int, string) {
	t.Helper()
	return doJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/permissions/"+permissionID,
		map[string]any{"outcome": outcome},
		map[string]string{"X-Client-ID": clientID},
	)
}

func newFakeACPStreamer(t *testing.T) agents.Streamer {
	t.Helper()
	binaryPath := buildFakeACPAgentBinary(t)
	client, err := acp.New(acp.Config{
		Command: binaryPath,
		Name:    "fake-acp",
	})
	if err != nil {
		t.Fatalf("acp.New(fake_acp_agent): %v", err)
	}
	return client
}

func buildFakeACPAgentBinary(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	binaryPath := filepath.Join(t.TempDir(), "fake-acp-agent")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./testdata/fake_acp_agent")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build fake_acp_agent failed: %v, output=%s", err, strings.TrimSpace(string(output)))
	}
	return binaryPath
}

func startTurnStreamHTTP(t *testing.T, baseURL, clientID, threadID, input string) (*http.Response, context.CancelFunc) {
	t.Helper()

	body := map[string]any{"input": input, "stream": true}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal stream request: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/threads/"+threadID+"/turns", bytes.NewReader(encoded))
	if err != nil {
		cancel()
		t.Fatalf("http.NewRequest stream: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-ID", clientID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("http.Do stream request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		cancel()
		t.Fatalf("stream status = %d, want %d, body=%s", resp.StatusCode, http.StatusOK, string(raw))
	}
	return resp, cancel
}

func streamSSEEvents(body io.ReadCloser) (<-chan parsedSSEEvent, <-chan error) {
	eventsCh := make(chan parsedSSEEvent, 8)
	doneCh := make(chan error, 1)

	go func() {
		defer close(eventsCh)

		reader := bufio.NewReader(body)
		for {
			event, err := readOneSSEEvent(reader)
			if err != nil {
				if errors.Is(err, io.EOF) {
					doneCh <- nil
					return
				}
				doneCh <- err
				return
			}
			eventsCh <- event
		}
	}()

	return eventsCh, doneCh
}

func readOneSSEEvent(reader *bufio.Reader) (parsedSSEEvent, error) {
	eventType := ""
	dataLine := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return parsedSSEEvent{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if eventType == "" {
				continue
			}
			payload := map[string]any{}
			if strings.TrimSpace(dataLine) != "" {
				if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
					return parsedSSEEvent{}, err
				}
			}
			return parsedSSEEvent{Event: eventType, Data: payload}, nil
		}
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		}
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		}
	}
}

func doJSON(t *testing.T, method, url string, body any, headers map[string]string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Do: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	return resp.StatusCode, string(raw)
}

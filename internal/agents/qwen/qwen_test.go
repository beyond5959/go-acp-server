package qwen_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/go-acp-server/internal/agents"
	qwen "github.com/beyond5959/go-acp-server/internal/agents/qwen"
)

// TestPreflight verifies that Preflight returns nil when the qwen binary exists.
func TestPreflight(t *testing.T) {
	if _, err := exec.LookPath("qwen"); err != nil {
		t.Skip("qwen not in PATH")
	}
	if err := qwen.Preflight(); err != nil {
		t.Fatalf("Preflight() = %v, want nil", err)
	}
}

// TestNew verifies Config validation.
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		cfg     qwen.Config
		wantErr bool
	}{
		{"empty dir", qwen.Config{Dir: ""}, true},
		{"valid", qwen.Config{Dir: "/tmp"}, false},
		{"with modelID", qwen.Config{Dir: "/tmp", ModelID: "coder-model"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := qwen.New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestClientName verifies Name().
func TestClientName(t *testing.T) {
	c, _ := qwen.New(qwen.Config{Dir: "/tmp"})
	if got := c.Name(); got != "qwen" {
		t.Errorf("Name() = %q, want %q", got, "qwen")
	}
}

// TestStreamWithFakeProcess tests the Stream protocol using a fake qwen binary.
func TestStreamWithFakeProcess(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    method = req.get("method", "")
    rid = req.get("id")
    params = req.get("params", {})

    if method == "initialize":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":1,
            "agentInfo":{"name":"qwen-code","title":"Qwen Code","version":"0.11.0"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_qwen_test_123",
            "models":{"currentModelId":"test-model","availableModels":[]}
        }})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
    elif method == "session/cancel":
        send({"jsonrpc":"2.0","id":rid,"result":{}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/qwen"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	if err := qwen.Preflight(); err != nil {
		t.Fatalf("Preflight with fake binary: %v", err)
	}

	c, err := qwen.New(qwen.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var deltas []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reason, err := c.Stream(ctx, "say hello", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if reason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", reason, "end_turn")
	}
	if len(deltas) == 0 {
		t.Error("no deltas received")
	}
	if got := strings.Join(deltas, ""); !strings.Contains(got, "Hello") {
		t.Errorf("deltas = %q, want to contain %q", got, "Hello")
	}
}

// TestPermissionMapping verifies approved/declined/cancelled mapping for
// session/request_permission.
func TestPermissionMapping(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    method = req.get("method", "")
    rid = req.get("id")
    params = req.get("params", {})

    if method == "initialize":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":1,
            "agentInfo":{"name":"qwen-code","title":"Qwen Code","version":"0.11.0"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_qwen_perm_123",
            "models":{"currentModelId":"test-model","availableModels":[]}
        }})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        perm_id = 9001
        send({"jsonrpc":"2.0","id":perm_id,"method":"session/request_permission","params":{
            "sessionId":sid,
            "toolCall":{"title":"Run shell command","kind":"execute"},
            "options":[
                {"optionId":"allow_once_opt","name":"Allow once","kind":"allow_once"},
                {"optionId":"allow_always_opt","name":"Allow always","kind":"allow_always"},
                {"optionId":"reject_once_opt","name":"Reject once","kind":"reject_once"},
                {"optionId":"reject_always_opt","name":"Reject always","kind":"reject_always"}
            ]
        }})

        marker = "missing_response"
        for rline in sys.stdin:
            rline = rline.strip()
            if not rline:
                continue
            resp = json.loads(rline)
            if resp.get("id") != perm_id:
                continue
            result = resp.get("result", {})
            outcome = result.get("outcome", {})
            if outcome.get("outcome") == "selected":
                marker = outcome.get("optionId", "")
            else:
                marker = outcome.get("outcome", "")
            break

        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":marker}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
    elif method == "session/cancel":
        send({"jsonrpc":"2.0","id":rid,"result":{}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/qwen"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	tests := []struct {
		name       string
		outcome    agents.PermissionOutcome
		wantMarker string
	}{
		{
			name:       "approved maps to selected allow_once option",
			outcome:    agents.PermissionOutcomeApproved,
			wantMarker: "allow_once_opt",
		},
		{
			name:       "declined maps to selected reject_once option",
			outcome:    agents.PermissionOutcomeDeclined,
			wantMarker: "reject_once_opt",
		},
		{
			name:       "cancelled maps to cancelled outcome",
			outcome:    agents.PermissionOutcomeCancelled,
			wantMarker: "cancelled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := qwen.New(qwen.Config{Dir: tmpDir})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			baseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			ctx := agents.WithPermissionHandler(baseCtx, func(ctx context.Context, req agents.PermissionRequest) (agents.PermissionResponse, error) {
				return agents.PermissionResponse{Outcome: tt.outcome}, nil
			})

			var deltas []string
			reason, err := c.Stream(ctx, "permission test", func(delta string) error {
				deltas = append(deltas, delta)
				return nil
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if reason != "end_turn" {
				t.Fatalf("StopReason = %q, want %q", reason, "end_turn")
			}

			got := strings.Join(deltas, "")
			if !strings.Contains(got, tt.wantMarker) {
				t.Fatalf("permission marker = %q, want contains %q", got, tt.wantMarker)
			}
		})
	}
}

// TestQwenE2ESmoke performs a real turn with the installed qwen binary.
// Run with: E2E_QWEN=1 go test ./internal/agents/qwen/ -run E2E -v -timeout 90s
func TestQwenE2ESmoke(t *testing.T) {
	if os.Getenv("E2E_QWEN") != "1" {
		t.Skip("set E2E_QWEN=1 to run")
	}
	if err := qwen.Preflight(); err != nil {
		t.Skipf("qwen not available: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	c, err := qwen.New(qwen.Config{Dir: cwd})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var builder strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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

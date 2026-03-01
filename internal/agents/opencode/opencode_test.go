package opencode_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	opencode "github.com/beyond5959/go-acp-server/internal/agents/opencode"
)

// TestPreflight verifies that Preflight returns nil when the opencode binary exists.
func TestPreflight(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not in PATH")
	}
	if err := opencode.Preflight(); err != nil {
		t.Fatalf("Preflight() = %v, want nil", err)
	}
}

// TestNew verifies Config validation.
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		cfg     opencode.Config
		wantErr bool
	}{
		{"empty dir", opencode.Config{Dir: ""}, true},
		{"valid", opencode.Config{Dir: "/tmp"}, false},
		{"with modelID", opencode.Config{Dir: "/tmp", ModelID: "anthropic/claude-3-5-haiku"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := opencode.New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestClientName verifies Name().
func TestClientName(t *testing.T) {
	c, _ := opencode.New(opencode.Config{Dir: "/tmp"})
	if got := c.Name(); got != "opencode" {
		t.Errorf("Name() = %q, want %q", got, "opencode")
	}
}

// TestStreamWithFakeProcess tests the Stream protocol using a fake opencode binary.
func TestStreamWithFakeProcess(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	// Build a fake opencode binary that mimics the protocol.
	fakeScript := fmt.Sprintf(`#!%s
import sys, json

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
    if method == "initialize":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":1,
            "agentInfo":{"name":"FakeOpenCode","version":"0.0.1"},
            "agentCapabilities":{},"authMethods":[]
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_test123",
            "models":{"currentModelId":"fake/model","availableModels":[]}
        }})
    elif method == "session/prompt":
        params = req.get("params", {})
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn","usage":{}}})
        sys.exit(0)
    elif method == "session/cancel":
        send({"jsonrpc":"2.0","id":rid,"result":{}})
        sys.exit(0)
`, python3)

	// Write fake binary.
	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/opencode"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	// Prepend tmpDir to PATH.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	// Verify Preflight sees the fake binary.
	if err := opencode.Preflight(); err != nil {
		t.Fatalf("Preflight with fake binary: %v", err)
	}

	c, err := opencode.New(opencode.Config{Dir: tmpDir})
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

// TestOpenCodeE2ESmoke performs a real turn with the installed opencode binary.
// Run with: E2E_OPENCODE=1 go test ./internal/agents/opencode/ -run E2E -v -timeout 60s
func TestOpenCodeE2ESmoke(t *testing.T) {
	if os.Getenv("E2E_OPENCODE") != "1" {
		t.Skip("set E2E_OPENCODE=1 to run")
	}
	if err := opencode.Preflight(); err != nil {
		t.Skipf("opencode not available: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	c, err := opencode.New(opencode.Config{Dir: cwd})
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

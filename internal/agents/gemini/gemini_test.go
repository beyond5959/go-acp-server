package gemini_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	gemini "github.com/beyond5959/go-acp-server/internal/agents/gemini"
)

// TestPreflight verifies that Preflight returns nil when the gemini binary exists.
func TestPreflight(t *testing.T) {
	if _, err := exec.LookPath("gemini"); err != nil {
		t.Skip("gemini not in PATH")
	}
	if err := gemini.Preflight(); err != nil {
		t.Fatalf("Preflight() = %v, want nil", err)
	}
}

// TestNew verifies Config validation.
func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		cfg     gemini.Config
		wantErr bool
	}{
		{"empty dir", gemini.Config{Dir: ""}, true},
		{"valid", gemini.Config{Dir: "/tmp"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gemini.New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestClientName verifies Name().
func TestClientName(t *testing.T) {
	c, _ := gemini.New(gemini.Config{Dir: "/tmp"})
	if got := c.Name(); got != "gemini" {
		t.Errorf("Name() = %q, want %q", got, "gemini")
	}
}

// TestStreamWithFakeProcess tests the Stream protocol using a fake gemini binary.
func TestStreamWithFakeProcess(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	// Build a fake gemini binary that mimics the ACP protocol.
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
    params = req.get("params", {})

    if method == "initialize":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":1,
            "authMethods":[{"id":"gemini-api-key","name":"Use Gemini API key","description":None}],
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "authenticate":
        send({"jsonrpc":"2.0","id":rid,"result":{}})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_test456",
            "modes":{"availableModes":[],"currentModeId":"default"}
        }})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        # Send a streaming update notification.
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"PONG"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/gemini"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	if err := gemini.Preflight(); err != nil {
		t.Fatalf("Preflight with fake binary: %v", err)
	}

	c, err := gemini.New(gemini.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var deltas []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reason, err := c.Stream(ctx, "say PONG", func(delta string) error {
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
	if got := strings.Join(deltas, ""); !strings.Contains(got, "PONG") {
		t.Errorf("deltas = %q, want to contain %q", got, "PONG")
	}
}

// TestGeminiE2ESmoke performs a real turn with the installed gemini binary.
// Run with: E2E_GEMINI=1 go test ./internal/agents/gemini/ -run E2E -v -timeout 60s
func TestGeminiE2ESmoke(t *testing.T) {
	if os.Getenv("E2E_GEMINI") != "1" {
		t.Skip("set E2E_GEMINI=1 to run")
	}
	if err := gemini.Preflight(); err != nil {
		t.Skipf("gemini not available: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	c, err := gemini.New(gemini.Config{Dir: cwd})
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

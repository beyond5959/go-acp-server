package opencode_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	opencode "github.com/beyond5959/ngent/internal/agents/opencode"
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

func TestStreamCapturesSlashCommandsEmittedBeforePrompt(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

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
            "agentInfo":{"name":"FakeOpenCode","version":"0.0.1"},
            "agentCapabilities":{},"authMethods":[]
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":"ses_opencode_slash_123",
            "update":{
                "sessionUpdate":"available_commands_update",
                "availableCommands":[
                    {"name":"init","description":"Initialize workspace"},
                    {"name":"review","description":"Review changes"}
                ]
            }
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_opencode_slash_123",
            "models":{"currentModelId":"fake/model","availableModels":[]}
        }})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"OK"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn","usage":{}}})
        sys.exit(0)
    elif method == "session/cancel":
        send({"jsonrpc":"2.0","id":rid,"result":{}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/opencode"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	c, err := opencode.New(opencode.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var captured []agents.SlashCommand
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = agents.WithSlashCommandsHandler(ctx, func(_ context.Context, commands []agents.SlashCommand) error {
		captured = append([]agents.SlashCommand(nil), commands...)
		return nil
	})

	reason, err := c.Stream(ctx, "show slash commands", func(string) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if reason != agents.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", reason, agents.StopReasonEndTurn)
	}
	if got, want := len(captured), 2; got != want {
		t.Fatalf("len(captured) = %d, want %d", got, want)
	}
	if captured[0].Name != "init" || captured[1].Name != "review" {
		t.Fatalf("captured = %+v, want init/review", captured)
	}
}

func TestSlashCommandsAfterConfigOptionsInit(t *testing.T) {
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

    if method == "initialize":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":1,
            "agentInfo":{"name":"FakeOpenCode","version":"0.0.1"},
            "agentCapabilities":{},"authMethods":[]
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":"ses_opencode_config_slash_123",
            "update":{
                "sessionUpdate":"available_commands_update",
                "availableCommands":[
                    {"name":"init","description":"create/update AGENTS.md"},
                    {"name":"compact","description":"compact the session"}
                ]
            }
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_opencode_config_slash_123",
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"openai/gpt-5",
                    "options":[
                        {"value":"openai/gpt-5","name":"GPT-5"}
                    ]
                }
            ]
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/opencode"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	c, err := opencode.New(opencode.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	options, err := c.ConfigOptions(ctx)
	if err != nil {
		t.Fatalf("ConfigOptions: %v", err)
	}
	if got, want := len(options), 1; got != want {
		t.Fatalf("len(options) = %d, want %d", got, want)
	}

	commands, known, err := c.SlashCommands(ctx)
	if err != nil {
		t.Fatalf("SlashCommands: %v", err)
	}
	if !known {
		t.Fatal("SlashCommands returned known=false, want true")
	}
	if got, want := len(commands), 2; got != want {
		t.Fatalf("len(commands) = %d, want %d", got, want)
	}
	if commands[0].Name != "init" || commands[1].Name != "compact" {
		t.Fatalf("commands = %+v, want init/compact", commands)
	}
}

func TestDiscoverModelsWithFakeProcess(t *testing.T) {
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
    if method == "initialize":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "protocolVersion":1,
            "agentInfo":{"name":"FakeOpenCode","version":"0.0.1"},
            "agentCapabilities":{},"authMethods":[]
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_models",
            "models":{
                "currentModelId":"openai/gpt-5",
                "availableModels":[
                    "openai/gpt-5",
                    {"modelId":"anthropic/claude-3-5-haiku","name":"Claude 3.5 Haiku"}
                ]
            }
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/opencode"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	models, err := opencode.DiscoverModels(context.Background(), opencode.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if got, want := len(models), 2; got != want {
		t.Fatalf("len(models) = %d, want %d", got, want)
	}
	if models[0].ID != "openai/gpt-5" {
		t.Fatalf("models[0].id = %q, want %q", models[0].ID, "openai/gpt-5")
	}
	if models[1].ID != "anthropic/claude-3-5-haiku" {
		t.Fatalf("models[1].id = %q, want %q", models[1].ID, "anthropic/claude-3-5-haiku")
	}
}

func TestSetConfigOptionModelUsesSessionSetModelSelection(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

selected_model = "opencode/big-pickle"

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
            "agentInfo":{"name":"FakeOpenCode","version":"0.0.1"},
            "agentCapabilities":{},"authMethods":[]
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_model_switch",
            "models":{
                "currentModelId":"opencode/big-pickle",
                "availableModels":[
                    "opencode/big-pickle",
                    "opencode/minimax-m2.5-free"
                ]
            }
        }})
    elif method == "session/set_model":
        selected_model = params.get("modelId") or selected_model
        send({"jsonrpc":"2.0","id":rid,"result":{}})
    elif method == "session/set_config_option":
        send({"jsonrpc":"2.0","id":rid,"error":{"code":-32601,"message":"method not found"}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/opencode"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	client, err := opencode.New(opencode.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	options, err := client.SetConfigOption(context.Background(), "model", "opencode/minimax-m2.5-free")
	if err != nil {
		t.Fatalf("SetConfigOption(model): %v", err)
	}
	if got, want := client.CurrentModelID(), "opencode/minimax-m2.5-free"; got != want {
		t.Fatalf("CurrentModelID() = %q, want %q", got, want)
	}
	if got, want := len(options), 1; got != want {
		t.Fatalf("len(options) = %d, want %d", got, want)
	}
	if got, want := strings.TrimSpace(options[0].CurrentValue), "opencode/minimax-m2.5-free"; got != want {
		t.Fatalf("options[0].CurrentValue = %q, want %q", got, want)
	}
}

func TestStreamUsesSessionModelSelection(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

selected_model = "opencode/big-pickle"

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
            "agentInfo":{"name":"FakeOpenCode","version":"0.0.1"},
            "agentCapabilities":{},"authMethods":[]
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_stream_model",
            "models":{
                "currentModelId":"opencode/big-pickle",
                "availableModels":[
                    "opencode/big-pickle",
                    "opencode/minimax-m2.5-free"
                ]
            }
        }})
    elif method == "session/set_model":
        selected_model = params.get("modelId") or selected_model
        send({"jsonrpc":"2.0","id":rid,"result":{}})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":selected_model}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn","usage":{}}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/opencode"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	client, err := opencode.New(opencode.Config{
		Dir:     tmpDir,
		ModelID: "opencode/minimax-m2.5-free",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var deltas []string
	reason, err := client.Stream(context.Background(), "say model", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got, want := reason, agents.StopReasonEndTurn; got != want {
		t.Fatalf("StopReason = %q, want %q", got, want)
	}
	if got, want := strings.Join(deltas, ""), "opencode/minimax-m2.5-free"; got != want {
		t.Fatalf("streamed model = %q, want %q", got, want)
	}
}

func TestListSessionsWithConfiguredModelDoesNotUseStartupFlags(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	tmpDir := t.TempDir()
	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

expected_args = ["acp", "--cwd", %q]
if sys.argv[1:] != expected_args:
    sys.exit(99)

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
            "agentInfo":{"name":"FakeOpenCode","version":"0.0.1"},
            "agentCapabilities":{
                "loadSession":True,
                "sessionCapabilities":{"list":True,"resume":True}
            },
            "authMethods":[]
        }})
    elif method == "session/list":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessions":[
                {"sessionId":"ses_list_1","cwd":params.get("cwd",""),"title":"History"}
            ]
        }})
        sys.exit(0)
`, python3, tmpDir)

	fakeBin := tmpDir + "/opencode"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	client, err := opencode.New(opencode.Config{
		Dir:     tmpDir,
		ModelID: "opencode/mimo-v2-pro-free/high",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	result, err := client.ListSessions(context.Background(), agents.SessionListRequest{CWD: tmpDir})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if got, want := len(result.Sessions), 1; got != want {
		t.Fatalf("len(result.Sessions) = %d, want %d", got, want)
	}
	if got, want := result.Sessions[0].SessionID, "ses_list_1"; got != want {
		t.Fatalf("result.Sessions[0].SessionID = %q, want %q", got, want)
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

func TestOpenCodeE2EListSessionsWithConfiguredModel(t *testing.T) {
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

	client, err := opencode.New(opencode.Config{
		Dir:     cwd,
		ModelID: "opencode/mimo-v2-pro-free/high",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := client.ListSessions(ctx, agents.SessionListRequest{CWD: cwd})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	t.Logf("ListSessions returned %d sessions", len(result.Sessions))
}

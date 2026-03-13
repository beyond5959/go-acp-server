package gemini_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	gemini "github.com/beyond5959/ngent/internal/agents/gemini"
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
		{"with modelID", gemini.Config{Dir: "/tmp", ModelID: "gemini-2.5-pro"}, false},
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

func TestStreamWithFakeProcessModelID(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import sys, json

expected_model = "gemini-2.5-pro"
seen_new_model = False
seen_prompt_model = False

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
    elif method == "session/new":
        seen_new_model = (params.get("model","") == expected_model)
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_test456",
            "modes":{"availableModes":[],"currentModeId":"default"}
        }})
    elif method == "session/prompt":
        seen_prompt_model = (params.get("model","") == expected_model)
        if (not seen_new_model) or (not seen_prompt_model):
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"model not forwarded"}})
            sys.exit(0)
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"MODEL_OK"}}
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

	c, err := gemini.New(gemini.Config{
		Dir:     tmpDir,
		ModelID: "gemini-2.5-pro",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var deltas []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reason, err := c.Stream(ctx, "say MODEL_OK", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if reason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", reason, "end_turn")
	}
	if got := strings.Join(deltas, ""); !strings.Contains(got, "MODEL_OK") {
		t.Errorf("deltas = %q, want to contain %q", got, "MODEL_OK")
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
            "authMethods":[{"id":"gemini-api-key","name":"Use Gemini API key","description":None}],
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":"ses_gemini_slash_123",
            "update":{
                "sessionUpdate":"available_commands_update",
                "availableCommands":[
                    {"name":"memory","description":"Show memory"},
                    {"name":"compress","description":"Compress context"}
                ]
            }
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_gemini_slash_123",
            "modes":{"availableModes":[],"currentModeId":"default"}
        }})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"OK"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/gemini"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	c, err := gemini.New(gemini.Config{Dir: tmpDir})
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
	if captured[0].Name != "memory" || captured[1].Name != "compress" {
		t.Fatalf("captured = %+v, want memory/compress", captured)
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
            "authMethods":[{"id":"gemini-api-key","name":"Use Gemini API key","description":None}],
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":"ses_gemini_config_slash_123",
            "update":{
                "sessionUpdate":"available_commands_update",
                "availableCommands":[
                    {"name":"memory","description":"Show memory"},
                    {"name":"compress","description":"Compress context"}
                ]
            }
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_gemini_config_slash_123",
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"gemini-2.5-pro",
                    "options":[
                        {"value":"gemini-2.5-pro","name":"Gemini 2.5 Pro"}
                    ]
                }
            ]
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/gemini"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	c, err := gemini.New(gemini.Config{Dir: tmpDir})
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
	if commands[0].Name != "memory" || commands[1].Name != "compress" {
		t.Fatalf("commands = %+v, want memory/compress", commands)
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
            "authMethods":[{"id":"gemini-api-key","name":"Use Gemini API key","description":None}],
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_test_models",
            "models":{
                "currentModelId":"gemini-2.5-pro",
                "availableModels":[
                    "gemini-2.5-pro",
                    {"modelId":"gemini-2.5-flash","name":"Gemini 2.5 Flash"}
                ]
            }
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/gemini"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	models, err := gemini.DiscoverModels(context.Background(), gemini.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if got, want := len(models), 2; got != want {
		t.Fatalf("len(models) = %d, want %d", got, want)
	}
	if models[0].ID != "gemini-2.5-pro" {
		t.Fatalf("models[0].id = %q, want %q", models[0].ID, "gemini-2.5-pro")
	}
	if models[1].ID != "gemini-2.5-flash" {
		t.Fatalf("models[1].id = %q, want %q", models[1].ID, "gemini-2.5-flash")
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

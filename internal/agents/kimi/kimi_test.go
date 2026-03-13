package kimi_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	kimi "github.com/beyond5959/ngent/internal/agents/kimi"
)

func setEmptyKimiHome(t *testing.T, base string) {
	t.Helper()
	kimiHome := filepath.Join(base, "empty-kimi-home")
	if err := os.MkdirAll(kimiHome, 0o755); err != nil {
		t.Fatalf("mkdir KIMI_HOME: %v", err)
	}
	t.Setenv("KIMI_HOME", kimiHome)
}

func writeKimiConfigFile(t *testing.T, kimiHome, body string) {
	t.Helper()
	if err := os.MkdirAll(kimiHome, 0o755); err != nil {
		t.Fatalf("mkdir KIMI_HOME: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kimiHome, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}

func TestPreflight(t *testing.T) {
	if _, err := exec.LookPath("kimi"); err != nil {
		t.Skip("kimi not in PATH")
	}
	if err := kimi.Preflight(); err != nil {
		t.Fatalf("Preflight() = %v, want nil", err)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		cfg     kimi.Config
		wantErr bool
	}{
		{"empty dir", kimi.Config{Dir: ""}, true},
		{"valid", kimi.Config{Dir: "/tmp"}, false},
		{"with modelID", kimi.Config{Dir: "/tmp", ModelID: "kimi-k2-turbo-preview"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := kimi.New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestClientName(t *testing.T) {
	c, _ := kimi.New(kimi.Config{Dir: "/tmp"})
	if got := c.Name(); got != "kimi" {
		t.Errorf("Name() = %q, want %q", got, "kimi")
	}
}

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
            "agentInfo":{"name":"kimi-code","title":"Kimi CLI","version":"0.0.1"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_kimi_test_123",
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
	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)
	setEmptyKimiHome(t, tmpDir)

	c, err := kimi.New(kimi.Config{Dir: tmpDir})
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

func TestStreamWithFakeProcessModelID(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

expected_model = "kimi-k2-turbo-preview"

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
            "agentInfo":{"name":"kimi-code","title":"Kimi CLI","version":"0.0.1"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_kimi_model_123",
            "models":{"currentModelId":"test-model","availableModels":[]}
        }})
    elif method == "session/prompt":
        if params.get("model","") != expected_model:
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"model not forwarded"}})
            sys.exit(0)
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"MODEL_OK"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
    elif method == "session/cancel":
        send({"jsonrpc":"2.0","id":rid,"result":{}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)
	setEmptyKimiHome(t, tmpDir)

	c, err := kimi.New(kimi.Config{
		Dir:     tmpDir,
		ModelID: "kimi-k2-turbo-preview",
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
            "agentInfo":{"name":"kimi-code","title":"Kimi CLI","version":"0.0.1"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":"ses_kimi_slash_123",
            "update":{
                "sessionUpdate":"available_commands_update",
                "availableCommands":[
                    {"name":"plan","description":"Toggle plan mode"},
                    {"name":"compact","description":"Compact the context"}
                ]
            }
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_kimi_slash_123",
            "models":{"currentModelId":"test-model","availableModels":[]}
        }})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"OK"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
    elif method == "session/cancel":
        send({"jsonrpc":"2.0","id":rid,"result":{}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))
	setEmptyKimiHome(t, tmpDir)

	c, err := kimi.New(kimi.Config{Dir: tmpDir})
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

	reason, err := c.Stream(ctx, "show slash commands", func(delta string) error { return nil })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if reason != agents.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", reason, agents.StopReasonEndTurn)
	}
	if got, want := len(captured), 2; got != want {
		t.Fatalf("len(captured) = %d, want %d", got, want)
	}
	if captured[0].Name != "plan" || captured[1].Name != "compact" {
		t.Fatalf("captured = %+v, want plan/compact", captured)
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
            "agentInfo":{"name":"kimi-code","title":"Kimi CLI","version":"0.0.1"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":"ses_kimi_config_slash_123",
            "update":{
                "sessionUpdate":"available_commands_update",
                "availableCommands":[
                    {"name":"plan","description":"Toggle plan mode"},
                    {"name":"compact","description":"Compact the context"}
                ]
            }
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_kimi_config_slash_123",
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"kimi-k2-turbo-preview",
                    "options":[
                        {"value":"kimi-k2-turbo-preview","name":"Kimi K2 Turbo Preview"}
                    ]
                }
            ]
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))
	setEmptyKimiHome(t, tmpDir)

	c, err := kimi.New(kimi.Config{Dir: tmpDir})
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
	if commands[0].Name != "plan" || commands[1].Name != "compact" {
		t.Fatalf("commands = %+v, want plan/compact", commands)
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
            "agentInfo":{"name":"kimi-code","title":"Kimi CLI","version":"0.0.1"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_kimi_models_123",
            "models":{
                "currentModelId":"kimi-k2-turbo-preview",
                "availableModels":[
                    "kimi-k2-turbo-preview",
                    {"modelId":"kimi-k2-thinking","name":"Kimi K2 Thinking"}
                ]
            }
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)
	setEmptyKimiHome(t, tmpDir)

	models, err := kimi.DiscoverModels(context.Background(), kimi.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if got, want := len(models), 2; got != want {
		t.Fatalf("len(models) = %d, want %d", got, want)
	}
	if models[0].ID != "kimi-k2-turbo-preview" {
		t.Fatalf("models[0].id = %q, want %q", models[0].ID, "kimi-k2-turbo-preview")
	}
	if models[1].ID != "kimi-k2-thinking" {
		t.Fatalf("models[1].id = %q, want %q", models[1].ID, "kimi-k2-thinking")
	}
}

func TestSetConfigOptionModelUsesStartupModelSelection(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

args = sys.argv[1:]
selected_model = "default-model"
for i, arg in enumerate(args):
    if arg == "--model" and i + 1 < len(args):
        selected_model = args[i + 1]

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
            "agentInfo":{"name":"kimi-code","title":"Kimi CLI","version":"0.0.1"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_kimi_switch_123",
            "models":{
                "currentModelId":selected_model,
                "availableModels":[
                    "default-model",
                    "kimi-k2-turbo-preview",
                    "kimi-k2-thinking"
                ]
            }
        }})
    elif method == "session/set_config_option":
        send({"jsonrpc":"2.0","id":rid,"error":{"code":-32601,"message":"method not found"}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)
	setEmptyKimiHome(t, tmpDir)

	client, err := kimi.New(kimi.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	options, err := client.SetConfigOption(context.Background(), "model", "kimi-k2-turbo-preview")
	if err != nil {
		t.Fatalf("SetConfigOption(model): %v", err)
	}
	if got := strings.TrimSpace(client.Name()); got != "kimi" {
		t.Fatalf("Name() = %q, want %q", got, "kimi")
	}
	if got := strings.TrimSpace(options[0].CurrentValue); got != "kimi-k2-turbo-preview" {
		t.Fatalf("current model = %q, want %q", got, "kimi-k2-turbo-preview")
	}
}

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
            "agentInfo":{"name":"kimi-code","title":"Kimi CLI","version":"0.0.1"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_kimi_perm_123",
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
	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)
	setEmptyKimiHome(t, tmpDir)

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
			c, err := kimi.New(kimi.Config{Dir: tmpDir})
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

func TestStreamFallsBackToFlagACP(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

args = sys.argv[1:]
if args == ["acp"]:
    sys.exit(2)

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
            "agentInfo":{"name":"kimi-code","title":"Kimi CLI","version":"0.0.1"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_kimi_fallback_123",
            "models":{"currentModelId":"test-model","availableModels":[]}
        }})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"FALLBACK_OK"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)
	setEmptyKimiHome(t, tmpDir)

	c, err := kimi.New(kimi.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var deltas []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reason, err := c.Stream(ctx, "say FALLBACK_OK", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if reason != "end_turn" {
		t.Fatalf("StopReason = %q, want %q", reason, "end_turn")
	}
	if got := strings.Join(deltas, ""); !strings.Contains(got, "FALLBACK_OK") {
		t.Fatalf("deltas = %q, want contains %q", got, "FALLBACK_OK")
	}
}

func TestConfigOptionsUseLocalConfigWithoutACP(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)
	t.Setenv("KIMI_HOME", tmpDir)
	writeKimiConfigFile(t, tmpDir, `
default_model = "kimi-code/default"
default_thinking = true

[models."kimi-code/default"]
model = "Kimi Default"
capabilities = ["thinking"]

[models."kimi-code/fast"]
model = "Kimi Fast"
capabilities = []
`)

	client, err := kimi.New(kimi.Config{
		Dir:             tmpDir,
		ConfigOverrides: map[string]string{"reasoning": "disabled"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	options, err := client.ConfigOptions(context.Background())
	if err != nil {
		t.Fatalf("ConfigOptions: %v", err)
	}
	if got, want := len(options), 2; got != want {
		t.Fatalf("len(options) = %d, want %d", got, want)
	}
	if got := strings.TrimSpace(options[0].CurrentValue); got != "kimi-code/default" {
		t.Fatalf("model currentValue = %q, want %q", got, "kimi-code/default")
	}
	if got := strings.TrimSpace(options[1].ID); got != "reasoning" {
		t.Fatalf("reasoning option id = %q, want %q", got, "reasoning")
	}
	if got := strings.TrimSpace(options[1].CurrentValue); got != "disabled" {
		t.Fatalf("reasoning currentValue = %q, want %q", got, "disabled")
	}
}

func TestSetConfigOptionUsesLocalConfigWithoutACP(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)
	t.Setenv("KIMI_HOME", tmpDir)
	writeKimiConfigFile(t, tmpDir, `
default_model = "kimi-code/default"
default_thinking = false

[models."kimi-code/default"]
model = "Kimi Default"
capabilities = ["thinking"]

[models."kimi-code/fast"]
model = "Kimi Fast"
capabilities = []
`)

	client, err := kimi.New(kimi.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	options, err := client.SetConfigOption(context.Background(), "model", "kimi-code/fast")
	if err != nil {
		t.Fatalf("SetConfigOption(model): %v", err)
	}
	if got := strings.TrimSpace(client.CurrentModelID()); got != "kimi-code/fast" {
		t.Fatalf("CurrentModelID() = %q, want %q", got, "kimi-code/fast")
	}
	if got, want := len(options), 1; got != want {
		t.Fatalf("len(options) after model switch = %d, want %d", got, want)
	}

	options, err = client.SetConfigOption(context.Background(), "model", "kimi-code/default")
	if err != nil {
		t.Fatalf("SetConfigOption(model->default): %v", err)
	}
	if got, want := len(options), 2; got != want {
		t.Fatalf("len(options) after switching back = %d, want %d", got, want)
	}

	options, err = client.SetConfigOption(context.Background(), "reasoning", "enabled")
	if err != nil {
		t.Fatalf("SetConfigOption(reasoning): %v", err)
	}
	if got := strings.TrimSpace(options[1].CurrentValue); got != "enabled" {
		t.Fatalf("reasoning currentValue = %q, want %q", got, "enabled")
	}
}

func TestDiscoverModelsUsesLocalConfigWithoutACP(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PATH", tmpDir)
	t.Setenv("KIMI_HOME", tmpDir)
	writeKimiConfigFile(t, tmpDir, `
default_model = "kimi-code/default"
default_thinking = false

[models."kimi-code/default"]
model = "Kimi Default"
capabilities = ["thinking"]

[models."kimi-code/fast"]
model = "Kimi Fast"
capabilities = []
`)

	models, err := kimi.DiscoverModels(context.Background(), kimi.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if got, want := len(models), 2; got != want {
		t.Fatalf("len(models) = %d, want %d", got, want)
	}
	if got := strings.TrimSpace(models[0].ID); got != "kimi-code/default" {
		t.Fatalf("models[0].id = %q, want %q", got, "kimi-code/default")
	}
}

func TestKimiConfigOptionsE2EDoesNotCreateSession(t *testing.T) {
	if os.Getenv("E2E_KIMI") != "1" {
		t.Skip("set E2E_KIMI=1 to run")
	}
	if err := kimi.Preflight(); err != nil {
		t.Skipf("kimi not available: %v", err)
	}

	kimiHome := strings.TrimSpace(os.Getenv("KIMI_HOME"))
	if kimiHome == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		kimiHome = filepath.Join(userHome, ".kimi")
	}
	sessionsDir := filepath.Join(kimiHome, "sessions")

	before, err := os.ReadDir(sessionsDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir(before): %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	client, err := kimi.New(kimi.Config{Dir: cwd})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	options, err := client.ConfigOptions(ctx)
	if err != nil {
		t.Fatalf("ConfigOptions: %v", err)
	}
	if len(options) == 0 {
		t.Fatal("ConfigOptions returned no options")
	}

	after, err := os.ReadDir(sessionsDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir(after): %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("ConfigOptions created session directories: before=%d after=%d", len(before), len(after))
	}
}

// TestKimiE2ESmoke performs a real turn with the installed kimi binary.
// Run with: E2E_KIMI=1 go test ./internal/agents/kimi/ -run E2E -v -timeout 90s
func TestKimiE2ESmoke(t *testing.T) {
	if os.Getenv("E2E_KIMI") != "1" {
		t.Skip("set E2E_KIMI=1 to run")
	}
	if err := kimi.Preflight(); err != nil {
		t.Skipf("kimi not available: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	c, err := kimi.New(kimi.Config{Dir: cwd})
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

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

func TestStreamWithFakeProcess(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

thinking = "enabled"

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

thinking = "enabled"

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

thinking = "enabled"

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

thinking = "enabled"

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

func TestConfigOptionsIgnoresLocalConfigAndUsesACP(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	tmpDir := t.TempDir()
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

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

thinking = "enabled"

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
            "sessionId":"ses_kimi_config_via_acp",
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"kimi-acp/default",
                    "options":[
                        {"value":"kimi-acp/default","name":"Kimi ACP Default"},
                        {"value":"kimi-acp/fast","name":"Kimi ACP Fast"}
                    ]
                },
                {
                    "id":"reasoning",
                    "category":"reasoning",
                    "name":"Reasoning",
                    "type":"select",
                    "currentValue":thinking,
                    "options":[
                        {"value":"enabled","name":"Enabled"},
                        {"value":"disabled","name":"Disabled"}
                    ]
                }
            ]
        }})
    elif method == "session/set_config_option":
        thinking = req.get("params", {}).get("value") or thinking
        send({"jsonrpc":"2.0","id":rid,"result":{
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"kimi-acp/default",
                    "options":[
                        {"value":"kimi-acp/default","name":"Kimi ACP Default"},
                        {"value":"kimi-acp/fast","name":"Kimi ACP Fast"}
                    ]
                },
                {
                    "id":"reasoning",
                    "category":"reasoning",
                    "name":"Reasoning",
                    "type":"select",
                    "currentValue":thinking,
                    "options":[
                        {"value":"enabled","name":"Enabled"},
                        {"value":"disabled","name":"Disabled"}
                    ]
                }
            ]
        }})
`, python3)

	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

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
	if got := strings.TrimSpace(options[0].CurrentValue); got != "kimi-acp/default" {
		t.Fatalf("model currentValue = %q, want %q", got, "kimi-acp/default")
	}
	if got := strings.TrimSpace(options[1].ID); got != "reasoning" {
		t.Fatalf("reasoning option id = %q, want %q", got, "reasoning")
	}
	if got := strings.TrimSpace(options[1].CurrentValue); got != "disabled" {
		t.Fatalf("reasoning currentValue = %q, want %q", got, "disabled")
	}
}

func TestSetConfigOptionIgnoresLocalConfigAndUsesACP(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	tmpDir := t.TempDir()
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

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

args = sys.argv[1:]
selected_model = "kimi-acp/default"
thinking = "disabled"
for i, arg in enumerate(args):
    if arg == "--model" and i + 1 < len(args):
        selected_model = args[i + 1]
    elif arg == "--thinking":
        thinking = "enabled"
    elif arg == "--no-thinking":
        thinking = "disabled"

def options():
    base = [{
        "id":"model",
        "category":"model",
        "name":"Model",
        "type":"select",
        "currentValue":selected_model,
        "options":[
            {"value":"kimi-acp/default","name":"Kimi ACP Default"},
            {"value":"kimi-acp/fast","name":"Kimi ACP Fast"}
        ]
    }]
    if selected_model != "kimi-acp/fast":
        base.append({
            "id":"reasoning",
            "category":"reasoning",
            "name":"Reasoning",
            "type":"select",
            "currentValue":thinking,
            "options":[
                {"value":"enabled","name":"Enabled"},
                {"value":"disabled","name":"Disabled"}
            ]
        })
    return base

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
            "sessionId":"ses_kimi_set_config_via_acp",
            "configOptions":options()
        }})
    elif method == "session/set_config_option":
        if params.get("configId") == "reasoning":
            thinking = params.get("value") or thinking
        send({"jsonrpc":"2.0","id":rid,"result":{"configOptions":options()}})
`, python3)

	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	client, err := kimi.New(kimi.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	options, err := client.SetConfigOption(context.Background(), "reasoning", "enabled")
	if err != nil {
		t.Fatalf("SetConfigOption(reasoning): %v", err)
	}
	if got := strings.TrimSpace(options[1].CurrentValue); got != "enabled" {
		t.Fatalf("reasoning currentValue = %q, want %q", got, "enabled")
	}
	if got := strings.TrimSpace(client.CurrentConfigOverrides()["reasoning"]); got != "enabled" {
		t.Fatalf("CurrentConfigOverrides()[reasoning] = %q, want %q", got, "enabled")
	}

	options, err = client.SetConfigOption(context.Background(), "model", "kimi-acp/fast")
	if err != nil {
		t.Fatalf("SetConfigOption(model): %v", err)
	}
	if got := strings.TrimSpace(client.CurrentModelID()); got != "kimi-acp/fast" {
		t.Fatalf("CurrentModelID() = %q, want %q", got, "kimi-acp/fast")
	}
	if got, want := len(options), 1; got != want {
		t.Fatalf("len(options) after model switch = %d, want %d", got, want)
	}
}

func TestDiscoverModelsIgnoresLocalConfigAndUsesACP(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	tmpDir := t.TempDir()
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
            "sessionId":"ses_kimi_models_via_acp",
            "models":{
                "currentModelId":"kimi-acp/default",
                "availableModels":[
                    {"modelId":"kimi-acp/default","name":"Kimi ACP Default"},
                    {"modelId":"kimi-acp/fast","name":"Kimi ACP Fast"}
                ]
            }
        }})
`, python3)

	fakeBin := tmpDir + "/kimi"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	models, err := kimi.DiscoverModels(context.Background(), kimi.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if got, want := len(models), 2; got != want {
		t.Fatalf("len(models) = %d, want %d", got, want)
	}
	if got := strings.TrimSpace(models[0].ID); got != "kimi-acp/default" {
		t.Fatalf("models[0].id = %q, want %q", got, "kimi-acp/default")
	}
}

func TestKimiConfigOptionsE2E(t *testing.T) {
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
	t.Logf("ConfigOptions returned %d options", len(options))
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

package qwen_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	qwen "github.com/beyond5959/ngent/internal/agents/qwen"
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

func TestStreamWithFakeProcessModelID(t *testing.T) {
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not in PATH")
	}

	fakeScript := fmt.Sprintf(`#!%s
import json
import sys

expected_model = "qwen3-coder-plus"
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
            "agentInfo":{"name":"qwen-code","title":"Qwen Code","version":"0.11.0"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_qwen_model_123",
            "models":{"currentModelId":"test-model","availableModels":[]}
        }})
    elif method == "session/prompt":
        seen_prompt_model = (params.get("model","") == expected_model)
        if not seen_prompt_model:
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
	fakeBin := tmpDir + "/qwen"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	c, err := qwen.New(qwen.Config{
		Dir:     tmpDir,
		ModelID: "qwen3-coder-plus",
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
            "agentInfo":{"name":"qwen-code","title":"Qwen Code","version":"0.11.0"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":"ses_qwen_slash_123",
            "update":{
                "sessionUpdate":"available_commands_update",
                "availableCommands":[
                    {"name":"init","description":"Initialize workspace"},
                    {"name":"compress","description":"Compress context"}
                ]
            }
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_qwen_slash_123",
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
	fakeBin := tmpDir + "/qwen"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	c, err := qwen.New(qwen.Config{Dir: tmpDir})
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
	if captured[0].Name != "init" || captured[1].Name != "compress" {
		t.Fatalf("captured = %+v, want init/compress", captured)
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
            "agentInfo":{"name":"qwen-code","title":"Qwen Code","version":"0.11.0"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":"ses_qwen_config_slash_123",
            "update":{
                "sessionUpdate":"available_commands_update",
                "availableCommands":[
                    {"name":"bug","description":"Report a bug"},
                    {"name":"summary","description":"Summarize context"}
                ]
            }
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_qwen_config_slash_123",
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"qwen3-coder-plus",
                    "options":[
                        {"value":"qwen3-coder-plus","name":"Qwen3 Coder Plus"}
                    ]
                }
            ]
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/qwen"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	c, err := qwen.New(qwen.Config{Dir: tmpDir})
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
	if commands[0].Name != "bug" || commands[1].Name != "summary" {
		t.Fatalf("commands = %+v, want bug/summary", commands)
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
            "agentInfo":{"name":"qwen-code","title":"Qwen Code","version":"0.11.0"},
            "authMethods":[],
            "modes":{"currentModeId":"default","availableModes":[]},
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "session/new":
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_qwen_models_123",
            "models":{
                "currentModelId":"qwen3-coder-plus",
                "availableModels":[
                    "qwen3-coder-plus",
                    {"modelId":"qwen3-coder-max","name":"Qwen3 Coder Max"}
                ]
            }
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/qwen"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	models, err := qwen.DiscoverModels(context.Background(), qwen.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if got, want := len(models), 2; got != want {
		t.Fatalf("len(models) = %d, want %d", got, want)
	}
	if models[0].ID != "qwen3-coder-plus" {
		t.Fatalf("models[0].id = %q, want %q", models[0].ID, "qwen3-coder-plus")
	}
	if models[1].ID != "qwen3-coder-max" {
		t.Fatalf("models[1].id = %q, want %q", models[1].ID, "qwen3-coder-max")
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

func TestQwenE2ESessionTranscriptReplay(t *testing.T) {
	if os.Getenv("E2E_QWEN") != "1" {
		t.Skip("set E2E_QWEN=1 to run")
	}
	if err := qwen.Preflight(); err != nil {
		t.Skipf("qwen not available: %v", err)
	}

	cwd := t.TempDir()
	c, err := qwen.New(qwen.Config{Dir: cwd})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	marker := fmt.Sprintf("QWEN_TRANSCRIPT_%d", time.Now().UnixNano())
	prompt := fmt.Sprintf("Reply with exactly %s and nothing else.", marker)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var response strings.Builder
	if _, err := c.Stream(ctx, prompt, func(delta string) error {
		response.WriteString(delta)
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}

	sessionID := c.CurrentSessionID()
	if sessionID == "" {
		t.Fatal("CurrentSessionID() returned empty session id")
	}
	t.Logf("sessionID: %s", sessionID)
	t.Logf("response: %q", response.String())

	var transcript agents.SessionTranscriptResult
	deadline := time.Now().Add(20 * time.Second)
	for {
		transcript, err = c.LoadSessionTranscript(context.Background(), agents.SessionTranscriptRequest{
			CWD:       cwd,
			SessionID: sessionID,
		})
		if err == nil {
			break
		}
		if !errors.Is(err, agents.ErrSessionNotFound) {
			t.Fatalf("LoadSessionTranscript: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("LoadSessionTranscript: session %q not visible in session/list within timeout", sessionID)
		}
		time.Sleep(time.Second)
	}

	if len(transcript.Messages) == 0 {
		t.Fatal("LoadSessionTranscript returned no replay messages")
	}

	foundMarker := false
	for _, msg := range transcript.Messages {
		if strings.Contains(msg.Content, marker) {
			foundMarker = true
			break
		}
	}
	if !foundMarker {
		t.Fatalf("replayed transcript did not contain marker %q: %+v", marker, transcript.Messages)
	}
}

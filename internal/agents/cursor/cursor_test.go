package cursor_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	cursor "github.com/beyond5959/ngent/internal/agents/cursor"
)

func TestPreflight(t *testing.T) {
	if _, err := exec.LookPath("agent"); err != nil {
		if _, err := exec.LookPath("cursor-agent"); err != nil {
			t.Skip("neither agent nor cursor-agent is in PATH")
		}
	}
	if err := cursor.Preflight(); err != nil {
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

authed = False

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
            "authMethods":[{"id":"cursor_login","name":"Cursor Login","description":"Use saved login"}],
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "authenticate":
        if params.get("methodId","") != "cursor_login":
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"wrong auth method"}})
            sys.exit(0)
        authed = True
        send({"jsonrpc":"2.0","id":rid,"result":{}})
    elif method == "session/new":
        if not authed:
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"authenticate required"}})
            sys.exit(0)
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_cursor_test_123",
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"default[]",
                    "options":[{"value":"default[]","name":"Auto"}]
                }
            ]
        }})
    elif method == "session/prompt":
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello from Cursor"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
    elif method == "session/cancel":
        send({"jsonrpc":"2.0","id":rid,"result":{}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/agent"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	c, err := cursor.New(cursor.Config{Dir: tmpDir})
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
	if reason != agents.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", reason, agents.StopReasonEndTurn)
	}
	if got := strings.Join(deltas, ""); !strings.Contains(got, "Hello from Cursor") {
		t.Fatalf("deltas = %q, want to contain %q", got, "Hello from Cursor")
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

authed = False
expected_model = "gpt-5.4-mini[reasoning=medium]"
selected_model = "default[]"

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
            "authMethods":[{"id":"cursor_login","name":"Cursor Login","description":"Use saved login"}],
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "authenticate":
        authed = (params.get("methodId","") == "cursor_login")
        send({"jsonrpc":"2.0","id":rid,"result":{}})
    elif method == "session/new":
        if not authed:
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"authenticate required"}})
            sys.exit(0)
        if params.get("model") or params.get("modelId"):
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"session/new should not carry model"}})
            sys.exit(0)
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_cursor_model_123",
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"default[]",
                    "options":[
                        {"value":"default[]","name":"Auto"},
                        {"value":"gpt-5.4-mini[reasoning=medium]","name":"GPT-5.4 Mini"}
                    ]
                }
            ]
        }})
    elif method == "session/set_config_option":
        if params.get("configId") != "model":
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"unexpected config option"}})
            sys.exit(0)
        if params.get("value") != expected_model:
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"wrong model value"}})
            sys.exit(0)
        selected_model = params.get("value")
        send({"jsonrpc":"2.0","id":rid,"result":{
            "configOptions":[
                {
                    "id":"model",
                    "category":"model",
                    "name":"Model",
                    "type":"select",
                    "currentValue":"%s",
                    "options":[
                        {"value":"default[]","name":"Auto"},
                        {"value":"%s","name":"GPT-5.4 Mini"}
                    ]
                }
            ]
        }})
    elif method == "session/prompt":
        if params.get("model") or params.get("modelId"):
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"prompt should not carry model"}})
            sys.exit(0)
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":selected_model}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"stopReason":"end_turn"}})
        sys.exit(0)
`, python3, expectedModelForFakeScript(), expectedModelForFakeScript())

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/agent"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	c, err := cursor.New(cursor.Config{
		Dir:     tmpDir,
		ModelID: expectedModelForFakeScript(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var deltas []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reason, err := c.Stream(ctx, "say model", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if reason != agents.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", reason, agents.StopReasonEndTurn)
	}
	if got := strings.Join(deltas, ""); !strings.Contains(got, expectedModelForFakeScript()) {
		t.Fatalf("deltas = %q, want to contain %q", got, expectedModelForFakeScript())
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

authed = False

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
            "authMethods":[{"id":"cursor_login","name":"Cursor Login","description":"Use saved login"}],
            "agentCapabilities":{"loadSession":True}
        }})
    elif method == "authenticate":
        authed = (params.get("methodId","") == "cursor_login")
        send({"jsonrpc":"2.0","id":rid,"result":{}})
    elif method == "session/new":
        if not authed:
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"authenticate required"}})
            sys.exit(0)
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessionId":"ses_cursor_models_123",
            "models":{
                "currentModelId":"default[]",
                "availableModels":[
                    {"modelId":"default[]","name":"Auto"},
                    {"modelId":"gpt-5.4-mini[reasoning=medium]","name":"GPT-5.4 Mini"}
                ]
            }
        }})
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/agent"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	models, err := cursor.DiscoverModels(context.Background(), cursor.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if got, want := len(models), 2; got != want {
		t.Fatalf("len(models) = %d, want %d", got, want)
	}
	if got, want := models[0].ID, "default[]"; got != want {
		t.Fatalf("models[0].ID = %q, want %q", got, want)
	}
	if got, want := models[1].ID, "gpt-5.4-mini[reasoning=medium]"; got != want {
		t.Fatalf("models[1].ID = %q, want %q", got, want)
	}
}

func expectedModelForFakeScript() string {
	return "gpt-5.4-mini[reasoning=medium]"
}

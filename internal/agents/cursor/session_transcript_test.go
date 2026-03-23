package cursor_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
	cursor "github.com/beyond5959/ngent/internal/agents/cursor"
)

func TestLoadSessionTranscriptWithFakeProcess(t *testing.T) {
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
            "agentCapabilities":{
                "loadSession":True,
                "sessionCapabilities":{"list":True,"load":True}
            }
        }})
    elif method == "authenticate":
        authed = (params.get("methodId","") == "cursor_login")
        send({"jsonrpc":"2.0","id":rid,"result":{}})
    elif method == "session/list":
        if not authed:
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"authenticate required"}})
            sys.exit(0)
        send({"jsonrpc":"2.0","id":rid,"result":{
            "sessions":[{"sessionId":"ses_cursor_history","cwd":params.get("cwd",""),"title":"History"}]
        }})
    elif method == "session/load":
        sid = params.get("sessionId","")
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello cursor"}}
        }})
        send({"jsonrpc":"2.0","method":"session/update","params":{
            "sessionId":sid,
            "update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello back"}}
        }})
        send({"jsonrpc":"2.0","id":rid,"result":{"sessionId":sid}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/agent"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	client, err := cursor.New(cursor.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := client.LoadSessionTranscript(ctx, agents.SessionTranscriptRequest{
		CWD:       tmpDir,
		SessionID: "ses_cursor_history",
	})
	if err != nil {
		t.Fatalf("LoadSessionTranscript: %v", err)
	}
	if got, want := len(result.Messages), 2; got != want {
		t.Fatalf("len(messages) = %d, want %d", got, want)
	}
	if got := result.Messages[0]; got.Role != "user" || got.Content != "hello cursor" {
		t.Fatalf("messages[0] = %+v, want user hello cursor", got)
	}
	if got := result.Messages[1]; got.Role != "assistant" || got.Content != "hello back" {
		t.Fatalf("messages[1] = %+v, want assistant hello back", got)
	}
}

func TestLoadSessionTranscriptReturnsNotFoundWhenSessionMissing(t *testing.T) {
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
            "agentCapabilities":{
                "loadSession":True,
                "sessionCapabilities":{"list":True,"load":True}
            }
        }})
    elif method == "authenticate":
        authed = (params.get("methodId","") == "cursor_login")
        send({"jsonrpc":"2.0","id":rid,"result":{}})
    elif method == "session/list":
        if not authed:
            send({"jsonrpc":"2.0","id":rid,"error":{"code":-32000,"message":"authenticate required"}})
            sys.exit(0)
        send({"jsonrpc":"2.0","id":rid,"result":{"sessions":[]}})
        sys.exit(0)
`, python3)

	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/agent"
	if err := os.WriteFile(fakeBin, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	client, err := cursor.New(cursor.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.LoadSessionTranscript(context.Background(), agents.SessionTranscriptRequest{
		CWD:       tmpDir,
		SessionID: "missing",
	})
	if !errors.Is(err, agents.ErrSessionNotFound) {
		t.Fatalf("LoadSessionTranscript error = %v, want %v", err, agents.ErrSessionNotFound)
	}
}

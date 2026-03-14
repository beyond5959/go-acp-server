package opencode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/beyond5959/ngent/internal/agents"
)

func TestHandlePermissionRequestParsesExternalDirectoryPayload(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"sessionId": "ses_opencode_perm",
		"options": [
			{"optionId":"once","kind":"allow_once"},
			{"optionId":"always","kind":"allow_always"},
			{"optionId":"reject","kind":"reject_once"}
		],
		"toolCall": {
			"title": "external_directory",
			"kind": "other",
			"toolCallId": "call_function_test_1",
			"rawInput": {
				"filepath": "/Users/niuniu/.config/opencode",
				"parentDir": "/Users/niuniu/.config/opencode"
			}
		}
	}`)

	var got agents.PermissionRequest
	resp, err := handlePermissionRequest(
		context.Background(),
		raw,
		func(ctx context.Context, req agents.PermissionRequest) (agents.PermissionResponse, error) {
			got = req
			return agents.PermissionResponse{Outcome: agents.PermissionOutcomeApproved}, nil
		},
		true,
	)
	if err != nil {
		t.Fatalf("handlePermissionRequest() error = %v", err)
	}

	if got.Approval != "file" {
		t.Fatalf("req.Approval = %q, want %q", got.Approval, "file")
	}
	if got.Command != "/Users/niuniu/.config/opencode" {
		t.Fatalf("req.Command = %q, want %q", got.Command, "/Users/niuniu/.config/opencode")
	}
	if sessionID, _ := got.RawParams["sessionId"].(string); sessionID != "ses_opencode_perm" {
		t.Fatalf("req.RawParams[sessionId] = %q, want %q", sessionID, "ses_opencode_perm")
	}
	if path, _ := got.RawParams["path"].(string); path != "/Users/niuniu/.config/opencode" {
		t.Fatalf("req.RawParams[path] = %q, want %q", path, "/Users/niuniu/.config/opencode")
	}

	var decoded struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal(resp, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Outcome.Outcome != "selected" {
		t.Fatalf("response.outcome.outcome = %q, want %q", decoded.Outcome.Outcome, "selected")
	}
	if decoded.Outcome.OptionID != "once" {
		t.Fatalf("response.outcome.optionId = %q, want %q", decoded.Outcome.OptionID, "once")
	}
}

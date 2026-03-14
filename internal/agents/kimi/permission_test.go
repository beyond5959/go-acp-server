package kimi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/beyond5959/ngent/internal/agents"
)

func TestHandlePermissionRequestParsesRichToolCallPayload(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"sessionId": "ses_kimi_perm_rich",
		"options": [
			{"optionId":"approve","kind":"allow_once"},
			{"optionId":"approve_for_session","kind":"allow_always"},
			{"optionId":"reject","kind":"reject_once"}
		],
		"toolCall": {
			"title": "WriteFile: soul.md",
			"toolCallId": "tool-123",
			"content": [
				{
					"type": "diff",
					"path": "/Users/niuniu/git_local/ngent/soul.md",
					"oldText": "",
					"newText": "hello"
				}
			]
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
	if got.Command != "WriteFile: soul.md" {
		t.Fatalf("req.Command = %q, want %q", got.Command, "WriteFile: soul.md")
	}
	if sessionID, _ := got.RawParams["sessionId"].(string); sessionID != "ses_kimi_perm_rich" {
		t.Fatalf("req.RawParams[sessionId] = %q, want %q", sessionID, "ses_kimi_perm_rich")
	}
	if path, _ := got.RawParams["path"].(string); path != "/Users/niuniu/git_local/ngent/soul.md" {
		t.Fatalf("req.RawParams[path] = %q, want %q", path, "/Users/niuniu/git_local/ngent/soul.md")
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
	if decoded.Outcome.OptionID != "approve" {
		t.Fatalf("response.outcome.optionId = %q, want %q", decoded.Outcome.OptionID, "approve")
	}
}

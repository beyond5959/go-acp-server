package kimi

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/beyond5959/ngent/internal/agents"
)

func TestHandlePermissionRequestParsesRichToolCallPayload(t *testing.T) {
	t.Parallel()

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("failed to get current file path")
	}
	projectDir := filepath.Join(filepath.Dir(testFile), "..", "..", "..")
	expectedPath := filepath.Join(projectDir, "soul.md")

	raw := json.RawMessage(fmt.Sprintf(`{
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
					"path": %q,
					"oldText": "",
					"newText": "hello"
				}
			]
		}
	}`, expectedPath))

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
	if path, _ := got.RawParams["path"].(string); path != expectedPath {
		t.Fatalf("req.RawParams[path] = %q, want %q", path, expectedPath)
	}
	if len(got.Options) != 3 {
		t.Fatalf("len(req.Options) = %d, want %d", len(got.Options), 3)
	}
	if got.Options[1].OptionID != "approve_for_session" {
		t.Fatalf("req.Options[1].OptionID = %q, want %q", got.Options[1].OptionID, "approve_for_session")
	}
	if got.Options[1].Kind != "allow_always" {
		t.Fatalf("req.Options[1].Kind = %q, want %q", got.Options[1].Kind, "allow_always")
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

func TestHandlePermissionRequestHonorsSelectedOptionID(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"sessionId": "ses_kimi_perm_selected",
		"options": [
			{"optionId":"approve","name":"Allow once","kind":"allow_once"},
			{"optionId":"approve_for_session","name":"Allow for session","kind":"allow_always"},
			{"optionId":"reject","name":"Reject once","kind":"reject_once"}
		],
		"toolCall": {
			"title": "Run shell command",
			"kind": "execute"
		}
	}`)

	resp, err := handlePermissionRequest(
		context.Background(),
		raw,
		func(ctx context.Context, req agents.PermissionRequest) (agents.PermissionResponse, error) {
			return agents.PermissionResponse{
				Outcome:          agents.PermissionOutcomeApproved,
				SelectedOptionID: "approve_for_session",
			}, nil
		},
		true,
	)
	if err != nil {
		t.Fatalf("handlePermissionRequest() error = %v", err)
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
	if decoded.Outcome.OptionID != "approve_for_session" {
		t.Fatalf("response.outcome.optionId = %q, want %q", decoded.Outcome.OptionID, "approve_for_session")
	}
}

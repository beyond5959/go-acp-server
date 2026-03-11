package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSessionTranscriptMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    string
		wantOK  bool
	}{
		{
			name: "assistant final answer kept",
			payload: `{
				"type":"message",
				"role":"assistant",
				"phase":"final_answer",
				"content":[{"type":"output_text","text":"answer"}]
			}`,
			want:   "answer",
			wantOK: true,
		},
		{
			name: "assistant commentary skipped",
			payload: `{
				"type":"message",
				"role":"assistant",
				"phase":"commentary",
				"content":[{"type":"output_text","text":"thinking"}]
			}`,
			wantOK: false,
		},
		{
			name: "bootstrap user message skipped",
			payload: `{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"# AGENTS.md instructions for /tmp/repo\n\n<INSTRUCTIONS>...\n</INSTRUCTIONS>"},{"type":"input_text","text":"\n<environment_context>\n  <cwd>/tmp/repo</cwd>\n</environment_context>"}]
			}`,
			wantOK: false,
		},
		{
			name: "ide context user message extracts request",
			payload: `{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"# Context from my IDE setup:\n\n## Active file: foo.go\n\n## My request for Codex:\nkeep only the real request"}]
			}`,
			want:   "keep only the real request",
			wantOK: true,
		},
		{
			name: "conversation summary extracts current input",
			payload: `{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"[Conversation Summary]\n(empty)\n\n[Recent Turns]\nUser: one\nAssistant: two\n----\n\n[Current User Input]\nreal prompt"}]
			}`,
			want:   "real prompt",
			wantOK: true,
		},
		{
			name: "plain user message kept",
			payload: `{
				"type":"message",
				"role":"user",
				"content":[{"type":"input_text","text":"plain prompt"}]
			}`,
			want:   "plain prompt",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg, ok, err := parseSessionTranscriptMessage("2026-03-11T11:04:48.797Z", json.RawMessage(tt.payload))
			if err != nil {
				t.Fatalf("parseSessionTranscriptMessage() error = %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("parseSessionTranscriptMessage() ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got := strings.TrimSpace(msg.Content); got != tt.want {
				t.Fatalf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

package codex

import (
	"testing"

	"github.com/beyond5959/ngent/internal/agents"
)

func TestNormalizeCodexSessionListResultUsesStableThreadID(t *testing.T) {
	result := normalizeCodexSessionListResult(agents.SessionListResult{
		Sessions: []agents.SessionInfo{
			{
				SessionID: "session-1",
				Meta: map[string]any{
					codexMetaThreadID: "thread-123",
				},
			},
		},
	})

	if got, want := len(result.Sessions), 1; got != want {
		t.Fatalf("len(sessions) = %d, want %d", got, want)
	}
	if got, want := result.Sessions[0].SessionID, "thread-123"; got != want {
		t.Fatalf("sessionId = %q, want %q", got, want)
	}
	if got, want := codexLoadSessionID(result.Sessions[0]), "session-1"; got != want {
		t.Fatalf("load session id = %q, want %q", got, want)
	}
}

func TestCodexSessionMatchesIDAcceptsStableAndRawIDs(t *testing.T) {
	session := normalizeCodexSessionInfo(agents.SessionInfo{
		SessionID: "session-7",
		Meta: map[string]any{
			codexMetaThreadID: "thread-789",
		},
	})

	if !codexSessionMatchesID(session, "thread-789") {
		t.Fatalf("stable session id did not match")
	}
	if !codexSessionMatchesID(session, "session-7") {
		t.Fatalf("raw session id did not match")
	}
	if codexSessionMatchesID(session, "session-8") {
		t.Fatalf("unexpected match for unrelated session id")
	}
}

func TestCodexStableSessionIDFallsBackToRawSessionID(t *testing.T) {
	session := normalizeCodexSessionInfo(agents.SessionInfo{SessionID: "session-9"})

	if got, want := session.SessionID, "session-9"; got != want {
		t.Fatalf("sessionId = %q, want %q", got, want)
	}
	if got, want := codexLoadSessionID(session), "session-9"; got != want {
		t.Fatalf("load session id = %q, want %q", got, want)
	}
}

func TestCodexShouldDeferInitialSessionBinding(t *testing.T) {
	if !codexShouldDeferInitialSessionBinding("", "session-1", "session-1") {
		t.Fatalf("expected provisional new-session binding to defer")
	}
	if codexShouldDeferInitialSessionBinding("thread-123", "session-1", "session-1") {
		t.Fatalf("did not expect loaded session binding to defer")
	}
	if codexShouldDeferInitialSessionBinding("", "session-1", "thread-123") {
		t.Fatalf("did not expect stable session binding to defer")
	}
}

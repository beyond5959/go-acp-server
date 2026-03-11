package codex

import (
	"strings"

	"github.com/beyond5959/ngent/internal/agents"
)

const (
	codexMetaThreadID      = "threadId"
	codexMetaLoadSessionID = "ngentLoadSessionId"
)

func codexShouldDeferInitialSessionBinding(
	requestedSessionID string,
	runtimeSessionID string,
	stableSessionID string,
) bool {
	requestedSessionID = strings.TrimSpace(requestedSessionID)
	runtimeSessionID = strings.TrimSpace(runtimeSessionID)
	stableSessionID = strings.TrimSpace(stableSessionID)
	return requestedSessionID == "" &&
		runtimeSessionID != "" &&
		runtimeSessionID == stableSessionID
}

func codexStableSessionID(session agents.SessionInfo) string {
	if session.Meta != nil {
		if threadID, _ := session.Meta[codexMetaThreadID].(string); strings.TrimSpace(threadID) != "" {
			return strings.TrimSpace(threadID)
		}
	}
	return strings.TrimSpace(session.SessionID)
}

func codexLoadSessionID(session agents.SessionInfo) string {
	if session.Meta != nil {
		if rawID, _ := session.Meta[codexMetaLoadSessionID].(string); strings.TrimSpace(rawID) != "" {
			return strings.TrimSpace(rawID)
		}
	}
	return strings.TrimSpace(session.SessionID)
}

func normalizeCodexSessionInfo(session agents.SessionInfo) agents.SessionInfo {
	normalized := agents.CloneSessionInfo(session)
	rawSessionID := strings.TrimSpace(normalized.SessionID)
	stableSessionID := codexStableSessionID(normalized)
	if stableSessionID == "" {
		stableSessionID = rawSessionID
	}
	if rawSessionID != "" {
		if normalized.Meta == nil {
			normalized.Meta = make(map[string]any)
		}
		normalized.Meta[codexMetaLoadSessionID] = rawSessionID
	}
	normalized.SessionID = stableSessionID
	return normalized
}

func normalizeCodexSessionListResult(result agents.SessionListResult) agents.SessionListResult {
	normalized := agents.SessionListResult{
		NextCursor: strings.TrimSpace(result.NextCursor),
		Sessions:   make([]agents.SessionInfo, 0, len(result.Sessions)),
	}
	for _, session := range result.Sessions {
		item := normalizeCodexSessionInfo(session)
		if item.SessionID == "" {
			continue
		}
		normalized.Sessions = append(normalized.Sessions, item)
	}
	return agents.CloneSessionListResult(normalized)
}

func codexSessionMatchesID(session agents.SessionInfo, requestedID string) bool {
	requestedID = strings.TrimSpace(requestedID)
	if requestedID == "" {
		return false
	}
	return requestedID == strings.TrimSpace(session.SessionID) ||
		requestedID == codexLoadSessionID(session)
}

package agents

import (
	"context"
	"errors"
	"strings"
)

var (
	// ErrSessionListUnsupported indicates the provider cannot list/load ACP sessions.
	ErrSessionListUnsupported = errors.New("agents: session list unsupported")
	// ErrSessionLoadUnsupported indicates the provider cannot load existing ACP sessions.
	ErrSessionLoadUnsupported = errors.New("agents: session load unsupported")
	// ErrSessionNotFound indicates the requested ACP session does not exist.
	ErrSessionNotFound = errors.New("agents: session not found")
)

// SessionInfo describes one ACP session entry returned by session/list.
type SessionInfo struct {
	SessionID string         `json:"sessionId"`
	CWD       string         `json:"cwd,omitempty"`
	Title     string         `json:"title,omitempty"`
	UpdatedAt string         `json:"updatedAt,omitempty"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

// SessionListRequest contains one ACP session/list request.
type SessionListRequest struct {
	CWD    string
	Cursor string
}

// SessionListResult contains one ACP session/list response.
type SessionListResult struct {
	Sessions   []SessionInfo `json:"sessions"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// SessionLister exposes ACP session/list for one provider.
type SessionLister interface {
	ListSessions(ctx context.Context, req SessionListRequest) (SessionListResult, error)
}

// SessionTranscriptMessage describes one replayable session transcript message.
type SessionTranscriptMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
}

// SessionTranscriptRequest contains one session transcript request.
type SessionTranscriptRequest struct {
	CWD       string
	SessionID string
}

// SessionTranscriptResult contains one session transcript payload.
type SessionTranscriptResult struct {
	Messages      []SessionTranscriptMessage `json:"messages"`
	ConfigOptions []ConfigOption             `json:"configOptions,omitempty"`
}

// SessionInfoUpdate describes one incremental ACP session metadata update.
type SessionInfoUpdate struct {
	SessionID string
	Title     string
	HasTitle  bool
}

// SessionTranscriptLoader exposes replayable session transcript messages.
type SessionTranscriptLoader interface {
	LoadSessionTranscript(ctx context.Context, req SessionTranscriptRequest) (SessionTranscriptResult, error)
}

// FindSessionByID pages one SessionLister until the requested session is found.
func FindSessionByID(
	ctx context.Context,
	lister SessionLister,
	cwd, sessionID string,
) (SessionInfo, error) {
	if lister == nil {
		return SessionInfo{}, ErrSessionListUnsupported
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionInfo{}, ErrSessionNotFound
	}

	cursor := ""
	for {
		result, err := lister.ListSessions(ctx, SessionListRequest{
			CWD:    cwd,
			Cursor: cursor,
		})
		if err != nil {
			return SessionInfo{}, err
		}
		for _, session := range result.Sessions {
			if strings.TrimSpace(session.SessionID) != sessionID {
				continue
			}
			return CloneSessionInfo(session), nil
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			break
		}
	}
	return SessionInfo{}, ErrSessionNotFound
}

// SessionBoundHandler receives the session ID bound to the active turn.
type SessionBoundHandler func(ctx context.Context, sessionID string) error

type sessionBoundHandlerContextKey struct{}

// SessionInfoHandler receives one incremental ACP session metadata update.
type SessionInfoHandler func(ctx context.Context, update SessionInfoUpdate) error

type sessionInfoHandlerContextKey struct{}

// WithSessionBoundHandler binds one session callback to context.
func WithSessionBoundHandler(ctx context.Context, handler SessionBoundHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionBoundHandlerContextKey{}, handler)
}

// SessionBoundHandlerFromContext gets session callback from context, if present.
func SessionBoundHandlerFromContext(ctx context.Context) (SessionBoundHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(sessionBoundHandlerContextKey{}).(SessionBoundHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

// NotifySessionBound reports the bound session ID to the active callback, if any.
func NotifySessionBound(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	handler, ok := SessionBoundHandlerFromContext(ctx)
	if !ok {
		return nil
	}
	return handler(ctx, sessionID)
}

// WithSessionInfoHandler binds one session metadata callback to context.
func WithSessionInfoHandler(ctx context.Context, handler SessionInfoHandler) context.Context {
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionInfoHandlerContextKey{}, handler)
}

// SessionInfoHandlerFromContext gets session metadata callback from context, if present.
func SessionInfoHandlerFromContext(ctx context.Context) (SessionInfoHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(sessionInfoHandlerContextKey{}).(SessionInfoHandler)
	if !ok || handler == nil {
		return nil, false
	}
	return handler, true
}

// NotifySessionInfoUpdate reports one ACP session metadata change to the active callback, if any.
func NotifySessionInfoUpdate(ctx context.Context, update SessionInfoUpdate) error {
	update.SessionID = strings.TrimSpace(update.SessionID)
	update.Title = strings.TrimSpace(update.Title)
	if update.SessionID == "" || !update.HasTitle {
		return nil
	}
	handler, ok := SessionInfoHandlerFromContext(ctx)
	if !ok {
		return nil
	}
	return handler(ctx, update)
}

// CloneSessionInfo returns a trimmed deep copy of the provided session entry.
func CloneSessionInfo(session SessionInfo) SessionInfo {
	cloned := SessionInfo{
		SessionID: strings.TrimSpace(session.SessionID),
		CWD:       strings.TrimSpace(session.CWD),
		Title:     strings.TrimSpace(session.Title),
		UpdatedAt: strings.TrimSpace(session.UpdatedAt),
	}
	if len(session.Meta) != 0 {
		cloned.Meta = make(map[string]any, len(session.Meta))
		for key, value := range session.Meta {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			cloned.Meta[key] = value
		}
		if len(cloned.Meta) == 0 {
			cloned.Meta = nil
		}
	}
	return cloned
}

// CloneSessionListResult returns a trimmed deep copy of one session/list result.
func CloneSessionListResult(result SessionListResult) SessionListResult {
	cloned := SessionListResult{
		NextCursor: strings.TrimSpace(result.NextCursor),
	}
	if len(result.Sessions) == 0 {
		return cloned
	}

	cloned.Sessions = make([]SessionInfo, 0, len(result.Sessions))
	seen := make(map[string]struct{}, len(result.Sessions))
	for _, session := range result.Sessions {
		item := CloneSessionInfo(session)
		if item.SessionID == "" {
			continue
		}
		if _, ok := seen[item.SessionID]; ok {
			continue
		}
		seen[item.SessionID] = struct{}{}
		cloned.Sessions = append(cloned.Sessions, item)
	}
	if len(cloned.Sessions) == 0 {
		cloned.Sessions = nil
	}
	return cloned
}

// CloneSessionTranscriptResult returns a trimmed deep copy of one session transcript.
func CloneSessionTranscriptResult(result SessionTranscriptResult) SessionTranscriptResult {
	if len(result.Messages) == 0 && len(result.ConfigOptions) == 0 {
		return SessionTranscriptResult{}
	}

	cloned := SessionTranscriptResult{
		Messages: make([]SessionTranscriptMessage, 0, len(result.Messages)),
	}
	for _, message := range result.Messages {
		role := strings.TrimSpace(strings.ToLower(message.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		cloned.Messages = append(cloned.Messages, SessionTranscriptMessage{
			Role:      role,
			Content:   content,
			Timestamp: strings.TrimSpace(message.Timestamp),
		})
	}
	if len(cloned.Messages) == 0 {
		cloned.Messages = nil
	}
	cloned.ConfigOptions = CloneConfigOptions(result.ConfigOptions)
	if len(cloned.ConfigOptions) == 0 {
		cloned.ConfigOptions = nil
	}
	return cloned
}

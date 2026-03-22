package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/beyond5959/acp-adapter/pkg/codexacp"
	"github.com/beyond5959/ngent/internal/agents"
	"github.com/beyond5959/ngent/internal/agents/acpmodel"
	"github.com/beyond5959/ngent/internal/observability"
)

var _ agents.SessionTranscriptLoader = (*Client)(nil)

// LoadSessionTranscript replays one Codex session through ACP session/load.
func (c *Client) LoadSessionTranscript(
	ctx context.Context,
	req agents.SessionTranscriptRequest,
) (agents.SessionTranscriptResult, error) {
	if c == nil {
		return agents.SessionTranscriptResult{}, errors.New("codex: nil client")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	startCtx, cancel := context.WithTimeout(ctx, c.startTimeout)
	defer cancel()

	runtime, caps, err := c.startRuntime(startCtx)
	if err != nil {
		return agents.SessionTranscriptResult{}, err
	}
	defer runtime.Close()

	if !caps.CanLoad {
		return agents.SessionTranscriptResult{}, agents.ErrSessionLoadUnsupported
	}

	session, err := c.findSessionInRuntime(startCtx, runtime, req.CWD, req.SessionID)
	if err != nil {
		return agents.SessionTranscriptResult{}, err
	}

	loadSessionID := strings.TrimSpace(codexLoadSessionID(session))
	if loadSessionID == "" {
		return agents.SessionTranscriptResult{}, agents.ErrSessionNotFound
	}

	return c.collectSessionReplay(ctx, runtime, loadSessionID)
}

func (c *Client) findSessionInRuntime(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	cwd, sessionID string,
) (agents.SessionInfo, error) {
	cursor := ""
	for {
		result, err := c.listSessionsRaw(ctx, runtime, agents.SessionListRequest{
			CWD:    cwd,
			Cursor: cursor,
		})
		if err != nil {
			return agents.SessionInfo{}, err
		}
		for _, session := range result.Sessions {
			normalized := normalizeCodexSessionInfo(session)
			if codexSessionMatchesID(normalized, sessionID) {
				return normalized, nil
			}
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			break
		}
	}
	return agents.SessionInfo{}, agents.ErrSessionNotFound
}

func (c *Client) findSession(
	ctx context.Context,
	cwd, sessionID string,
) (agents.SessionInfo, error) {
	cursor := ""
	for {
		result, err := c.ListSessions(ctx, agents.SessionListRequest{
			CWD:    cwd,
			Cursor: cursor,
		})
		if err != nil {
			return agents.SessionInfo{}, err
		}
		for _, session := range result.Sessions {
			if codexSessionMatchesID(session, sessionID) {
				return agents.CloneSessionInfo(session), nil
			}
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			break
		}
	}
	return agents.SessionInfo{}, agents.ErrSessionNotFound
}

func (c *Client) collectSessionReplay(
	ctx context.Context,
	runtime *codexacp.EmbeddedRuntime,
	sessionID string,
) (agents.SessionTranscriptResult, error) {
	if runtime == nil {
		return agents.SessionTranscriptResult{}, errors.New("codex: embedded runtime is nil")
	}

	collector := agents.NewACPTranscriptCollector()
	updates, unsubscribe := runtime.SubscribeUpdates(256)
	defer unsubscribe()

	type loadResult struct {
		err    error
		result json.RawMessage
	}
	loadDone := make(chan loadResult, 1)
	go func() {
		rawResult, err := c.clientRequest(ctx, runtime, "session/load", map[string]any{
			"sessionId":  sessionID,
			"cwd":        c.Dir(),
			"mcpServers": []any{},
		})
		if err != nil {
			loadDone <- loadResult{err: fmt.Errorf("codex: session/load failed: %w", err)}
			return
		}
		loadDone <- loadResult{result: rawResult.Result}
	}()

	for {
		select {
		case <-ctx.Done():
			return agents.SessionTranscriptResult{}, ctx.Err()
		case result := <-loadDone:
			if result.err != nil {
				return agents.SessionTranscriptResult{}, result.err
			}
			if err := drainCodexReplayUpdates(collector, updates); err != nil {
				return agents.SessionTranscriptResult{}, err
			}
			replay := collector.Result()
			replay.ConfigOptions = acpmodel.ExtractConfigOptions(result.result)
			return agents.CloneSessionTranscriptResult(replay), nil
		case msg, ok := <-updates:
			if !ok {
				if ctx.Err() != nil {
					return agents.SessionTranscriptResult{}, ctx.Err()
				}
				return agents.SessionTranscriptResult{}, errors.New("codex: embedded updates channel closed")
			}
			if err := consumeCodexReplayUpdate(collector, msg); err != nil {
				return agents.SessionTranscriptResult{}, err
			}
		}
	}
}

func drainCodexReplayUpdates(
	collector *agents.ACPTranscriptCollector,
	updates <-chan codexacp.RPCMessage,
) error {
	for {
		select {
		case msg, ok := <-updates:
			if !ok {
				return nil
			}
			if err := consumeCodexReplayUpdate(collector, msg); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func consumeCodexReplayUpdate(
	collector *agents.ACPTranscriptCollector,
	msg codexacp.RPCMessage,
) error {
	observability.LogACPMessage("codex-embedded", "inbound", msg)
	if msg.Method != methodSessionUpdate || len(msg.Params) == 0 {
		return nil
	}
	return collector.HandleRawUpdate(msg.Params)
}

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	jsonRPCVersion = "2.0"
	methodNotFound = -32601
	invalidParams  = -32602
)

func main() {
	agent := &fakeACPAgent{
		reader:    bufio.NewReader(os.Stdin),
		writer:    bufio.NewWriter(os.Stdout),
		cancelled: make(map[string]bool),
	}
	if err := agent.run(); err != nil && !errors.Is(err, io.EOF) {
		// keep stdout protocol-only; report diagnostics on stderr only.
		_, _ = fmt.Fprintf(os.Stderr, "fake_acp_agent error: %v\n", err)
		os.Exit(1)
	}
}

type fakeACPAgent struct {
	reader *bufio.Reader
	writer *bufio.Writer

	nextPermissionID int64
	cancelled        map[string]bool
}

func (a *fakeACPAgent) run() error {
	for {
		msg, err := a.readMessage()
		if err != nil {
			return err
		}
		if msg.Method == "" || len(msg.ID) == 0 {
			// Top-level notifications or responses are ignored in server mode.
			continue
		}
		if err := a.handleRequest(msg); err != nil {
			return err
		}
	}
}

func (a *fakeACPAgent) handleRequest(msg rpcMessage) error {
	switch msg.Method {
	case "initialize":
		return a.replyResult(msg.ID, map[string]any{
			"serverInfo": map[string]any{
				"name":    "fake-acp-agent",
				"version": "0.1.0",
			},
		})
	case "session/new":
		return a.replyResult(msg.ID, map[string]any{
			"sessionId": "fake-session-1",
		})
	case "session/prompt":
		return a.handlePrompt(msg)
	case "session/cancel":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if len(msg.Params) > 0 {
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				return a.replyError(msg.ID, invalidParams, "invalid session/cancel params")
			}
		}
		if params.SessionID != "" {
			a.cancelled[params.SessionID] = true
		}
		return a.replyResult(msg.ID, map[string]any{"ok": true})
	default:
		return a.replyError(msg.ID, methodNotFound, "method not found")
	}
}

func (a *fakeACPAgent) handlePrompt(promptReq rpcMessage) error {
	var params struct {
		SessionID string `json:"sessionId"`
		Input     string `json:"input"`
	}
	if err := json.Unmarshal(promptReq.Params, &params); err != nil {
		return a.replyError(promptReq.ID, invalidParams, "invalid session/prompt params")
	}
	if strings.TrimSpace(params.SessionID) == "" {
		return a.replyError(promptReq.ID, invalidParams, "sessionId is required")
	}

	if a.cancelled[params.SessionID] {
		return a.replyResult(promptReq.ID, map[string]any{"stopReason": "cancelled"})
	}

	if err := a.sendNotification("session/update", map[string]any{
		"sessionId": params.SessionID,
		"delta":     "before-permission ",
	}); err != nil {
		return err
	}
	if err := a.sendNotification("session/update", map[string]any{
		"sessionId": params.SessionID,
		"delta":     "need-approval ",
	}); err != nil {
		return err
	}

	permissionID := a.nextPermissionRequestID()
	if err := a.writeMessage(rpcMessage{
		JSONRPC: jsonRPCVersion,
		ID:      json.RawMessage(strconv.AppendInt(nil, permissionID, 10)),
		Method:  "session/request_permission",
		Params: mustJSON(map[string]any{
			"approval":  "command",
			"command":   "echo fake-acp-agent",
			"sessionId": params.SessionID,
		}),
	}); err != nil {
		return err
	}

	outcome, cancelled, err := a.waitPermissionDecision(permissionID, params.SessionID)
	if err != nil {
		return err
	}
	if cancelled || outcome == "declined" || outcome == "cancelled" {
		return a.replyResult(promptReq.ID, map[string]any{
			"stopReason": "cancelled",
		})
	}

	if err := a.sendNotification("session/update", map[string]any{
		"sessionId": params.SessionID,
		"delta":     "after-permission ",
	}); err != nil {
		return err
	}

	return a.replyResult(promptReq.ID, map[string]any{
		"stopReason": "end_turn",
	})
}

func (a *fakeACPAgent) waitPermissionDecision(permissionID int64, sessionID string) (outcome string, cancelled bool, err error) {
	for {
		msg, readErr := a.readMessage()
		if readErr != nil {
			return "", false, readErr
		}

		if msg.Method != "" && len(msg.ID) > 0 {
			switch msg.Method {
			case "session/cancel":
				var params struct {
					SessionID string `json:"sessionId"`
				}
				if len(msg.Params) > 0 {
					if err := json.Unmarshal(msg.Params, &params); err != nil {
						if replyErr := a.replyError(msg.ID, invalidParams, "invalid session/cancel params"); replyErr != nil {
							return "", false, replyErr
						}
						continue
					}
				}
				if params.SessionID != "" {
					a.cancelled[params.SessionID] = true
				}
				if replyErr := a.replyResult(msg.ID, map[string]any{"ok": true}); replyErr != nil {
					return "", false, replyErr
				}
				return "cancelled", true, nil
			default:
				if replyErr := a.replyError(msg.ID, methodNotFound, "method not found"); replyErr != nil {
					return "", false, replyErr
				}
				continue
			}
		}

		if msg.Method == "" && len(msg.ID) > 0 && strings.TrimSpace(string(msg.ID)) == strconv.FormatInt(permissionID, 10) {
			if msg.Error != nil {
				return "declined", false, nil
			}
			var result struct {
				Outcome string `json:"outcome"`
			}
			if err := json.Unmarshal(msg.Result, &result); err != nil {
				return "declined", false, nil
			}
			switch strings.TrimSpace(result.Outcome) {
			case "approved":
				return "approved", false, nil
			case "cancelled":
				return "cancelled", false, nil
			default:
				return "declined", false, nil
			}
		}
	}
}

func (a *fakeACPAgent) sendNotification(method string, payload any) error {
	return a.writeMessage(rpcMessage{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  mustJSON(payload),
	})
}

func (a *fakeACPAgent) replyResult(id json.RawMessage, payload any) error {
	return a.writeMessage(rpcMessage{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  mustJSON(payload),
	})
}

func (a *fakeACPAgent) replyError(id json.RawMessage, code int, message string) error {
	return a.writeMessage(rpcMessage{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	})
}

func (a *fakeACPAgent) writeMessage(msg rpcMessage) error {
	wire, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := a.writer.Write(wire); err != nil {
		return err
	}
	if err := a.writer.WriteByte('\n'); err != nil {
		return err
	}
	return a.writer.Flush()
}

func (a *fakeACPAgent) readMessage() (rpcMessage, error) {
	for {
		line, err := a.reader.ReadBytes('\n')
		if err != nil {
			return rpcMessage{}, err
		}
		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return rpcMessage{}, fmt.Errorf("decode json-rpc: %w", err)
		}
		return msg, nil
	}
}

func (a *fakeACPAgent) nextPermissionRequestID() int64 {
	a.nextPermissionID++
	return a.nextPermissionID
}

func mustJSON(payload any) json.RawMessage {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return data
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

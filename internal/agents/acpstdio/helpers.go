package acpstdio

import (
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// ParseSessionID returns sessionId from a JSON-RPC result payload.
func ParseSessionID(raw json.RawMessage) string {
	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.SessionID)
}

// ParseStopReason returns stopReason from a JSON-RPC result payload.
func ParseStopReason(raw json.RawMessage) string {
	var payload struct {
		StopReason string `json:"stopReason"`
	}
	if len(raw) == 0 {
		return ""
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.StopReason)
}

// TerminateProcess waits for process exit and force-kills after timeout.
func TerminateProcess(cmd *exec.Cmd, errCh <-chan error, timeout time.Duration) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if errCh == nil {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return
	}

	select {
	case <-time.After(timeout):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-errCh:
		case <-time.After(timeout):
		}
	case <-errCh:
	}
}

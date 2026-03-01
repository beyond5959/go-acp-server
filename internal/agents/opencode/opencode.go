package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/beyond5959/go-acp-server/internal/agents"
)

const (
	jsonRPCVersion = "2.0"
	methodNotFound = -32601

	updateTypeMessageChunk = "agent_message_chunk"
)

// Config configures the OpenCode ACP stdio provider.
type Config struct {
	// Dir is the working directory passed to opencode acp --cwd.
	Dir string
	// ModelID is the optional model identifier (e.g. "anthropic/claude-3-5-haiku-20241022").
	// When empty, OpenCode uses its configured default model.
	ModelID string
}

// Client runs one opencode acp process per Stream call.
type Client struct {
	dir     string
	modelID string
}

var _ agents.Streamer = (*Client)(nil)

// New constructs an OpenCode ACP client.
func New(cfg Config) (*Client, error) {
	dir := strings.TrimSpace(cfg.Dir)
	if dir == "" {
		return nil, errors.New("opencode: Dir is required")
	}
	return &Client{
		dir:     dir,
		modelID: strings.TrimSpace(cfg.ModelID),
	}, nil
}

// Preflight checks that the opencode binary is available in PATH.
func Preflight() error {
	_, err := exec.LookPath("opencode")
	if err != nil {
		return fmt.Errorf("opencode binary not found in PATH: %w", err)
	}
	return nil
}

// Name returns the provider identifier.
func (c *Client) Name() string { return "opencode" }

// Stream spawns opencode acp, runs one turn, and streams deltas via onDelta.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("opencode: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("opencode: onDelta callback is required")
	}

	cmd := exec.Command("opencode", "acp", "--cwd", c.dir)
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: open stdout pipe: %w", err)
	}
	// Discard stderr to avoid blocking.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: start process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { errCh <- cmd.Wait() }()

	conn := newRPCConn(stdin, stdout)
	defer conn.Close()
	defer terminateProcess(cmd, errCh)

	// 1. initialize — protocolVersion must be an integer.
	if _, err := conn.Call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "agent-hub-server",
			"version": "0.1.0",
		},
		"protocolVersion": 1,
	}); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: initialize: %w", err)
	}

	// 2. session/new — server assigns sessionId; mcpServers is required.
	newResult, err := conn.Call(ctx, "session/new", map[string]any{
		"cwd":        c.dir,
		"mcpServers": []any{},
	})
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: session/new: %w", err)
	}
	sessionID := parseSessionID(newResult)
	if sessionID == "" {
		return agents.StopReasonEndTurn, errors.New("opencode: session/new returned empty sessionId")
	}

	// 3. Wire streaming: agent_message_chunk → onDelta.
	conn.SetNotificationHandler(func(msg rpcMessage) error {
		if msg.Method != "session/update" {
			return nil
		}
		var payload struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if len(msg.Params) == 0 {
			return nil
		}
		if err := json.Unmarshal(msg.Params, &payload); err != nil {
			return nil // ignore malformed updates
		}
		if payload.Update.SessionUpdate == updateTypeMessageChunk {
			if text := payload.Update.Content.Text; text != "" {
				return onDelta(text)
			}
		}
		return nil
	})

	// 4. session/prompt.
	promptParams := map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": input}},
	}
	if c.modelID != "" {
		promptParams["modelId"] = c.modelID
	}

	promptResult, err := conn.Call(ctx, "session/prompt", promptParams)
	if err != nil {
		if ctx.Err() != nil {
			c.sendCancel(conn, sessionID)
			return agents.StopReasonCancelled, nil
		}
		return agents.StopReasonEndTurn, fmt.Errorf("opencode: session/prompt: %w", err)
	}

	if parseStopReason(promptResult) == "cancelled" {
		return agents.StopReasonCancelled, nil
	}
	return agents.StopReasonEndTurn, nil
}

func (c *Client) sendCancel(conn *rpcConn, sessionID string) {
	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = conn.Call(cancelCtx, "session/cancel", map[string]any{"sessionId": sessionID})
}

// ── JSON-RPC 2.0 transport (self-contained, same pattern as internal/agents/acp) ──

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

type rpcConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan rpcMessage
	nextID    int64

	notifMu sync.RWMutex
	notif   func(rpcMessage) error

	closeOnce sync.Once
	done      chan struct{}
	doneErrMu sync.RWMutex
	doneErr   error
}

func newRPCConn(stdin io.WriteCloser, stdout io.ReadCloser) *rpcConn {
	c := &rpcConn{
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[string]chan rpcMessage),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *rpcConn) Close() { c.closeWithErr(io.EOF) }

func (c *rpcConn) SetNotificationHandler(fn func(rpcMessage) error) {
	c.notifMu.Lock()
	c.notif = fn
	c.notifMu.Unlock()
}

func (c *rpcConn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("opencode: marshal %s params: %w", method, err)
	}

	id := atomic.AddInt64(&c.nextID, 1)
	idRaw := json.RawMessage(strconv.AppendInt(nil, id, 10))
	idKey := string(idRaw)
	respCh := make(chan rpcMessage, 1)

	c.pendingMu.Lock()
	c.pending[idKey] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
	}()

	if err := c.write(rpcMessage{
		JSONRPC: jsonRPCVersion,
		ID:      idRaw,
		Method:  method,
		Params:  paramsJSON,
	}); err != nil {
		return nil, err
	}

	select {
	case <-c.done:
		if e := c.doneError(); e != nil && !errors.Is(e, io.EOF) {
			return nil, e
		}
		return nil, errors.New("opencode: connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-respCh:
		if !ok {
			return nil, errors.New("opencode: connection closed")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("opencode: rpc %s error (%d): %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *rpcConn) write(msg rpcMessage) error {
	wire, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("opencode: marshal rpc message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(wire); err != nil {
		return fmt.Errorf("opencode: write rpc: %w", err)
	}
	if _, err := c.stdin.Write([]byte("\n")); err != nil {
		return fmt.Errorf("opencode: write rpc delimiter: %w", err)
	}
	return nil
}

func (c *rpcConn) readLoop() {
	rd := bufio.NewReader(c.stdout)
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			if e := c.consume(line); e != nil {
				c.closeWithErr(e)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.closeWithErr(io.EOF)
			} else {
				c.closeWithErr(fmt.Errorf("opencode: read stdout: %w", err))
			}
			return
		}
	}
}

func (c *rpcConn) consume(line []byte) error {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return nil
	}
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return fmt.Errorf("opencode: decode rpc line: %w", err)
	}

	// Response: has id, no method.
	if msg.Method == "" && len(msg.ID) > 0 {
		key := string(msg.ID)
		c.pendingMu.Lock()
		ch, ok := c.pending[key]
		if ok {
			delete(c.pending, key)
		}
		c.pendingMu.Unlock()
		if ok {
			ch <- msg
		}
		return nil
	}

	// Notification: has method, no id.
	if msg.Method != "" && len(msg.ID) == 0 {
		c.notifMu.RLock()
		fn := c.notif
		c.notifMu.RUnlock()
		if fn != nil {
			return fn(msg)
		}
		return nil
	}

	// Inbound request (method + id): OpenCode doesn't send these in basic flow;
	// reply method-not-found to avoid blocking the remote side.
	if msg.Method != "" && len(msg.ID) > 0 {
		return c.write(rpcMessage{
			JSONRPC: jsonRPCVersion,
			ID:      msg.ID,
			Error:   &rpcError{Code: methodNotFound, Message: "method not found"},
		})
	}

	return nil
}

func (c *rpcConn) closeWithErr(err error) {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()

		c.doneErrMu.Lock()
		c.doneErr = err
		c.doneErrMu.Unlock()

		c.pendingMu.Lock()
		for k, ch := range c.pending {
			close(ch)
			delete(c.pending, k)
		}
		c.pendingMu.Unlock()

		close(c.done)
	})
}

func (c *rpcConn) doneError() error {
	c.doneErrMu.RLock()
	defer c.doneErrMu.RUnlock()
	return c.doneErr
}

// ── helpers ──

func terminateProcess(cmd *exec.Cmd, errCh <-chan error) {
	select {
	case <-time.After(2 * time.Second):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
	case <-errCh:
	}
}

func parseSessionID(raw json.RawMessage) string {
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

func parseStopReason(raw json.RawMessage) string {
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

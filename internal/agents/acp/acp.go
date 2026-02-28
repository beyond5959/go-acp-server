package acp

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

	"github.com/example/code-agent-hub-server/internal/agents"
)

const (
	jsonRPCVersion   = "2.0"
	methodNotFound   = -32601
	internalRPCError = -32603
)

// Config configures ACP stdio provider.
type Config struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Name    string
}

// Client talks to one ACP agent process over stdio JSON-RPC.
type Client struct {
	command string
	args    []string
	dir     string
	env     []string
	name    string
}

var _ agents.Streamer = (*Client)(nil)
var _ io.Closer = (*Client)(nil)

// New constructs one ACP stdio client.
func New(cfg Config) (*Client, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return nil, errors.New("acp: command is required")
	}

	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = "acp-stdio"
	}

	args := make([]string, len(cfg.Args))
	copy(args, cfg.Args)

	env := make([]string, len(cfg.Env))
	copy(env, cfg.Env)

	return &Client{
		command: command,
		args:    args,
		dir:     strings.TrimSpace(cfg.Dir),
		env:     env,
		name:    name,
	}, nil
}

// Name returns provider name.
func (c *Client) Name() string {
	if c == nil || c.name == "" {
		return "acp-stdio"
	}
	return c.name
}

// Close closes client resources. Stream mode is process-per-call, so this is a no-op.
func (c *Client) Close() error {
	return nil
}

// Stream runs one ACP lifecycle turn over stdio JSON-RPC.
func (c *Client) Stream(ctx context.Context, input string, onDelta func(delta string) error) (agents.StopReason, error) {
	if c == nil {
		return agents.StopReasonEndTurn, errors.New("acp: nil client")
	}
	if onDelta == nil {
		return agents.StopReasonEndTurn, errors.New("acp: onDelta callback is required")
	}

	cmd := exec.Command(c.command, c.args...)
	if c.dir != "" {
		cmd.Dir = c.dir
	}
	if len(c.env) > 0 {
		cmd.Env = append(os.Environ(), c.env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("acp: open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("acp: open stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("acp: open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("acp: start agent process: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()
	go func() {
		errCh <- cmd.Wait()
	}()

	conn := newRPCConn(stdin, stdout)
	defer conn.Close()
	defer terminateProcess(cmd, errCh)

	if _, err := conn.Call(ctx, "initialize", map[string]any{
		"client": map[string]any{
			"name": "code-agent-hub-server",
		},
	}); err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("acp: initialize failed: %w", err)
	}

	newSessionResult, err := conn.Call(ctx, "session/new", map[string]any{})
	if err != nil {
		return agents.StopReasonEndTurn, fmt.Errorf("acp: session/new failed: %w", err)
	}
	sessionID := parseSessionID(newSessionResult)
	if sessionID == "" {
		return agents.StopReasonEndTurn, errors.New("acp: session/new returned empty sessionId")
	}

	conn.SetNotificationHandler(func(msg rpcMessage) error {
		if msg.Method != "session/update" {
			return nil
		}
		var payload struct {
			Delta string `json:"delta"`
		}
		if len(msg.Params) == 0 {
			return nil
		}
		if err := json.Unmarshal(msg.Params, &payload); err != nil {
			return fmt.Errorf("acp: decode session/update: %w", err)
		}
		return onDelta(payload.Delta)
	})

	conn.SetRequestHandler(func(msg rpcMessage) error {
		if msg.Method != "session/request_permission" {
			return conn.ReplyMethodNotFound(msg.ID)
		}
		return c.handlePermissionRequest(ctx, conn, msg)
	})

	promptResult, err := conn.Call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"input":     input,
	})
	if err != nil {
		if ctx.Err() != nil {
			c.sendSessionCancel(conn, sessionID)
			return agents.StopReasonCancelled, nil
		}
		return agents.StopReasonEndTurn, fmt.Errorf("acp: session/prompt failed: %w", err)
	}

	reason := parseStopReason(promptResult)
	if reason == "cancelled" {
		return agents.StopReasonCancelled, nil
	}
	return agents.StopReasonEndTurn, nil
}

func (c *Client) handlePermissionRequest(ctx context.Context, conn *rpcConn, msg rpcMessage) error {
	rawParams := make(map[string]any)
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &rawParams); err != nil {
			return conn.ReplyError(msg.ID, internalRPCError, "invalid permission params")
		}
	}

	req := agents.PermissionRequest{
		RequestID: idToString(msg.ID),
		Approval:  stringValue(rawParams, "approval"),
		Command:   stringValue(rawParams, "command"),
		RawParams: rawParams,
	}

	outcome := agents.PermissionOutcomeDeclined
	if handler, ok := agents.PermissionHandlerFromContext(ctx); ok {
		resp, err := handler(ctx, req)
		if err == nil {
			switch resp.Outcome {
			case agents.PermissionOutcomeApproved, agents.PermissionOutcomeDeclined, agents.PermissionOutcomeCancelled:
				outcome = resp.Outcome
			}
		}
	}

	return conn.ReplyResult(msg.ID, map[string]any{
		"outcome": string(outcome),
	})
}

func (c *Client) sendSessionCancel(conn *rpcConn, sessionID string) {
	cancelCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, _ = conn.Call(cancelCtx, "session/cancel", map[string]any{
		"sessionId": sessionID,
	})
}

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

type rpcConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan rpcMessage
	nextID    int64

	requestHandlerMu sync.RWMutex
	requestHandler   func(msg rpcMessage) error

	notificationHandlerMu sync.RWMutex
	notificationHandler   func(msg rpcMessage) error

	closeOnce sync.Once
	done      chan struct{}
	doneErrMu sync.RWMutex
	doneErr   error
}

func newRPCConn(stdin io.WriteCloser, stdout io.ReadCloser) *rpcConn {
	conn := &rpcConn{
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[string]chan rpcMessage),
		done:    make(chan struct{}),
	}
	go conn.readLoop()
	return conn
}

func (c *rpcConn) Close() {
	c.closeWithError(io.EOF)
}

func (c *rpcConn) SetRequestHandler(handler func(msg rpcMessage) error) {
	c.requestHandlerMu.Lock()
	c.requestHandler = handler
	c.requestHandlerMu.Unlock()
}

func (c *rpcConn) SetNotificationHandler(handler func(msg rpcMessage) error) {
	c.notificationHandlerMu.Lock()
	c.notificationHandler = handler
	c.notificationHandlerMu.Unlock()
}

func (c *rpcConn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("acp: marshal %s params: %w", method, err)
	}

	idValue := atomic.AddInt64(&c.nextID, 1)
	idRaw := json.RawMessage(strconv.AppendInt(nil, idValue, 10))
	idKey := idToString(idRaw)
	respCh := make(chan rpcMessage, 1)

	c.pendingMu.Lock()
	c.pending[idKey] = respCh
	c.pendingMu.Unlock()
	defer c.removePending(idKey)

	if err := c.writeMessage(rpcMessage{
		JSONRPC: jsonRPCVersion,
		ID:      idRaw,
		Method:  method,
		Params:  paramsJSON,
	}); err != nil {
		return nil, err
	}

	select {
	case <-c.done:
		err := c.doneError()
		if err == nil {
			return nil, errors.New("acp: connection closed")
		}
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case response, ok := <-respCh:
		if !ok {
			err := c.doneError()
			if err == nil {
				return nil, errors.New("acp: connection closed")
			}
			return nil, err
		}
		if response.Error != nil {
			return nil, fmt.Errorf("acp: rpc %s error (%d): %s", method, response.Error.Code, response.Error.Message)
		}
		return response.Result, nil
	}
}

func (c *rpcConn) ReplyResult(id json.RawMessage, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("acp: marshal rpc result: %w", err)
	}
	return c.writeMessage(rpcMessage{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  data,
	})
}

func (c *rpcConn) ReplyError(id json.RawMessage, code int, message string) error {
	return c.writeMessage(rpcMessage{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	})
}

func (c *rpcConn) ReplyMethodNotFound(id json.RawMessage) error {
	return c.ReplyError(id, methodNotFound, "method not found")
}

func (c *rpcConn) writeMessage(msg rpcMessage) error {
	wire, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("acp: encode rpc message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(wire); err != nil {
		return fmt.Errorf("acp: write rpc message: %w", err)
	}
	if _, err := c.stdin.Write([]byte("\n")); err != nil {
		return fmt.Errorf("acp: write rpc delimiter: %w", err)
	}
	return nil
}

func (c *rpcConn) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if consumeErr := c.consumeLine(line); consumeErr != nil {
				c.closeWithError(consumeErr)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.closeWithError(io.EOF)
				return
			}
			c.closeWithError(fmt.Errorf("acp: read stdout: %w", err))
			return
		}
	}
}

func (c *rpcConn) consumeLine(line []byte) error {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return nil
	}

	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return fmt.Errorf("acp: decode rpc line: %w", err)
	}

	if msg.Method == "" && len(msg.ID) > 0 {
		return c.dispatchResponse(msg)
	}

	if msg.Method != "" && len(msg.ID) == 0 {
		c.notificationHandlerMu.RLock()
		handler := c.notificationHandler
		c.notificationHandlerMu.RUnlock()
		if handler != nil {
			return handler(msg)
		}
		return nil
	}

	if msg.Method != "" && len(msg.ID) > 0 {
		c.requestHandlerMu.RLock()
		handler := c.requestHandler
		c.requestHandlerMu.RUnlock()
		if handler == nil {
			return c.ReplyMethodNotFound(msg.ID)
		}
		return handler(msg)
	}

	return nil
}

func (c *rpcConn) dispatchResponse(msg rpcMessage) error {
	idKey := idToString(msg.ID)

	c.pendingMu.Lock()
	ch, ok := c.pending[idKey]
	if ok {
		delete(c.pending, idKey)
	}
	c.pendingMu.Unlock()

	if !ok {
		return nil
	}

	ch <- msg
	return nil
}

func (c *rpcConn) removePending(idKey string) {
	c.pendingMu.Lock()
	delete(c.pending, idKey)
	c.pendingMu.Unlock()
}

func (c *rpcConn) closeWithError(err error) {
	c.closeOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.stdout != nil {
			_ = c.stdout.Close()
		}

		c.doneErrMu.Lock()
		c.doneErr = err
		c.doneErrMu.Unlock()

		c.pendingMu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
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

func stringValue(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	v, _ := data[key]
	text, _ := v.(string)
	return strings.TrimSpace(text)
}

func idToString(id json.RawMessage) string {
	raw := strings.TrimSpace(string(id))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "\"") {
		var text string
		if err := json.Unmarshal([]byte(raw), &text); err == nil {
			return text
		}
	}
	return raw
}

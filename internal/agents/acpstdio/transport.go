package acpstdio

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	jsonRPCVersion = "2.0"

	// MethodNotFound is the JSON-RPC method-not-found error code.
	MethodNotFound = -32601
	internalError  = -32603
)

// Message is one JSON-RPC 2.0 message.
type Message struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is one JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// Conn is a newline-delimited JSON-RPC stdio connection.
type Conn struct {
	prefix string

	stdin  io.WriteCloser
	stdout io.ReadCloser

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan Message
	nextID    int64

	notifMu sync.RWMutex
	notifFn func(Message) error

	reqMu sync.RWMutex
	reqFn func(method string, params json.RawMessage) (json.RawMessage, error)

	closeOnce sync.Once
	done      chan struct{}
	doneErrMu sync.RWMutex
	doneErr   error
}

// NewConn creates a new JSON-RPC stdio connection and starts its read loop.
func NewConn(stdin io.WriteCloser, stdout io.ReadCloser, prefix string) *Conn {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "acpstdio"
	}
	conn := &Conn{
		prefix:  prefix,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[string]chan Message),
		done:    make(chan struct{}),
	}
	go conn.readLoop()
	return conn
}

// Close closes both pipes and unblocks pending calls.
func (c *Conn) Close() { c.closeWithErr(io.EOF) }

// SetNotificationHandler sets a handler for inbound notifications.
func (c *Conn) SetNotificationHandler(fn func(Message) error) {
	c.notifMu.Lock()
	c.notifFn = fn
	c.notifMu.Unlock()
}

// SetRequestHandler sets a handler for inbound requests.
func (c *Conn) SetRequestHandler(fn func(method string, params json.RawMessage) (json.RawMessage, error)) {
	c.reqMu.Lock()
	c.reqFn = fn
	c.reqMu.Unlock()
}

// Call sends a request and waits for a response.
func (c *Conn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, c.errf("marshal %s params: %w", method, err)
	}

	id := atomic.AddInt64(&c.nextID, 1)
	idRaw := json.RawMessage(strconv.AppendInt(nil, id, 10))
	idKey := string(idRaw)
	respCh := make(chan Message, 1)

	c.pendingMu.Lock()
	c.pending[idKey] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
	}()

	if err := c.write(Message{
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
		return nil, errors.New(c.prefix + ": connection closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-respCh:
		if !ok {
			return nil, errors.New(c.prefix + ": connection closed")
		}
		if resp.Error != nil {
			return nil, c.errf("rpc %s error (%d): %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// Notify sends a notification and does not wait for any response.
func (c *Conn) Notify(method string, params any) {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return
	}
	_ = c.write(Message{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		Params:  paramsJSON,
	})
}

func (c *Conn) write(msg Message) error {
	wire, err := json.Marshal(msg)
	if err != nil {
		return c.errf("marshal rpc message: %w", err)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if _, err := c.stdin.Write(wire); err != nil {
		return c.errf("write rpc: %w", err)
	}
	if _, err := c.stdin.Write([]byte("\n")); err != nil {
		return c.errf("write rpc delimiter: %w", err)
	}
	return nil
}

func (c *Conn) readLoop() {
	rd := bufio.NewReader(c.stdout)
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			if consumeErr := c.consume(line); consumeErr != nil {
				c.closeWithErr(consumeErr)
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.closeWithErr(io.EOF)
			} else {
				c.closeWithErr(c.errf("read stdout: %w", err))
			}
			return
		}
	}
}

func (c *Conn) consume(line []byte) error {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return nil
	}

	var msg Message
	if err := json.Unmarshal(line, &msg); err != nil {
		return c.errf("decode rpc line: %w", err)
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
		fn := c.notifFn
		c.notifMu.RUnlock()
		if fn != nil {
			return fn(msg)
		}
		return nil
	}

	// Inbound request: has method + id.
	if msg.Method != "" && len(msg.ID) > 0 {
		c.reqMu.RLock()
		fn := c.reqFn
		c.reqMu.RUnlock()
		if fn == nil {
			return c.write(Message{
				JSONRPC: jsonRPCVersion,
				ID:      msg.ID,
				Error: &RPCError{
					Code:    MethodNotFound,
					Message: "method not found",
				},
			})
		}

		result, err := fn(msg.Method, msg.Params)
		if err != nil {
			var rpcErr *RPCError
			if errors.As(err, &rpcErr) {
				return c.write(Message{
					JSONRPC: jsonRPCVersion,
					ID:      msg.ID,
					Error:   rpcErr,
				})
			}
			return c.write(Message{
				JSONRPC: jsonRPCVersion,
				ID:      msg.ID,
				Error: &RPCError{
					Code:    internalError,
					Message: err.Error(),
				},
			})
		}
		return c.write(Message{
			JSONRPC: jsonRPCVersion,
			ID:      msg.ID,
			Result:  result,
		})
	}

	return nil
}

func (c *Conn) closeWithErr(err error) {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		_ = c.stdout.Close()

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

func (c *Conn) doneError() error {
	c.doneErrMu.RLock()
	defer c.doneErrMu.RUnlock()
	return c.doneErr
}

func (c *Conn) errf(format string, args ...any) error {
	return fmt.Errorf(c.prefix+": "+format, args...)
}

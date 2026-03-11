package acpstdio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/beyond5959/ngent/internal/observability"
)

func TestConnCallReturnsConnectionClosedOnPeerEOF(t *testing.T) {
	conn, reqReader, respWriter := newTestConn(t)

	done := make(chan error, 1)
	go func() {
		_, err := conn.Call(context.Background(), "initialize", map[string]any{"protocolVersion": 1})
		done <- err
	}()

	_ = readMessage(t, reqReader)
	_ = respWriter.Close()

	err := waitErr(t, done)
	if err == nil {
		t.Fatalf("Call() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("Call() error = %q, want contains %q", err.Error(), "connection closed")
	}
}

func TestConnDispatchesInboundRequestToHandler(t *testing.T) {
	conn, reqReader, respWriter := newTestConn(t)

	conn.SetRequestHandler(func(method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "session/request_permission" {
			t.Fatalf("method = %q, want %q", method, "session/request_permission")
		}
		var payload map[string]any
		if err := json.Unmarshal(params, &payload); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if payload["scope"] != "shell" {
			t.Fatalf("scope = %v, want %q", payload["scope"], "shell")
		}
		return json.Marshal(map[string]any{"ok": true})
	})

	writeMessage(t, respWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      101,
		"method":  "session/request_permission",
		"params":  map[string]any{"scope": "shell"},
	})

	msg := readMessage(t, reqReader)
	if got := strings.TrimSpace(string(msg.ID)); got != "101" {
		t.Fatalf("response id = %q, want %q", got, "101")
	}
	if msg.Error != nil {
		t.Fatalf("response error = %+v, want nil", msg.Error)
	}

	var result map[string]any
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("result.ok = %v, want true", result["ok"])
	}
}

func TestConnInboundRequestWithoutHandlerReturnsMethodNotFound(t *testing.T) {
	_, reqReader, respWriter := newTestConn(t)

	writeMessage(t, respWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      202,
		"method":  "unknown/method",
		"params":  map[string]any{},
	})

	msg := readMessage(t, reqReader)
	if got := strings.TrimSpace(string(msg.ID)); got != "202" {
		t.Fatalf("response id = %q, want %q", got, "202")
	}
	if msg.Error == nil {
		t.Fatalf("response error = nil, want non-nil")
	}
	if msg.Error.Code != MethodNotFound {
		t.Fatalf("error.code = %d, want %d", msg.Error.Code, MethodNotFound)
	}
	if msg.Error.Message != "method not found" {
		t.Fatalf("error.message = %q, want %q", msg.Error.Message, "method not found")
	}
}

func TestConnInboundRequestHandlerErrorMapsToInternalError(t *testing.T) {
	conn, reqReader, respWriter := newTestConn(t)

	conn.SetRequestHandler(func(method string, params json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("boom")
	})

	writeMessage(t, respWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      303,
		"method":  "session/request_permission",
		"params":  map[string]any{},
	})

	msg := readMessage(t, reqReader)
	if msg.Error == nil {
		t.Fatalf("response error = nil, want non-nil")
	}
	if msg.Error.Code != internalError {
		t.Fatalf("error.code = %d, want %d", msg.Error.Code, internalError)
	}
	if msg.Error.Message != "boom" {
		t.Fatalf("error.message = %q, want %q", msg.Error.Message, "boom")
	}
}

func TestConnDebugLogsInboundAndOutboundMessages(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(observability.NewJSONHandler(&logBuf, slog.LevelDebug))
	observability.ConfigureACPDebug(logger, true)
	t.Cleanup(func() {
		observability.ConfigureACPDebug(nil, false)
	})

	conn, reqReader, respWriter := newTestConn(t)

	done := make(chan error, 1)
	go func() {
		_, err := conn.Call(context.Background(), "initialize", map[string]any{
			"client": map[string]any{"name": "ngent"},
		})
		done <- err
	}()

	reqMsg := readMessage(t, reqReader)
	if got := reqMsg.Method; got != "initialize" {
		t.Fatalf("request method = %q, want %q", got, "initialize")
	}

	writeMessage(t, respWriter, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result":  map[string]any{"ok": true},
	})

	if err := waitErr(t, done); err != nil {
		t.Fatalf("Call() error = %v, want nil", err)
	}

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("log lines = %d, want at least 2\nlogs:\n%s", len(lines), logBuf.String())
	}

	foundOutbound := false
	foundInbound := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry := map[string]any{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("unmarshal log: %v", err)
		}
		if entry["msg"] != "acp.message" {
			continue
		}
		if entry["component"] != "acpstdio-test" {
			t.Fatalf("component = %v, want %q", entry["component"], "acpstdio-test")
		}

		switch entry["direction"] {
		case "outbound":
			if entry["method"] == "initialize" {
				foundOutbound = true
			}
		case "inbound":
			if entry["rpcType"] == "response" {
				foundInbound = true
			}
		}
	}

	if !foundOutbound {
		t.Fatalf("missing outbound initialize log\nlogs:\n%s", logBuf.String())
	}
	if !foundInbound {
		t.Fatalf("missing inbound response log\nlogs:\n%s", logBuf.String())
	}
}

func newTestConn(t *testing.T) (*Conn, *bufio.Reader, *io.PipeWriter) {
	t.Helper()

	reqReaderPipe, reqWriterPipe := io.Pipe()
	respReaderPipe, respWriterPipe := io.Pipe()

	conn := NewConn(reqWriterPipe, respReaderPipe, "acpstdio-test")

	t.Cleanup(func() {
		conn.Close()
		_ = reqReaderPipe.Close()
		_ = reqWriterPipe.Close()
		_ = respReaderPipe.Close()
		_ = respWriterPipe.Close()
	})

	return conn, bufio.NewReader(reqReaderPipe), respWriterPipe
}

func writeMessage(t *testing.T, w io.Writer, msg any) {
	t.Helper()

	wire, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if _, err := w.Write(wire); err != nil {
		t.Fatalf("write message: %v", err)
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		t.Fatalf("write message delimiter: %v", err)
	}
}

func readMessage(t *testing.T, rd *bufio.Reader) Message {
	t.Helper()

	type readResult struct {
		msg Message
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			ch <- readResult{err: err}
			return
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			ch <- readResult{err: err}
			return
		}
		ch <- readResult{msg: msg}
	}()

	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read message: %v", got.err)
		}
		return got.msg
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for message")
		return Message{}
	}
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()

	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for error")
		return nil
	}
}

package lspclient

import (
	"bufio"
	"bytes"
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
)

var ErrClientClosed = errors.New("lspclient: client closed")

// ResponseError is one JSON-RPC error payload.
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ResponseError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("lspclient: rpc error %d: %s", e.Code, e.Message)
}

// Config controls one stdio LSP client process.
type Config struct {
	Command     string
	Args        []string
	WorkDir     string
	Env         []string
	InitTimeout time.Duration
	RootURI     string
}

// Client is one persistent stdio JSON-RPC client.
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex
	write   io.Writer
	reader  *bufio.Reader

	seq int64

	pendingMu sync.Mutex
	pending   map[string]chan responsePacket

	closed  chan struct{}
	closeMu sync.Once
}

type requestPacket struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method,omitempty"`
	Params  any    `json:"params,omitempty"`
}

type responsePacket struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func Start(ctx context.Context, cfg Config) (*Client, error) {
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		command = "gopls"
	}
	args := cfg.Args
	if len(args) == 0 {
		args = []string{"serve"}
	}
	cmd := exec.CommandContext(context.Background(), command, args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Drain stderr to avoid child process blocking.
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
	}()

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		write:   stdin,
		reader:  bufio.NewReader(stdout),
		pending: map[string]chan responsePacket{},
		closed:  make(chan struct{}),
	}
	go c.readLoop()

	initTimeout := cfg.InitTimeout
	if initTimeout <= 0 {
		initTimeout = 15 * time.Second
	}
	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()

	params := map[string]any{
		"processId": os.Getpid(),
		"capabilities": map[string]any{
			"workspace":    map[string]any{},
			"textDocument": map[string]any{},
		},
	}
	if cfg.RootURI != "" {
		params["rootUri"] = cfg.RootURI
	}
	var initResp map[string]any
	if err := c.Call(initCtx, "initialize", params, &initResp); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := c.Notify(initCtx, "initialized", map[string]any{}); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) IsClosed() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *Client) Notify(ctx context.Context, method string, params any) error {
	if c == nil {
		return ErrClientClosed
	}
	packet := requestPacket{JSONRPC: "2.0", Method: method, Params: params}
	return c.writeMessage(ctx, packet)
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if c == nil {
		return ErrClientClosed
	}
	if c.IsClosed() {
		return ErrClientClosed
	}
	id := atomic.AddInt64(&c.seq, 1)
	idKey := strconv.FormatInt(id, 10)
	ch := make(chan responsePacket, 1)

	c.pendingMu.Lock()
	c.pending[idKey] = ch
	c.pendingMu.Unlock()

	packet := requestPacket{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.writeMessage(ctx, packet); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return ErrClientClosed
	case res := <-ch:
		if res.Error != nil {
			return res.Error
		}
		if out == nil || len(res.Result) == 0 {
			return nil
		}
		return json.Unmarshal(res.Result, out)
	}
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	var closeErr error
	c.closeMu.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		_ = c.Call(shutdownCtx, "shutdown", map[string]any{}, nil)
		_ = c.Notify(shutdownCtx, "exit", map[string]any{})
		close(c.closed)
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil {
			waitDone := make(chan error, 1)
			go func() {
				waitDone <- c.cmd.Wait()
			}()
			select {
			case err := <-waitDone:
				if err != nil {
					closeErr = err
				}
			case <-time.After(1500 * time.Millisecond):
				if c.cmd.Process != nil {
					_ = c.cmd.Process.Kill()
				}
				if err := <-waitDone; err != nil {
					closeErr = err
				}
			}
		}

		c.pendingMu.Lock()
		for id, ch := range c.pending {
			delete(c.pending, id)
			select {
			case ch <- responsePacket{Error: &ResponseError{Code: -32001, Message: ErrClientClosed.Error()}}:
			default:
			}
			close(ch)
		}
		c.pendingMu.Unlock()
	})
	return closeErr
}

func (c *Client) readLoop() {
	for {
		payload, err := readMessage(c.reader)
		if err != nil {
			_ = c.Close()
			return
		}
		var packet responsePacket
		if err := json.Unmarshal(payload, &packet); err != nil {
			continue
		}
		if len(packet.ID) > 0 && packet.Method != "" {
			go c.handleServerRequest(packet)
			continue
		}
		if len(packet.ID) > 0 {
			idKey := normalizeID(packet.ID)
			c.pendingMu.Lock()
			ch, ok := c.pending[idKey]
			if ok {
				delete(c.pending, idKey)
			}
			c.pendingMu.Unlock()
			if ok {
				ch <- packet
				close(ch)
			}
		}
	}
}

func (c *Client) handleServerRequest(req responsePacket) {
	idKey := normalizeID(req.ID)
	if idKey == "" {
		return
	}
	var result any = nil
	switch req.Method {
	case "workspace/configuration":
		var params struct {
			Items []any `json:"items"`
		}
		if err := json.Unmarshal(req.Params, &params); err == nil && len(params.Items) > 0 {
			values := make([]any, len(params.Items))
			for i := range values {
				values[i] = map[string]any{}
			}
			result = values
		}
	case "window/workDoneProgress/create", "client/registerCapability", "client/unregisterCapability":
		result = map[string]any{}
	case "workspace/applyEdit":
		result = map[string]any{"applied": false}
	default:
		result = map[string]any{}
	}
	_ = c.writeRaw(responsePacket{
		JSONRPC: "2.0",
		ID:      json.RawMessage(req.ID),
		Result:  mustMarshalRaw(result),
	})
}

func (c *Client) writeMessage(ctx context.Context, packet requestPacket) error {
	if c.IsClosed() {
		return ErrClientClosed
	}
	payload, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	return c.writeFrame(ctx, payload)
}

func (c *Client) writeRaw(packet responsePacket) error {
	if c.IsClosed() {
		return ErrClientClosed
	}
	payload, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	return c.writeFrame(context.Background(), payload)
}

func (c *Client) writeFrame(ctx context.Context, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return ErrClientClosed
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	if _, err := io.WriteString(c.write, header); err != nil {
		return err
	}
	_, err := c.write.Write(payload)
	return err
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	if reader == nil {
		return nil, ErrClientClosed
	}
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(line[len("content-length:"):])
			n, convErr := strconv.Atoi(value)
			if convErr != nil {
				return nil, convErr
			}
			contentLength = n
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("lspclient: invalid content-length %d", contentLength)
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func normalizeID(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	trimmed = strings.Trim(trimmed, "\"")
	return trimmed
}

func mustMarshalRaw(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(bytes.TrimSpace(data))
}

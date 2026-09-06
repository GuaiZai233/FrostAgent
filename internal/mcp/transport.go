package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Transport defines the communication channel with an MCP server.
type Transport interface {
	RoundTrip(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error)
	Notify(ctx context.Context, notif *JSONRPCNotification) error
	Close() error
}

// ──────────────────────────────────────────────
//  Stdio Transport
// ──────────────────────────────────────────────

type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[any]chan *JSONRPCResponse
	closed  atomic.Bool
	done    chan struct{}
}

func NewStdioTransport(command string, args []string, env map[string]string, dir string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Inherit environment and overlay custom env
	cmdEnv := os.Environ()
	for k, v := range env {
		cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = cmdEnv

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("failed to open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("failed to start stdio command: %w", err)
	}

	t := &StdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		pending: make(map[any]chan *JSONRPCResponse),
		done:    make(chan struct{}),
	}

	// Discard or drain stderr in background so buffer doesn't fill up
	go func() {
		r := bufio.NewReader(stderr)
		for {
			_, err := r.ReadString('\n')
			if err != nil {
				return
			}
		}
	}()

	// Start reading stdout loop
	go t.readLoop()

	return t, nil
}

func (t *StdioTransport) readLoop() {
	reader := bufio.NewReader(t.stdout)
	defer func() {
		t.mu.Lock()
		t.closed.Store(true)
		for _, ch := range t.pending {
			close(ch)
		}
		t.pending = make(map[any]chan *JSONRPCResponse)
		t.mu.Unlock()
		close(t.done)
	}()

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Might be a notification or malformed message from server, skip
			continue
		}

		// Normalize ID for lookup (numbers can be float64 or int or string)
		idKey := normalizeID(resp.ID)
		t.mu.Lock()
		ch, exists := t.pending[idKey]
		if exists {
			delete(t.pending, idKey)
		}
		t.mu.Unlock()

		if exists && ch != nil {
			select {
			case ch <- &resp:
			default:
			}
		}
	}
}

func (t *StdioTransport) RoundTrip(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if t.closed.Load() {
		return nil, errors.New("stdio transport is closed")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal jsonrpc request: %w", err)
	}
	data = append(data, '\n')

	ch := make(chan *JSONRPCResponse, 1)
	idKey := normalizeID(req.ID)

	t.mu.Lock()
	t.pending[idKey] = ch
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pending, idKey)
		t.mu.Unlock()
	}()

	t.writeMu.Lock()
	_, err = t.stdin.Write(data)
	t.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("failed to write to stdio: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, errors.New("mcp server exited unexpectedly")
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, errors.New("transport closed while waiting for response")
		}
		return resp, nil
	}
}

func (t *StdioTransport) Notify(ctx context.Context, notif *JSONRPCNotification) error {
	if t.closed.Load() {
		return errors.New("stdio transport is closed")
	}

	data, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}
	data = append(data, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.stdin.Write(data)
	return err
}

func (t *StdioTransport) Close() error {
	if t.closed.Swap(true) {
		return nil
	}

	_ = t.stdin.Close()
	_ = t.stdout.Close()
	_ = t.stderr.Close()

	if t.cmd != nil && t.cmd.Process != nil {
		// Try to wait or kill
		done := make(chan struct{})
		go func() {
			_ = t.cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(1 * time.Second):
			_ = t.cmd.Process.Kill()
		}
	}
	return nil
}

// ──────────────────────────────────────────────
//  Streamable HTTP Transport
// ──────────────────────────────────────────────

type StreamableHTTPTransport struct {
	url     string
	headers map[string]string
	client  *http.Client
	closed  atomic.Bool
}

func NewStreamableHTTPTransport(endpointURL string, headers map[string]string) (*StreamableHTTPTransport, error) {
	if endpointURL == "" {
		return nil, errors.New("http endpoint URL cannot be empty")
	}
	return &StreamableHTTPTransport{
		url:     endpointURL,
		headers: headers,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

func (t *StreamableHTTPTransport) RoundTrip(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if t.closed.Load() {
		return nil, errors.New("http transport is closed")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1024))
		return nil, fmt.Errorf("http error status %d: %s", httpResp.StatusCode, string(respBody))
	}

	var jsonResp JSONRPCResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&jsonResp); err != nil {
		return nil, fmt.Errorf("failed to decode jsonrpc response: %w", err)
	}
	return &jsonResp, nil
}

func (t *StreamableHTTPTransport) Notify(ctx context.Context, notif *JSONRPCNotification) error {
	if t.closed.Load() {
		return errors.New("http transport is closed")
	}

	body, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http notification failed: %w", err)
	}
	_ = httpResp.Body.Close()
	return nil
}

func (t *StreamableHTTPTransport) Close() error {
	t.closed.Store(true)
	return nil
}

func normalizeID(id any) any {
	if id == nil {
		return nil
	}
	switch v := id.(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

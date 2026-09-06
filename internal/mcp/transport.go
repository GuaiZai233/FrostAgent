package mcp

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HeaderTransport injects custom HTTP headers into requests.
type HeaderTransport struct {
	Base    http.RoundTripper
	Headers map[string]string
}

func (h *HeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.Base
	if base == nil {
		base = http.DefaultTransport
	}
	reqCopy := req.Clone(req.Context())
	for k, v := range h.Headers {
		reqCopy.Header.Set(k, v)
	}
	return base.RoundTrip(reqCopy)
}

// newTransportHTTPClient constructs an HTTP client suitable for long-lived streaming connections (SSE and Streamable HTTP).
// It sets Timeout to 0 to avoid killing hanging GET streams, while configuring granular dial, TLS, and header timeouts.
func newTransportHTTPClient(headers map[string]string) *http.Client {
	baseTransport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	var rt http.RoundTripper = baseTransport
	if len(headers) > 0 {
		rt = &HeaderTransport{
			Base:    baseTransport,
			Headers: headers,
		}
	}

	return &http.Client{
		Transport: rt,
		Timeout:   0, // Unbounded stream reading
	}
}

// CreateTransport builds an official MCP Transport based on TransportConfig.
func CreateTransport(cfg TransportConfig) (officialmcp.Transport, error) {
	switch cfg.Type {
	case TransportStdio:
		if cfg.Command == "" {
			return nil, fmt.Errorf("command cannot be empty for stdio transport")
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if cfg.WorkingDir != "" {
			cmd.Dir = cfg.WorkingDir
		}
		if len(cfg.Env) > 0 {
			cmdEnv := os.Environ()
			for k, v := range cfg.Env {
				cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
			}
			cmd.Env = cmdEnv
		}
		return &officialmcp.CommandTransport{
			Command:           cmd,
			TerminateDuration: 5 * time.Second,
		}, nil

	case TransportStreamableHTTP:
		if cfg.URL == "" {
			return nil, fmt.Errorf("url cannot be empty for streamable_http transport")
		}
		return &officialmcp.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: newTransportHTTPClient(cfg.Headers),
		}, nil

	case TransportSSE:
		if cfg.URL == "" {
			return nil, fmt.Errorf("url cannot be empty for sse transport")
		}
		return &officialmcp.SSEClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: newTransportHTTPClient(cfg.Headers),
		}, nil

	default:
		return nil, fmt.Errorf("unsupported transport type: %s (allowed: stdio, streamable_http, sse)", cfg.Type)
	}
}

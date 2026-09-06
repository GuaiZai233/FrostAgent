package mcp

import (
	"fmt"
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
		httpClient := &http.Client{
			Timeout: 60 * time.Second,
		}
		if len(cfg.Headers) > 0 {
			httpClient.Transport = &HeaderTransport{
				Base:    http.DefaultTransport,
				Headers: cfg.Headers,
			}
		}
		return &officialmcp.StreamableClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
		}, nil

	case TransportSSE:
		if cfg.URL == "" {
			return nil, fmt.Errorf("url cannot be empty for sse transport")
		}
		httpClient := &http.Client{
			Timeout: 60 * time.Second,
		}
		if len(cfg.Headers) > 0 {
			httpClient.Transport = &HeaderTransport{
				Base:    http.DefaultTransport,
				Headers: cfg.Headers,
			}
		}
		return &officialmcp.SSEClientTransport{
			Endpoint:   cfg.URL,
			HTTPClient: httpClient,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported transport type: %s (allowed: stdio, streamable_http, sse)", cfg.Type)
	}
}

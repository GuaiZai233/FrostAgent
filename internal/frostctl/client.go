package frostctl

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/GuaiZai233/FrostAgent/internal/managementipc"
)

type client struct {
	verification        []string
	http                *http.Client
	base, origin, token string
}
type commandError struct {
	Code, Message, Status string
	Resource              string
	Verification          []string
	Correlation           map[string]string
}

func (e *commandError) Error() string { return e.Message }
func newClient(ctx context.Context, inv Invocation) (*client, error) {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	c := &client{base: "http://frostagent.local"}
	if name := inv.Value("remote"); name != "" {
		transport.Proxy = http.ProxyFromEnvironment
		_, err := withRemotes(ctx, func(store *remoteStore) (any, error) {
			r, ok := store.Entries[name]
			if !ok {
				return nil, errors.New("remote does not exist")
			}
			base, origin, err := canonicalURL(r.URL)
			if err != nil {
				return nil, err
			}
			token, err := store.token(ctx, r)
			if err != nil {
				return nil, err
			}
			c.base, c.origin, c.token = base, origin, token
			return nil, nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		endpoint := managementipc.Endpoint()
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return managementipc.Dial(ctx, endpoint) }
	}
	c.http = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are forbidden") }}
	return c, nil
}
func (c *client) close() { c.http.CloseIdleConnections() }
func (c *client) request(ctx context.Context, method, path string, body any, write bool) (*http.Response, error) {
	var data []byte
	var err error
	if raw, ok := body.([]byte); ok {
		data = raw
	} else if body != nil {
		data, err = json.Marshal(body)
	}
	if err != nil {
		return nil, err
	}
	endpoint := c.base + path
	if c.origin != "" {
		_, origin, err := canonicalURL(strings.SplitN(endpoint, "?", 2)[0])
		if err != nil || origin != c.origin {
			return nil, errors.New("request origin does not match saved credential")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("invalid management request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	var sent atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{WroteHeaders: func() { sent.Store(true) }}))
	response, err := c.http.Do(req)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		status := "failed"
		if write && sent.Load() {
			status = "unknown"
		}
		return nil, c.failure("transport", "management request failed; check endpoint, permissions, TLS, token, and timeout", status, path, nil)
	}
	if response.StatusCode >= 300 {
		defer response.Body.Close()
		code := "server_error"
		switch response.StatusCode {
		case 401, 403:
			code = "unauthorized"
		case 404:
			code = "not_found"
		case 400:
			code = "invalid_argument"
		case 409:
			code = "conflict"
		}
		status := "failed"
		if write && response.StatusCode >= 500 {
			status = "unknown"
		}
		metadata := readErrorMetadata(response.Body)
		if state, _ := metadata["state"].(string); state == "failed" || state == "unknown" {
			status = state
		}
		return nil, c.failure(code, http.StatusText(response.StatusCode), status, path, metadata)
	}
	return response, nil
}
func (c *client) call(ctx context.Context, method, path string, body any, write bool) (any, error) {
	response, err := c.request(ctx, method, path, body, write)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var result any
	decoder := json.NewDecoder(io.LimitReader(response.Body, 32<<20))
	decoder.UseNumber()
	if err = decoder.Decode(&result); err != nil {
		status := "failed"
		if write {
			status = "unknown"
		}
		return nil, c.failure("invalid_response", "invalid or truncated management response", status, path, nil)
	}
	if object, ok := result.(map[string]any); ok {
		status := "failed"
		if object["state"] == "unknown" || object["status"] == "unknown" {
			status = "unknown"
		}
		if message, ok := object["error"].(string); ok && message != "" {
			return result, c.failure("rejected", message, status, path, object)
		}
		if success, ok := object["success"].(bool); ok && !success {
			return result, c.failure("rejected", "server rejected the operation", status, path, object)
		}
	}
	return result, nil
}
func instancePrefix(inv Invocation) string {
	if id := inv.Value("instance"); id != "" {
		return "/instances/" + url.PathEscape(id)
	}
	return ""
}
func rpc(service, method string) string { return "/frostagent.v1." + service + "/" + method }

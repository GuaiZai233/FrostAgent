package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

type ClientConfig struct {
	BaseURL string
	Token   string
	// HTTPClient is a trusted transport override, e.g. for private CA roots.
	// Its TLS verification must not be disabled by deployment configuration.
	HTTPClient     *http.Client
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	RequestTimeout time.Duration
	MaxFailures    int
}

type Client struct {
	config ClientConfig
	http   *http.Client
}

func NewClient(config ClientConfig) (*Client, error) {
	endpoint, err := url.Parse(config.BaseURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		(endpoint.Path != "" && endpoint.Path != "/") || !bridgeToken.MatchString(config.Token) {
		return nil, ErrInvalid
	}
	config.BaseURL = endpoint.Scheme + "://" + endpoint.Host
	if config.InitialBackoff == 0 {
		config.InitialBackoff = 250 * time.Millisecond
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = 10 * time.Second
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 15 * time.Second
	}
	if config.MaxFailures == 0 {
		config.MaxFailures = 5
	}
	if config.InitialBackoff < 0 || config.MaxBackoff < config.InitialBackoff || config.MaxBackoff > time.Minute ||
		config.RequestTimeout < 0 || config.RequestTimeout > time.Minute || config.MaxFailures < 1 || config.MaxFailures > 100 {
		return nil, ErrInvalid
	}
	var client http.Client
	if config.HTTPClient != nil {
		client = *config.HTTPClient
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		// The bridge uses a private route, not an ambient outbound HTTP proxy.
		transport.Proxy = nil
		client.Transport = transport
	}
	// Never forward a VM credential or operation body to a redirect target.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{config: config, http: &client}, nil
}

// Run submits one logical operation and polls until a terminal fact is known.
// Transport failures retry the same ID/body, with capped exponential backoff.
// A terminal failure/unknown is returned without executing a replacement.
// The caller must check State and fence Identity.Generation against its task.
func (c *Client) Run(ctx context.Context, request Request) (Operation, error) {
	request.Parameters = append([]byte(nil), request.Parameters...)
	hash, err := request.fingerprint()
	if err != nil {
		return Operation{}, err
	}
	body, _ := json.Marshal(request)
	submitted := false
	uncertain := false
	failures := 0
	delay := c.config.InitialBackoff
	for {
		method := http.MethodPut
		var payload []byte = body
		if submitted {
			method, payload = http.MethodGet, nil
		}
		op, err, retry := c.exchange(ctx, method, request.OperationID, payload)
		if err == nil {
			if !op.valid() || op.Identity != request.Identity || op.RequestHash != hash {
				return Operation{}, errors.Join(ErrOutcomeUnknown, ErrProtocol)
			}
			submitted = true
			failures = 0
			if op.State.Terminal() {
				return op, nil
			}
		} else {
			if !retry {
				if submitted || uncertain || errors.Is(err, ErrProtocol) {
					return Operation{}, errors.Join(ErrOutcomeUnknown, err)
				}
				return Operation{}, err
			}
			if errors.Is(err, ErrOutcomeUnknown) {
				// A later authentication/routing error cannot disprove that an
				// earlier submission executed before its response was lost.
				uncertain = true
			}
			failures++
			if failures >= c.config.MaxFailures {
				return Operation{}, ErrOutcomeUnknown
			}
		}
		if err := waitBackoff(ctx, delay); err != nil {
			return Operation{}, errors.Join(ErrOutcomeUnknown, err)
		}
		if delay < c.config.MaxBackoff/2 {
			delay *= 2
		} else {
			delay = c.config.MaxBackoff
		}
	}
}

func (c *Client) exchange(ctx context.Context, method, id string, body []byte) (Operation, error, bool) {
	requestCtx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, c.config.BaseURL+"/v1/operations/"+id, bytes.NewReader(body))
	if err != nil {
		return Operation{}, ErrInvalid, false
	}
	req.Header.Set("Authorization", "Bearer "+c.config.Token)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// URL/transport errors may contain credentials, paths or raw payloads.
		return Operation{}, ErrOutcomeUnknown, true
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return Operation{}, ErrOutcomeUnknown, true
	}
	if len(data) > MaxResponseBytes {
		return Operation{}, ErrProtocol, false
	}
	if resp.StatusCode == http.StatusOK {
		var op Operation
		if json.Unmarshal(data, &op) != nil {
			return Operation{}, ErrProtocol, false
		}
		return op, nil, false
	}
	// Do not propagate remote bodies to the caller or ordinary model logs.
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return Operation{}, ErrInvalid, false
	case http.StatusUnauthorized, http.StatusForbidden:
		return Operation{}, ErrUnauthorized, false
	case http.StatusConflict:
		return Operation{}, ErrConflict, false
	case http.StatusNotFound:
		return Operation{}, ErrNotFound, false
	case http.StatusTooManyRequests:
		return Operation{}, ErrCapacity, true
	default:
		if resp.StatusCode >= 500 {
			return Operation{}, ErrOutcomeUnknown, true
		}
		return Operation{}, ErrProtocol, false
	}
}

func waitBackoff(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

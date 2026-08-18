package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Standard Alcyone LLM decision constants.
const (
	DecisionWelcome      = "welcome_granted"
	DecisionReserved     = "reserved"
	DecisionInsufficient = "insufficient_funds"
)

// Standard Alcyone LLM status constants.
const (
	StatusWelcome      = "welcome_granted"
	StatusReserved     = "reserved"
	StatusInsufficient = "insufficient_funds"
	StatusCommitted    = "committed"
	StatusReleased     = "released"
	StatusExpired      = "expired"
)

// Standard Alcyone LLM release reasons.
const (
	ReasonModelFailed      = "model_failed"
	ReasonModelTimeout     = "model_timeout"
	ReasonRequestCancelled = "request_cancelled"
	ReasonCommitFailed     = "commit_failed"
	ReasonToolCommitFailed = "tool_commit_failed"
	ReasonBillingFailure   = "billing_failure"
	ReasonInternalError    = "internal_error"
)

var (
	ErrInsufficientFunds   = errors.New("insufficient snowflake balance")
	ErrReservationNotFound = errors.New("LLM reservation not found")
	ErrReservationExpired  = errors.New("LLM reservation expired")
	ErrReservationTerminal = errors.New("LLM reservation in conflicting terminal state")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrAlcyoneUnavailable  = errors.New("alcyone billing service unavailable")
)

// APIError represents a structured error returned by the Alcyone HTTP API.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Alcyone API error (status %d, code %s): %s", e.StatusCode, e.Code, e.Message)
}

func (e *APIError) Is(target error) bool {
	if target == ErrInsufficientFunds && (e.StatusCode == http.StatusPaymentRequired || e.Code == "insufficient_funds") {
		return true
	}
	if target == ErrReservationNotFound && (e.StatusCode == http.StatusNotFound || e.Code == "reservation_not_found") {
		return true
	}
	if target == ErrReservationExpired && e.Code == "reservation_expired" {
		return true
	}
	if target == ErrReservationTerminal && e.Code == "reservation_terminal" {
		return true
	}
	if target == ErrIdempotencyConflict && (e.StatusCode == http.StatusConflict && e.Code == "idempotency_conflict") {
		return true
	}
	return false
}

// BalanceResult represents the balance query response from Alcyone.
type BalanceResult struct {
	Exists       bool   `json:"exists"`
	Platform     string `json:"platform"`
	ExternalID   string `json:"external_id"`
	UserUID      string `json:"user_uid,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	BalanceMinor int64  `json:"balance_minor"`
}

// LLMReserveRequest holds parameters for reserving snowflakes before an LLM call.
type LLMReserveRequest struct {
	Platform       string `json:"platform"`
	ExternalID     string `json:"external_id"`
	DisplayName    string `json:"display_name"`
	TaskID         string `json:"task_id"`
	CallID         string `json:"call_id"`
	AmountMinor    int64  `json:"amount_minor"`
	IdempotencyKey string `json:"-"`
}

// LLMReservationResult represents the reservation result from Alcyone.
type LLMReservationResult struct {
	ReservationID string `json:"reservation_id"`
	UserUID       string `json:"user_uid"`
	Decision      string `json:"decision"`
	Status        string `json:"status"`
	ReservedMinor int64  `json:"reserved_minor"`
	ActualMinor   *int64 `json:"actual_minor,omitempty"`
	BalanceMinor  int64  `json:"balance_minor"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	Created       bool   `json:"created"`
	Replayed      bool   `json:"replayed"`
}

// Client interacts with the Alcyone billing service HTTP API.
type Client struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
}

const (
	defaultHTTPTimeout = 5 * time.Second
	maxResponseBody    = 64 * 1024
)

// NewClient creates a new Alcyone billing client.
func NewClient(baseURL, serviceToken string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		serviceToken: strings.TrimSpace(serviceToken),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Balance queries the current snowflake balance for a user.
func (c *Client) Balance(ctx context.Context, platform, externalID string) (*BalanceResult, error) {
	if platform == "" {
		platform = "qq"
	}
	reqBody := map[string]string{
		"platform":    platform,
		"external_id": externalID,
	}
	var resp struct {
		Data BalanceResult `json:"data"`
	}
	if err := c.post(ctx, "/v1/balance", reqBody, "", &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ReserveLLM creates a pre-allocation reservation before calling the model.
func (c *Client) ReserveLLM(ctx context.Context, req LLMReserveRequest) (*LLMReservationResult, error) {
	if req.Platform == "" {
		req.Platform = "qq"
	}
	var resp struct {
		Data LLMReservationResult `json:"data"`
	}
	if err := c.post(ctx, "/v1/billing/llm/reservations", req, req.IdempotencyKey, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// CommitLLM settles actual token consumption after a successful model response.
func (c *Client) CommitLLM(ctx context.Context, reservationID string, actualMinor int64) (*LLMReservationResult, error) {
	reqBody := map[string]int64{
		"actual_minor": actualMinor,
	}
	path := fmt.Sprintf("/v1/billing/llm/reservations/%s/commit", url.PathEscape(reservationID))
	var resp struct {
		Data LLMReservationResult `json:"data"`
	}
	if err := c.post(ctx, path, reqBody, "", &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ReleaseLLM cancels a reservation and returns all reserved snowflakes on failure.
func (c *Client) ReleaseLLM(ctx context.Context, reservationID, reason string) (*LLMReservationResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = ReasonModelFailed
	}
	reqBody := map[string]string{
		"reason": reason,
	}
	path := fmt.Sprintf("/v1/billing/llm/reservations/%s/release", url.PathEscape(reservationID))
	var resp struct {
		Data LLMReservationResult `json:"data"`
	}
	if err := c.post(ctx, path, reqBody, "", &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (c *Client) post(ctx context.Context, path string, body any, idempotencyKey string, target any) error {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	reqURL := c.baseURL + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.serviceToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.serviceToken)
	}
	if idempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAlcyoneUnavailable, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(respBytes) > maxResponseBody {
		return errors.New("alcyone response body exceeds size limit")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(respBytes, &errResp)
		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Code:       errResp.Error.Code,
			Message:    errResp.Error.Message,
		}
		switch resp.StatusCode {
		case http.StatusPaymentRequired:
			return ErrInsufficientFunds
		case http.StatusConflict:
			switch apiErr.Code {
			case "idempotency_conflict":
				return ErrIdempotencyConflict
			case "reservation_expired":
				return ErrReservationExpired
			case "reservation_terminal":
				return ErrReservationTerminal
			}
		case http.StatusNotFound:
			if apiErr.Code == "reservation_not_found" {
				return ErrReservationNotFound
			}
		}
		if apiErr.Code == "insufficient_funds" {
			return ErrInsufficientFunds
		}
		return apiErr
	}

	if target != nil {
		if err := json.Unmarshal(respBytes, target); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

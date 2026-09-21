package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
)

// OpenAI-compatible structures for API communication.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []any         `json:"tools,omitempty"`
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Client implements the core.LLMProvider interface for OpenAI-compatible APIs.
type Client struct {
	Logger     *logs.Store
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// HTTPError preserves the upstream response body so the agent can relay the
// provider's original error text according to the current product contract.
type HTTPError struct {
	Status string
	Body   string
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Body != "" {
		return e.Body
	}
	return e.Status
}

const defaultHTTPTimeout = 120 * time.Second

const (
	staySilentFallbackToolName   = "stay_silent"
	staySilentFallbackToolCallID = "fallback_stay_silent"
)

func NewClient(baseURL, apiKey string) *Client {
	return NewClientWithTimeout(baseURL, apiKey, defaultHTTPTimeout)
}

// NewClientWithTimeout creates an isolated OpenAI-compatible client with a
// caller-specific total HTTP timeout. Long-running background jobs should use
// their own client instead of widening the foreground chat timeout.
func NewClientWithTimeout(baseURL, apiKey string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Chat sends a request to the LLM and returns the response message.
func (c *Client) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	// Convert core request to OpenAI format
	openAIReq := chatRequest{
		Model: req.Model,
	}

	// Convert core.Tool to OpenAI function-call format
	for _, t := range req.Tools {
		openAIReq.Tools = append(openAIReq.Tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
			},
		})
	}

	for _, msg := range req.Messages {
		cm := chatMessage{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}
		for _, tc := range msg.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls, toolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		openAIReq.Messages = append(openAIReq.Messages, cm)
	}

	jsonData, err := json.Marshal(openAIReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	logReq := compactHistoricalToolResultsForLogging(openAIReq)
	if logData, err := json.Marshal(logReq); err == nil {
		c.log().LLMRequest(string(logData))
	} else {
		c.log().LLMRequest(string(jsonData))
	}

	fullURL, err := url.JoinPath(c.BaseURL, "chat/completions")
	if err != nil {
		return nil, fmt.Errorf("failed to join url path: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.log().Error(logs.HTTP, fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(body)))
		return nil, &HTTPError{Status: resp.Status, Body: string(body)}
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var openAIResp chatResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		c.log().LLMResponse(fmt.Sprintf("[malformed response body: len=%d]", len(respBody)))
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.log().LLMResponse(string(respBody))

	if openAIResp.Error != nil {
		return nil, fmt.Errorf("API returned error: %s", openAIResp.Error.Message)
	}

	var usage *core.Usage
	if openAIResp.Usage != nil {
		usage = &core.Usage{
			PromptTokens:     openAIResp.Usage.PromptTokens,
			CompletionTokens: openAIResp.Usage.CompletionTokens,
			TotalTokens:      openAIResp.Usage.TotalTokens,
		}
	}

	if len(openAIResp.Choices) == 0 {
		for _, tool := range req.Tools {
			if tool.Name != staySilentFallbackToolName {
				continue
			}
			c.log().Warn(logs.LLM_RESPONSE, "LLM response contained no choices; falling back to stay_silent")
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role: core.RoleAssistant,
					ToolCalls: []core.ToolCall{{
						ID:   staySilentFallbackToolCallID,
						Type: "function",
						Function: core.ToolCallFunction{
							Name:      tool.Name,
							Arguments: "{}",
						},
					}},
				},
				Usage: usage,
			}, nil
		}
		return nil, fmt.Errorf("no choices in response")
	}

	// Map back to core response
	choice := openAIResp.Choices[0].Message
	coreMsg := core.ChatMessage{
		Role:    core.MessageRole(choice.Role),
		Content: choice.Content,
	}
	for _, tc := range choice.ToolCalls {
		coreMsg.ToolCalls = append(coreMsg.ToolCalls, core.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: core.ToolCallFunction{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return &core.ChatResponse{
		Message: coreMsg,
		Usage:   usage,
	}, nil
}

func (c *Client) log() *logs.Store {
	if c.Logger != nil {
		return c.Logger
	}
	return logs.General
}

const maxHistoricalToolOutputBytes = 256

// compactHistoricalToolResultsForLogging creates a shallow-copied chatRequest for logging where
// historical tool results (tools called in previous turns that are followed by subsequent
// assistant messages) have large outputs (stdout/stderr) folded into a reference placeholder.
// This prevents quadratic memory amplification across turns in logs.Store, while keeping
// current-turn tool executions and tool call commands fully unredacted.
func compactHistoricalToolResultsForLogging(req chatRequest) chatRequest {
	lastAssistantIdx := -1
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "assistant" {
			lastAssistantIdx = i
			break
		}
	}
	if lastAssistantIdx <= 0 {
		return req
	}

	var needsCopy bool
	for i := 0; i < lastAssistantIdx; i++ {
		if req.Messages[i].Role == "tool" {
			needsCopy = true
			break
		}
	}
	if !needsCopy {
		return req
	}

	copied := req
	copied.Messages = make([]chatMessage, len(req.Messages))
	copy(copied.Messages, req.Messages)

	for i := 0; i < lastAssistantIdx; i++ {
		if copied.Messages[i].Role != "tool" {
			continue
		}
		msg := copied.Messages[i]
		str, ok := msg.Content.(string)
		if !ok {
			continue
		}

		var r struct {
			ExitCode                  *int   `json:"exit_code"`
			TimedOut                  bool   `json:"timed_out"`
			Stdout                    string `json:"stdout"`
			Stderr                    string `json:"stderr"`
			StdoutTruncated           bool   `json:"stdout_truncated"`
			StderrTruncated           bool   `json:"stderr_truncated"`
			FrostAgentStdoutTruncated bool   `json:"frostagent_stdout_truncated,omitempty"`
			FrostAgentStderrTruncated bool   `json:"frostagent_stderr_truncated,omitempty"`
			DurationMs                int64  `json:"duration_ms"`
		}
		if err := json.Unmarshal([]byte(str), &r); err == nil && (r.Stdout != "" || r.Stderr != "" || r.ExitCode != nil) {
			changed := false
			if len(r.Stdout) > maxHistoricalToolOutputBytes {
				r.Stdout = fmt.Sprintf("[historical stdout omitted: len=%d, see TOOL log]", len(r.Stdout))
				changed = true
			}
			if len(r.Stderr) > maxHistoricalToolOutputBytes {
				r.Stderr = fmt.Sprintf("[historical stderr omitted: len=%d, see TOOL log]", len(r.Stderr))
				changed = true
			}
			if changed {
				if b, err := json.Marshal(r); err == nil {
					msg.Content = string(b)
					copied.Messages[i] = msg
				}
			}
		} else if len(str) > maxHistoricalToolOutputBytes {
			msg.Content = fmt.Sprintf("[historical output omitted: len=%d, see TOOL log]", len(str))
			copied.Messages[i] = msg
		}
	}

	return copied
}

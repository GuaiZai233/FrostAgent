package openai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	logSafeReq := redactChatRequestForLogging(openAIReq)
	if logSafeData, err := json.Marshal(logSafeReq); err == nil {
		c.log().LLMRequest(string(logSafeData))
	} else {
		c.log().LLMRequest("[failed to marshal log-safe request]")
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

	logSafeResp := redactChatResponseForLogging(openAIResp)
	if logSafeBytes, err := json.Marshal(logSafeResp); err == nil {
		c.log().LLMResponse(string(logSafeBytes))
	} else {
		c.log().LLMResponse("[failed to marshal log-safe response]")
	}

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

// redactExecuteCommandArgs replaces raw shell command strings with metadata
// (length, sha256 prefix, cwd, timeout) for safe logging.
func redactExecuteCommandArgs(rawArgs string) string {
	var p struct {
		Command string   `json:"command"`
		Cwd     string   `json:"cwd"`
		Timeout *float64 `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &p); err == nil {
		cmdHash := sha256.Sum256([]byte(p.Command))
		hashPrefix := hex.EncodeToString(cmdHash[:8])
		cwd := p.Cwd
		if cwd == "" {
			cwd = "/sandbox"
		}
		timeoutStr := "default"
		if p.Timeout != nil {
			timeoutStr = fmt.Sprintf("%.1fs", *p.Timeout)
		}
		p.Command = fmt.Sprintf("[REDACTED command: len=%d, sha256_prefix=%s, cwd=%s, timeout=%s]",
			len(p.Command), hashPrefix, cwd, timeoutStr)
		if b, err := json.Marshal(p); err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf(`{"command":"[REDACTED command: raw_len=%d]"}`, len(rawArgs))
}

// redactExecuteCommandResult replaces stdout and stderr strings in execute_command
// tool outputs with length metadata for safe logging.
func redactExecuteCommandResult(content any) any {
	str, ok := content.(string)
	if !ok {
		return "[REDACTED execute_command output]"
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
	if err := json.Unmarshal([]byte(str), &r); err == nil {
		r.Stdout = fmt.Sprintf("[REDACTED stdout: len=%d]", len(r.Stdout))
		r.Stderr = fmt.Sprintf("[REDACTED stderr: len=%d]", len(r.Stderr))
		if b, err := json.Marshal(r); err == nil {
			return string(b)
		}
	}
	return fmt.Sprintf("[REDACTED execute_command output: raw_len=%d]", len(str))
}

// redactChatRequestForLogging returns a deep log-safe copy of chatRequest where
// execute_command invocations and results have their command strings and stdout/stderr redacted.
func redactChatRequestForLogging(req chatRequest) chatRequest {
	execToolCallIDs := make(map[string]bool)
	for _, msg := range req.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name == "execute_command" {
				if tc.ID != "" {
					execToolCallIDs[tc.ID] = true
				}
			}
		}
	}

	redacted := req
	redacted.Messages = make([]chatMessage, len(req.Messages))
	for i, msg := range req.Messages {
		msgCopy := msg
		if len(msg.ToolCalls) > 0 {
			msgCopy.ToolCalls = make([]toolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				tcCopy := tc
				if tc.Function.Name == "execute_command" {
					tcCopy.Function.Arguments = redactExecuteCommandArgs(tc.Function.Arguments)
				}
				msgCopy.ToolCalls[j] = tcCopy
			}
		}

		if msg.Role == "tool" {
			isExec := execToolCallIDs[msg.ToolCallID]
			if !isExec {
				if s, ok := msg.Content.(string); ok {
					var probe struct {
						Stdout   *string `json:"stdout"`
						Stderr   *string `json:"stderr"`
						ExitCode *int    `json:"exit_code"`
					}
					if err := json.Unmarshal([]byte(s), &probe); err == nil {
						if probe.Stdout != nil || probe.Stderr != nil || probe.ExitCode != nil {
							isExec = true
						}
					}
				}
			}
			if isExec {
				msgCopy.Content = redactExecuteCommandResult(msg.Content)
			}
		}
		redacted.Messages[i] = msgCopy
	}
	return redacted
}

// redactChatResponseForLogging returns a log-safe copy of chatResponse where
// execute_command tool call arguments are redacted.
func redactChatResponseForLogging(resp chatResponse) chatResponse {
	redacted := resp
	if len(resp.Choices) > 0 {
		redacted.Choices = make([]struct {
			Message chatMessage `json:"message"`
		}, len(resp.Choices))
		for i, ch := range resp.Choices {
			chCopy := ch
			if len(ch.Message.ToolCalls) > 0 {
				chCopy.Message.ToolCalls = make([]toolCall, len(ch.Message.ToolCalls))
				for j, tc := range ch.Message.ToolCalls {
					tcCopy := tc
					if tc.Function.Name == "execute_command" {
						tcCopy.Function.Arguments = redactExecuteCommandArgs(tc.Function.Arguments)
					}
					chCopy.Message.ToolCalls[j] = tcCopy
				}
			}
			redacted.Choices[i] = chCopy
		}
	}
	return redacted
}

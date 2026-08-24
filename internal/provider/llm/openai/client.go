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
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
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

	logs.LLMRequest(string(jsonData))

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
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logs.Error(logs.HTTP, fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(body)))
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	logs.LLMResponse(string(respBody))

	var openAIResp chatResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
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
			logs.Warn(logs.LLM_RESPONSE, "LLM response contained no choices; falling back to stay_silent")
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

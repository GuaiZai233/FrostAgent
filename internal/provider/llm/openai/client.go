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
)

// OpenAI-compatible structures for API communication.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []any         `json:"tools,omitempty"`
}

type chatMessage struct {
	Role       string      `json:"role"`
	Content    any         `json:"content"`
	ToolCalls  []toolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
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

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Chat sends a request to the LLM and returns the response message.
func (c *Client) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	// Convert core request to OpenAI format
	openAIReq := chatRequest{
		Model: req.Model,
	}

	for _, msg := range req.Messages {
		openAIReq.Messages = append(openAIReq.Messages, chatMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	// Note: Tool conversion logic would go here if needed
	// For now, we keep it simple to match the current step goal

	jsonData, err := json.Marshal(openAIReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
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
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var openAIResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if openAIResp.Error != nil {
		return nil, fmt.Errorf("API returned error: %s", openAIResp.Error.Message)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	// Map back to core response
	choice := openAIResp.Choices[0].Message
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.MessageRole(choice.Role),
			Content: choice.Content,
		},
	}, nil
}

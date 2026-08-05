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

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
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

	if len(openAIResp.Choices) == 0 {
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
	}, nil
}

// Embed calls the OpenAI-compatible /embeddings endpoint and returns
// one vector (float32) per input text.
func (c *Client) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := embeddingRequest{Model: model, Input: texts}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	fullURL, err := url.JoinPath(c.BaseURL, "embeddings")
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
		return nil, fmt.Errorf("embedding API error (status %d): %s", resp.StatusCode, string(body))
	}

	var embResp embeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}
	if embResp.Error != nil {
		return nil, fmt.Errorf("embedding API error: %s", embResp.Error.Message)
	}

	vectors := make([][]float32, len(embResp.Data))
	for i, d := range embResp.Data {
		vectors[i] = d.Embedding
	}
	return vectors, nil
}

// Embedder adapts an OpenAI-compatible Client to the memory.Embedder interface.
type Embedder struct {
	Client *Client
	Model  string
}

// NewEmbedder creates an Embedder that uses the given OpenAI-compatible client
// with the specified embedding model.
func NewEmbedder(client *Client, model string) *Embedder {
	return &Embedder{Client: client, Model: model}
}

// Embed satisfies memory.Embedder.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.Client.Embed(ctx, texts, e.Model)
}

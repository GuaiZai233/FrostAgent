package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const DefaultHTTPTimeout = 120 * time.Second

// ChatMessage represents a message in the chat conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool call made by the model.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ChatRequest represents the request body for the chat completions API.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []any         `json:"tools,omitempty"`
}

// ChatResponse represents the response body from the chat completions API.
type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type,omitempty"`
		Code    any    `json:"code,omitempty"`
	} `json:"error,omitempty"`
}

// Client is the LLM client.
type Client struct {
	HTTPClient *http.Client
}

// NewClient creates a new LLM client with a default timeout.
func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: DefaultHTTPTimeout},
	}
}

// buildChatCompletionsURL normalizes the baseURL to an OpenAI-compatible chat completions address.
func buildChatCompletionsURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions"
	}
	return baseURL + "/v1/chat/completions"
}

// CallAPI sends a request to the LLM API.
func (c *Client) CallAPI(baseURL, apiKey, model string, messages []ChatMessage, tools []any) (*ChatMessage, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages 不能为空")
	}

	reqBody := ChatRequest{
		Model:    model,
		Messages: messages,
		Tools:    tools,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("JSON 编码失败: %w", err)
	}

	url := buildChatCompletionsURL(baseURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %v", err)
	}
	defer resp.Body.Close()

	respBodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("【API 错误响应】%s", string(respBodyBytes))
		return nil, fmt.Errorf("API 请求失败，状态码: %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBodyBytes, &chatResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API 返回错误: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("API 响应中没有 choices")
	}

	return &chatResp.Choices[0].Message, nil
}

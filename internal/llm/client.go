package llm

import (
	"FrostAgent/internal/logs"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// openai 兼容协议结构体

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []any         `json:"tools,omitempty"`
}

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

//客户端核心实现

const DefaultHTTPTimeout = 120 * time.Second

type Client struct {
	HTTPClient *http.Client
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: DefaultHTTPTimeout},
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	if c.HTTPClient.Timeout == 0 {
		clone := *c.HTTPClient
		clone.Timeout = DefaultHTTPTimeout
		c.HTTPClient = &clone
	}
	return c.HTTPClient
}

// buildChatCompletionsURL 不再拼接，直接返回用户提供的完整 URL。
func buildChatCompletionsURL(baseURL string) string {
	return baseURL + "/chat/completions"
}

//callapi 发送请求

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

	//组装http请求
	url := buildChatCompletionsURL(baseURL) // Use the provided baseURL directly
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// 打印请求摘要，避免在日志中泄露完整上下文和密钥
	logs.Info(logs.HTTP, fmt.Sprintf("【发送请求】POST %s，模型: %s，消息数: %d，工具数: %d", url, model, len(messages), len(tools)))
	logs.LLMRequest(string(jsonData))

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logs.Error(logs.HTTP, fmt.Sprintf("关闭响应体失败: %v", err))
		}
	}()

	// 打印响应状态码
	logs.LLMResponse(fmt.Sprintf("【响应状态码】%d", resp.StatusCode))

	// 读取完整的响应体
	respBodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logs.Error(logs.HTTP, fmt.Sprintf("【API 错误响应】%s", string(respBodyBytes)))
		return nil, fmt.Errorf("API 请求失败，状态码: %d，响应内容: %s", resp.StatusCode, string(respBodyBytes))
	}

	// 解析响应
	var chatResp ChatResponse
	if err := json.Unmarshal(respBodyBytes, &chatResp); err != nil {
		logs.Error(logs.HTTP, fmt.Sprintf("【响应解析失败，原始响应】%s", string(respBodyBytes)))
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("API 返回错误: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("API 响应中没有 choices")
	}

	//返回大模型生成的单条消息
	return &chatResp.Choices[0].Message, nil
}

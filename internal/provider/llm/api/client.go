package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/openai"
)

// Client keeps the existing OpenAI transport for compatible protocols.
type Client struct {
	*openai.Client
	Format Format
}

func NewClient(baseURL, key string, format Format, timeout time.Duration) *Client {
	return &Client{Client: openai.NewClientWithTimeout(baseURL, key, timeout), Format: format}
}

func (c *Client) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	endpoint, err := c.Format.URL(c.BaseURL, "chat/completions", req.Model)
	if err != nil {
		return nil, err
	}
	if c.Format == Legacy || c.Format == ChatCompletions || c.Format == Custom {
		client := *c.Client
		client.RequestURL = endpoint
		return client.Chat(ctx, req)
	}
	var payload any
	switch c.Format {
	case Responses:
		payload, err = responsesRequest(req)
	case Anthropic:
		payload, err = anthropicRequest(req)
	case Gemini:
		payload, err = geminiRequest(req)
	}
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode API request: %w", err)
	}
	c.log().LLMRequest(string(body), req.TraceID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	c.Format.Authorize(request, c.APIKey)
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		c.log().Error(logs.HTTP, fmt.Sprintf("API error (status %d): %s", response.StatusCode, raw), req.TraceID)
		return nil, &openai.HTTPError{Status: response.Status, StatusCode: response.StatusCode, Body: string(raw)}
	}
	c.log().LLMResponse(string(raw), req.TraceID)
	var result *core.ChatResponse
	switch c.Format {
	case Responses:
		result, err = decodeResponses(raw, req.Model)
	case Anthropic:
		result, err = decodeAnthropic(raw, req.Model)
	case Gemini:
		result, err = decodeGemini(raw, req.Model)
	}
	if result != nil && c.ObserveUsage != nil {
		c.ObserveUsage(result.Usage)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Embed(ctx context.Context, req core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	endpoint, err := c.Format.URL(c.BaseURL, "embeddings", req.Model)
	if err != nil {
		return nil, err
	}
	client := *c.Client
	client.EmbeddingURL = endpoint
	return client.Embed(ctx, req)
}

func (c *Client) log() *logs.Store {
	if c.Logger != nil {
		return c.Logger
	}
	return logs.General
}

func contentParts(content any) ([]core.ContentPart, error) {
	if content == nil {
		return nil, nil
	}
	if text, ok := content.(string); ok {
		if text == "" {
			return nil, nil
		}
		return []core.ContentPart{{Type: "text", Text: text}}, nil
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	var parts []core.ContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("unsupported message content: %w", err)
	}
	for _, part := range parts {
		if part.Type != "text" && (part.Type != "image_url" || part.ImageURL == nil) {
			return nil, fmt.Errorf("unsupported content part %q", part.Type)
		}
	}
	return parts, nil
}

func arguments(raw string) (map[string]any, error) {
	if raw == "" {
		raw = "{}"
	}
	var value map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, fmt.Errorf("invalid tool arguments")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("invalid trailing tool arguments")
	}
	return value, nil
}

func call(id, name string, args any) (core.ToolCall, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return core.ToolCall{}, err
	}
	if _, err = arguments(string(raw)); err != nil {
		return core.ToolCall{}, err
	}
	if id == "" || name == "" {
		return core.ToolCall{}, fmt.Errorf("tool call is missing id or name")
	}
	return core.ToolCall{ID: id, Type: "function", Function: core.ToolCallFunction{Name: name, Arguments: string(raw)}}, nil
}

func tokenUsage(input, output, total, cached *int) *core.Usage {
	if input == nil && output == nil && total == nil {
		return nil
	}
	usage := &core.Usage{Reported: &core.UsageReported{}}
	if input != nil && *input >= 0 {
		usage.PromptTokens = *input
		usage.Reported.Prompt = true
	}
	if output != nil && *output >= 0 {
		usage.CompletionTokens = *output
		usage.Reported.Completion = true
	}
	if total != nil && *total >= 0 {
		usage.TotalTokens = *total
		usage.Reported.Total = true
	} else if usage.Reported.Prompt && usage.Reported.Completion {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		usage.Reported.Total = true
	}
	if cached != nil && *cached >= 0 {
		usage.CachedPromptTokens = cached
	}
	return usage
}

// decodeJSON preserves integer-valued tool arguments during opaque replay.
func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("invalid trailing JSON")
	}
	return nil
}

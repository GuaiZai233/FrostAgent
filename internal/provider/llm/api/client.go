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
	Format     Format
	EndpointID string
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
		client.EndpointID = c.EndpointID
		return client.Chat(ctx, req)
	}
	// Bind opaque state to the same configured endpoint and effective URL.
	// URL resolution also makes versioned and unversioned bases equivalent.
	origin := stateOrigin{EndpointID: c.EndpointID, URL: endpoint}
	payload, err := c.payload(req, origin)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode API request: %w", err)
	}
	logBody := string(body)
	if core.HasInlineMedia(req.Messages) {
		// Audio and video are request-local: log placeholders, never the bytes.
		redacted := req
		redacted.Messages = core.WithoutInlineMedia(req.Messages)
		logBody = "[inline media omitted]"
		if logPayload, logErr := c.payload(redacted, origin); logErr == nil {
			if encoded, encodeErr := json.Marshal(logPayload); encodeErr == nil {
				logBody = string(encoded)
			}
		}
	}
	c.log().LLMRequest(logBody, req.TraceID)
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
		result, err = decodeResponses(raw, req.Model, origin)
	case Anthropic:
		result, err = decodeAnthropic(raw, req.Model, origin)
	case Gemini:
		result, err = decodeGemini(raw, req.Model, origin)
	}
	if result != nil && c.ObserveUsage != nil {
		c.ObserveUsage(result.Usage)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) payload(req core.ChatRequest, origin stateOrigin) (any, error) {
	switch c.Format {
	case Responses:
		return responsesRequest(req, origin)
	case Anthropic:
		return anthropicRequest(req, origin)
	case Gemini:
		return geminiRequest(req, origin)
	}
	return nil, fmt.Errorf("unsupported API format %q", c.Format)
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
		switch {
		case part.Type == "text":
		case part.Type == string(core.ContentPartTypeImage) && part.ImageURL != nil:
		case part.Type == string(core.ContentPartTypeAudio) && part.InputAudio != nil:
		case part.Type == string(core.ContentPartTypeVideo) && part.VideoURL != nil:
		default:
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

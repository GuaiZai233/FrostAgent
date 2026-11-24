package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func anthropicContent(content any) ([]any, error) {
	parts, err := contentParts(content)
	if err != nil {
		return nil, err
	}
	blocks := make([]any, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" {
			blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
			continue
		}
		source := map[string]any{"type": "url", "url": part.ImageURL.URL}
		if strings.HasPrefix(part.ImageURL.URL, "data:") {
			mime, data, err := dataImage(part.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			source = map[string]any{"type": "base64", "media_type": mime, "data": data}
		}
		blocks = append(blocks, map[string]any{"type": "image", "source": source})
	}
	return blocks, nil
}

func anthropicRequest(req core.ChatRequest, origin stateOrigin) (any, error) {
	messages := make([]map[string]any, 0)
	system := make([]any, 0)
	for _, msg := range req.Messages {
		content, err := anthropicContent(msg.Content)
		if err != nil {
			return nil, err
		}
		if msg.Role == core.RoleSystem {
			system = append(system, content...)
			continue
		}
		role := string(msg.Role)
		if msg.Role == core.RoleAssistant && stateMatches(msg, Anthropic, req.Model, origin) {
			if err := decodeJSON(msg.ProviderState.Content, &content); err != nil {
				return nil, err
			}
		} else if msg.Role == core.RoleTool {
			role = "user"
			content = []any{map[string]any{"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": content}}
		} else {
			for _, tc := range msg.ToolCalls {
				args, err := arguments(tc.Function.Arguments)
				if err != nil {
					return nil, err
				}
				content = append(content, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": args})
			}
		}
		if len(content) == 0 {
			continue
		}
		if len(messages) > 0 && messages[len(messages)-1]["role"] == role {
			previous := messages[len(messages)-1]
			previous["content"] = append(previous["content"].([]any), content...)
		} else {
			messages = append(messages, map[string]any{"role": role, "content": content})
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	payload := map[string]any{"model": req.Model, "messages": messages, "max_tokens": maxTokens}
	if len(system) > 0 {
		payload["system"] = system
	}
	tools := make([]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.Parameters})
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	if req.ToolChoice != "" {
		if req.ToolChoice == "auto" || req.ToolChoice == "none" {
			payload["tool_choice"] = map[string]any{"type": req.ToolChoice}
		} else {
			payload["tool_choice"] = map[string]any{"type": "tool", "name": req.ToolChoice}
		}
	}
	return payload, nil
}

func decodeAnthropic(raw []byte, model string, origin stateOrigin) (*core.ChatResponse, error) {
	var payload struct {
		Type    string            `json:"type"`
		Content []json.RawMessage `json:"content"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage *struct {
			Input   *int `json:"input_tokens"`
			Output  *int `json:"output_tokens"`
			Read    *int `json:"cache_read_input_tokens"`
			Created *int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Anthropic response: %w", err)
	}
	result := &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant}}
	if payload.Usage != nil {
		input := payload.Usage.Input
		if input != nil {
			count := *input
			if payload.Usage.Read != nil {
				count += *payload.Usage.Read
			}
			if payload.Usage.Created != nil {
				count += *payload.Usage.Created
			}
			input = &count
		}
		result.Usage = tokenUsage(input, payload.Usage.Output, nil, payload.Usage.Read)
	}
	if payload.Error != nil {
		return result, fmt.Errorf("API returned error: %s", payload.Error.Message)
	}
	var text strings.Builder
	for _, block := range payload.Content {
		var item struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(block)))
		decoder.UseNumber()
		if err := decoder.Decode(&item); err != nil {
			return result, err
		}
		if item.Type == "text" {
			text.WriteString(item.Text)
		}
		if item.Type == "tool_use" {
			tc, err := call(item.ID, item.Name, item.Input)
			if err != nil {
				return result, err
			}
			result.Message.ToolCalls = append(result.Message.ToolCalls, tc)
		}
	}
	result.Message.Content = text.String()
	if text.Len() == 0 && len(result.Message.ToolCalls) == 0 {
		return result, fmt.Errorf("no message or tool calls in Anthropic response")
	}
	rememberState(&result.Message, Anthropic, model, payload.Content, origin)
	return result, nil
}

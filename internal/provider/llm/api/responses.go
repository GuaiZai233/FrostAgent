package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func responsesContent(content any) ([]any, error) {
	parts, err := contentParts(content)
	if err != nil {
		return nil, err
	}
	result := make([]any, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" {
			result = append(result, map[string]any{"type": "input_text", "text": part.Text})
		} else {
			result = append(result, map[string]any{"type": "input_image", "image_url": part.ImageURL.URL})
		}
	}
	return result, nil
}

func responsesMessageContent(msg core.ChatMessage) (any, error) {
	if msg.Role != core.RoleAssistant {
		return responsesContent(msg.Content)
	}
	parts, err := contentParts(msg.Content)
	if err != nil {
		return nil, err
	}
	var text strings.Builder
	for _, part := range parts {
		if part.Type != "text" {
			return nil, fmt.Errorf("unsupported Responses assistant content part %q", part.Type)
		}
		text.WriteString(part.Text)
	}
	// Assistant history uses string content in the easy-input message form.
	return text.String(), nil
}

func responsesRequest(req core.ChatRequest, origin stateOrigin) (any, error) {
	input := make([]any, 0)
	for _, msg := range req.Messages {
		if msg.Role == core.RoleAssistant && stateMatches(msg, Responses, req.Model, origin) {
			var items []any
			if err := decodeJSON(msg.ProviderState.Content, &items); err != nil {
				return nil, err
			}
			input = append(input, items...)
			continue
		}
		if msg.Role == core.RoleTool {
			output := msg.Content
			if _, ok := output.(string); !ok {
				var err error
				output, err = responsesContent(output)
				if err != nil {
					return nil, err
				}
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": msg.ToolCallID, "output": output})
			continue
		}
		if msg.Content != nil && msg.Content != "" {
			content, err := responsesMessageContent(msg)
			if err != nil {
				return nil, err
			}
			input = append(input, map[string]any{"role": msg.Role, "content": content})
		}
		for _, tc := range msg.ToolCalls {
			if _, err := arguments(tc.Function.Arguments); err != nil {
				return nil, err
			}
			input = append(input, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Function.Name, "arguments": tc.Function.Arguments})
		}
	}
	payload := map[string]any{"model": req.Model, "input": input, "store": false, "include": []string{"reasoning.encrypted_content"}}
	if req.MaxTokens > 0 {
		payload["max_output_tokens"] = req.MaxTokens
	}
	tools := make([]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Parameters, "strict": false})
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	if req.ToolChoice != "" {
		if req.ToolChoice == "auto" || req.ToolChoice == "none" {
			payload["tool_choice"] = req.ToolChoice
		} else {
			payload["tool_choice"] = map[string]any{"type": "function", "name": req.ToolChoice}
		}
	}
	return payload, nil
}

func decodeResponses(raw []byte, model string, origin stateOrigin) (*core.ChatResponse, error) {
	var payload struct {
		Output []json.RawMessage `json:"output"`
		Status string            `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage *struct {
			Input   *int `json:"input_tokens"`
			Output  *int `json:"output_tokens"`
			Total   *int `json:"total_tokens"`
			Details *struct {
				Cached *int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Responses response: %w", err)
	}
	result := &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant}}
	if payload.Usage != nil {
		var cached *int
		if payload.Usage.Details != nil {
			cached = payload.Usage.Details.Cached
		}
		result.Usage = tokenUsage(payload.Usage.Input, payload.Usage.Output, payload.Usage.Total, cached)
	}
	if payload.Error != nil {
		return result, fmt.Errorf("API returned error: %s", payload.Error.Message)
	}
	if payload.Status != "" && payload.Status != "completed" {
		return result, fmt.Errorf("Responses response status: %s", payload.Status)
	}
	var text strings.Builder
	for _, item := range payload.Output {
		var output struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		if err := json.Unmarshal(item, &output); err != nil {
			return result, err
		}
		switch output.Type {
		case "message":
			for _, part := range output.Content {
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				}
				if part.Type == "refusal" {
					text.WriteString(part.Refusal)
				}
			}
		case "function_call":
			args, err := arguments(output.Arguments)
			if err != nil {
				return result, err
			}
			tc, err := call(output.CallID, output.Name, args)
			if err != nil {
				return result, err
			}
			result.Message.ToolCalls = append(result.Message.ToolCalls, tc)
		}
	}
	result.Message.Content = text.String()
	if text.Len() == 0 && len(result.Message.ToolCalls) == 0 {
		return result, fmt.Errorf("no message or tool calls in Responses response")
	}
	rememberState(&result.Message, Responses, model, payload.Output, origin)
	return result, nil
}

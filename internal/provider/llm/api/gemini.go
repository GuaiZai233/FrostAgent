package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func dataImage(value string) (string, string, error) {
	header, data, ok := strings.Cut(value, ",")
	if !ok || !strings.HasPrefix(header, "data:image/") || !strings.HasSuffix(header, ";base64") {
		return "", "", fmt.Errorf("image must be a base64 data URL")
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return "", "", fmt.Errorf("invalid base64 image")
	}
	return strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"), data, nil
}

func geminiContent(content any) ([]any, error) {
	parts, err := contentParts(content)
	if err != nil {
		return nil, err
	}
	result := make([]any, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" {
			result = append(result, map[string]any{"text": part.Text})
			continue
		}
		value := part.ImageURL.URL
		if !strings.HasPrefix(value, "data:") {
			return nil, fmt.Errorf("Gemini image URLs are unsupported; provide a base64 data URL")
		}
		media, data, err := dataImage(value)
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"inlineData": map[string]any{"mimeType": media, "data": data}})
	}
	return result, nil
}

func geminiRequest(req core.ChatRequest) (any, error) {
	contents := make([]map[string]any, 0)
	system := make([]any, 0)
	names := make(map[string]string)
	nativeIDs := make(map[string]string)
	for _, msg := range req.Messages {
		parts, err := geminiContent(msg.Content)
		if err != nil {
			return nil, err
		}
		if msg.Role == core.RoleSystem {
			system = append(system, parts...)
			continue
		}
		role := "user"
		if msg.Role == core.RoleAssistant {
			role = "model"
		}
		for _, tc := range msg.ToolCalls {
			names[tc.ID] = tc.Function.Name
			nativeIDs[tc.ID] = tc.ID
		}
		if msg.Role == core.RoleAssistant && stateMatches(msg, Gemini, req.Model) {
			if err := decodeJSON(msg.ProviderState.Content, &parts); err != nil {
				return nil, err
			}
			callIndex := 0
			for _, part := range parts {
				object, ok := part.(map[string]any)
				if !ok {
					continue
				}
				fc, ok := object["functionCall"].(map[string]any)
				if !ok {
					continue
				}
				if callIndex < len(msg.ToolCalls) {
					nativeIDs[msg.ToolCalls[callIndex].ID], _ = fc["id"].(string)
				}
				callIndex++
			}
		} else if msg.Role == core.RoleTool {
			name := names[msg.ToolCallID]
			if name == "" {
				return nil, fmt.Errorf("Gemini tool result has no matching tool call")
			}
			response := map[string]any{"output": msg.Content}
			if text, ok := msg.Content.(string); ok {
				var object map[string]any
				if decodeJSON([]byte(text), &object) == nil && object != nil {
					response = object
				}
			}
			functionResponse := map[string]any{"name": name, "response": response}
			if id := nativeIDs[msg.ToolCallID]; id != "" {
				functionResponse["id"] = id
			}
			if _, ok := msg.Content.(string); !ok {
				var text []string
				var media []any
				for _, part := range parts {
					object := part.(map[string]any)
					if value, ok := object["text"].(string); ok {
						text = append(text, value)
					} else {
						media = append(media, part)
					}
				}
				functionResponse["response"] = map[string]any{"output": strings.Join(text, "\n")}
				if len(media) > 0 {
					functionResponse["parts"] = media
				}
			}
			parts = []any{map[string]any{"functionResponse": functionResponse}}
		} else {
			for _, tc := range msg.ToolCalls {
				args, err := arguments(tc.Function.Arguments)
				if err != nil {
					return nil, err
				}
				parts = append(parts, map[string]any{"functionCall": map[string]any{"id": tc.ID, "name": tc.Function.Name, "args": args}})
			}
		}
		if len(parts) == 0 {
			continue
		}
		if len(contents) > 0 && contents[len(contents)-1]["role"] == role {
			previous := contents[len(contents)-1]
			previous["parts"] = append(previous["parts"].([]any), parts...)
		} else {
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}
	payload := map[string]any{"contents": contents}
	if len(system) > 0 {
		payload["systemInstruction"] = map[string]any{"parts": system}
	}
	declarations := make([]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		declarations = append(declarations, map[string]any{"name": tool.Name, "description": tool.Description, "parametersJsonSchema": tool.Parameters})
	}
	if len(declarations) > 0 {
		payload["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
	}
	if req.MaxTokens > 0 {
		payload["generationConfig"] = map[string]any{"maxOutputTokens": req.MaxTokens}
	}
	if req.ToolChoice != "" {
		config := map[string]any{"mode": "AUTO"}
		if req.ToolChoice == "none" {
			config["mode"] = "NONE"
		} else if req.ToolChoice != "auto" {
			config["mode"] = "ANY"
			config["allowedFunctionNames"] = []string{req.ToolChoice}
		}
		payload["toolConfig"] = map[string]any{"functionCallingConfig": config}
	}
	return payload, nil
}

func decodeGemini(raw []byte, model string) (*core.ChatResponse, error) {
	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []json.RawMessage `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage *struct {
			Input    *int `json:"promptTokenCount"`
			Output   *int `json:"candidatesTokenCount"`
			Thoughts *int `json:"thoughtsTokenCount"`
			Total    *int `json:"totalTokenCount"`
			Cached   *int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode Gemini response: %w", err)
	}
	result := &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant}}
	if payload.Usage != nil {
		output := payload.Usage.Output
		if output != nil && payload.Usage.Thoughts != nil {
			value := *output + *payload.Usage.Thoughts
			output = &value
		}
		result.Usage = tokenUsage(payload.Usage.Input, output, payload.Usage.Total, payload.Usage.Cached)
	}
	if payload.Error != nil {
		return result, fmt.Errorf("API returned error: %s", payload.Error.Message)
	}
	if len(payload.Candidates) == 0 {
		return result, fmt.Errorf("no candidates in Gemini response")
	}
	candidate := payload.Candidates[0]
	if candidate.FinishReason != "" && candidate.FinishReason != "STOP" && candidate.FinishReason != "MAX_TOKENS" {
		return result, fmt.Errorf("Gemini response blocked: %s", candidate.FinishReason)
	}
	var text strings.Builder
	for _, rawPart := range candidate.Content.Parts {
		var part struct {
			Text         string `json:"text"`
			Thought      bool   `json:"thought"`
			FunctionCall *struct {
				ID   string         `json:"id"`
				Name string         `json:"name"`
				Args map[string]any `json:"args"`
			} `json:"functionCall"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(rawPart)))
		decoder.UseNumber()
		if err := decoder.Decode(&part); err != nil {
			return result, err
		}
		if !part.Thought {
			text.WriteString(part.Text)
		}
		if part.FunctionCall != nil {
			fc := part.FunctionCall
			if fc.ID == "" {
				fc.ID = "gemini_" + rand.Text()
			}
			if fc.Args == nil {
				fc.Args = map[string]any{}
			}
			tc, err := call(fc.ID, fc.Name, fc.Args)
			if err != nil {
				return result, err
			}
			result.Message.ToolCalls = append(result.Message.ToolCalls, tc)
		}
	}
	result.Message.Content = text.String()
	if text.Len() == 0 && len(result.Message.ToolCalls) == 0 {
		return result, fmt.Errorf("no message or tool calls in Gemini response")
	}
	rememberState(&result.Message, Gemini, model, candidate.Content.Parts)
	return result, nil
}

package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Format selects the wire protocol and URL convention for an endpoint.
type Format string

const (
	Legacy          Format = ""
	ChatCompletions Format = "openai_chat_completions"
	Responses       Format = "openai_responses"
	Anthropic       Format = "anthropic_messages"
	Gemini          Format = "gemini_generate_content"
	Custom          Format = "custom"
)

func (f Format) Valid() bool {
	switch f {
	case Legacy, ChatCompletions, Responses, Anthropic, Gemini, Custom:
		return true
	}
	return false
}

// URL preserves proxy prefixes and accepts an already versioned base URL.
// Custom endpoints are complete URLs, including any query string.
func (f Format) URL(base, operation, model string) (string, error) {
	if !f.Valid() {
		return "", fmt.Errorf("unknown API format %q", f)
	}
	if f == Custom {
		if operation == "models" {
			return "", fmt.Errorf("自定义端点不支持获取模型列表，请手动填写模型名称")
		}
		return base, nil
	}
	if f == Legacy {
		return url.JoinPath(base, operation)
	}
	version := "v1"
	if f == Gemini {
		version = "v1beta"
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	versioned := strings.HasSuffix(path, "/"+version)
	if operation == "chat/completions" {
		switch f {
		case Responses:
			operation = "responses"
		case Anthropic:
			operation = "messages"
		case Gemini:
			model = strings.TrimPrefix(model, "models/")
			if model == "" || strings.ContainsAny(model, "/?#\\") || model == "." || model == ".." {
				return "", fmt.Errorf("invalid Gemini model name")
			}
			operation = "models/" + model + ":generateContent"
		}
	}
	if operation == "embeddings" && (f == Anthropic || f == Gemini) {
		return "", fmt.Errorf("此 API 格式不支持 OpenAI Embedding，请使用独立的 OpenAI 兼容端点")
	}
	if versioned {
		return url.JoinPath(base, operation)
	}
	return url.JoinPath(base, version, operation)
}

func (f Format) Authorize(request *http.Request, key string) {
	switch f {
	case Anthropic:
		request.Header.Set("anthropic-version", "2023-06-01")
		if key != "" {
			request.Header.Set("x-api-key", key)
		}
	case Gemini:
		if key != "" {
			request.Header.Set("x-goog-api-key", key)
		}
	default:
		if key != "" {
			request.Header.Set("Authorization", "Bearer "+key)
		}
	}
}

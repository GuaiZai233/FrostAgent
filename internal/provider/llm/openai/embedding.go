package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"

	"FrostAgent/internal/core"
)

func (c *Client) Embed(ctx context.Context, req core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	if len(req.Input) == 0 {
		return nil, fmt.Errorf("embedding input is empty")
	}
	body, err := json.Marshal(struct {
		Model    string   `json:"model"`
		Input    []string `json:"input"`
		Encoding string   `json:"encoding_format"`
	}{req.Model, req.Input, "float"})
	if err != nil {
		return nil, err
	}
	endpoint, err := url.JoinPath(c.BaseURL, "embeddings")
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &HTTPError{Status: response.Status, StatusCode: response.StatusCode, Body: string(raw)}
	}
	var payload struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage *struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}
	if len(payload.Data) != len(req.Input) {
		return nil, fmt.Errorf("embedding result count does not match input")
	}
	result := &core.EmbeddingResponse{Vectors: make([][]float32, len(req.Input))}
	dimensions := 0
	for _, item := range payload.Data {
		if item.Index < 0 || item.Index >= len(result.Vectors) || result.Vectors[item.Index] != nil || len(item.Embedding) == 0 {
			return nil, fmt.Errorf("invalid embedding index or empty vector")
		}
		if dimensions == 0 {
			dimensions = len(item.Embedding)
		}
		if len(item.Embedding) != dimensions {
			return nil, fmt.Errorf("inconsistent embedding dimensions")
		}
		nonzero := false
		for _, value := range item.Embedding {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, fmt.Errorf("non-finite embedding value")
			}
			nonzero = nonzero || value != 0
		}
		if !nonzero {
			return nil, fmt.Errorf("zero embedding vector")
		}
		result.Vectors[item.Index] = item.Embedding
	}
	if payload.Usage != nil {
		result.Usage = &core.Usage{PromptTokens: payload.Usage.PromptTokens, TotalTokens: payload.Usage.TotalTokens}
	}
	return result, nil
}

// IsInputTooLong only recognizes an explicit upstream input-length rejection.
func IsInputTooLong(err error) bool {
	var e *HTTPError
	if !errors.As(err, &e) || (e.StatusCode != 400 && e.StatusCode != 413 && e.StatusCode != 422) {
		return false
	}
	body := strings.ToLower(e.Body)
	return e.StatusCode == 413 || strings.Contains(body, "context_length_exceeded") ||
		strings.Contains(body, "maximum context") || strings.Contains(body, "too many tokens") ||
		strings.Contains(body, "input is too long") || strings.Contains(body, "input exceeds") ||
		strings.Contains(body, "max input tokens")
}

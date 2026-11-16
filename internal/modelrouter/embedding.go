package modelrouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/openai"
)

var ErrInputTooLong = errors.New("memory input exceeds model window")

func limitMemoryChat(req core.ChatRequest, maxInput int) (core.ChatRequest, error) {
	if maxInput <= 0 {
		return req, nil
	}
	budget := maxInput - req.MaxTokens - 128
	if budget <= 0 {
		return req, ErrInputTooLong
	}
	messages := append([]core.ChatMessage(nil), req.Messages...)
	for i := range messages {
		text, ok := messages[i].Content.(string)
		if !ok {
			return req, ErrInputTooLong
		}
		text = core.LimitInput(text, budget)
		if text == "" {
			return req, ErrInputTooLong
		}
		messages[i].Content = text
		budget -= len(text)
	}
	req.Messages = messages
	return req, nil
}

func InputTooLong(err error) bool {
	return errors.Is(err, ErrInputTooLong) || openai.IsInputTooLong(err)
}

// EmbeddingSession captures the same target and credentials for index or query
// generation, so a concurrent route publication cannot mix vector spaces.
func (m *Manager) EmbeddingSession(ctx context.Context, scope Scope) (Target, core.EmbeddingProvider, error) {
	snapshot := snapshotFromContext(ctx)
	if snapshot == nil {
		snapshot = m.Snapshot()
	}
	target, err := snapshot.Resolve(WorkloadEmbedding, scope)
	if err != nil {
		return Target{}, nil, fmt.Errorf("Embedding 模型未配置或不可用: %w", err)
	}
	apiKey, err := snapshotAPIKey(m, snapshot, target)
	if err != nil {
		return Target{}, nil, err
	}
	client := api.NewClient(target.BaseURL, apiKey, api.Format(target.APIFormat), 120*time.Second)
	client.Logger = m.Log()
	client.ObserveUsage = m.usageObserver("embedding")
	return target, client, nil
}

func snapshotAPIKey(m *Manager, snapshot *Snapshot, target Target) (string, error) {
	if snapshot.credentials != nil {
		value := snapshot.credentials[target.EndpointID]
		return value.value, value.err
	}
	return resolveEndpointAPIKey(m.secrets, Endpoint{ID: target.EndpointID, DisplayName: target.EndpointDisplayName,
		APIKeySource: target.APIKeySource, APIKeyRef: target.APIKeyRef})
}

func (m *Manager) MemoryChat(ctx context.Context, route core.RouteContext, req core.ChatRequest) (*core.ChatResponse, error) {
	req.Route = route
	return m.Provider(WorkloadReflection, false, 120*time.Second).Chat(ctx, req)
}

func EmbeddingFingerprint(target Target) string {
	// Credentials and display names do not define the vector space.
	baseURL := strings.TrimRight(target.BaseURL, "/")
	routeKind := ""
	if format := api.Format(target.APIFormat); format != api.Legacy {
		endpoint, err := format.URL(target.BaseURL, "embeddings", target.UpstreamModel)
		if err != nil {
			routeKind = "\x00unsupported-embedding-format\x00" + target.APIFormat
		} else if parsed, err := url.Parse(endpoint); err == nil && strings.HasSuffix(parsed.EscapedPath(), "/embeddings") {
			// Express standard routes as their legacy base URL so equivalent
			// presets and exact URLs can reuse indexes written by older versions.
			parsed.Path = strings.TrimSuffix(parsed.Path, "/embeddings")
			parsed.RawPath = strings.TrimSuffix(parsed.RawPath, "/embeddings")
			baseURL = parsed.String()
		} else {
			// Other exact URLs must not collide with a legacy base that would
			// append /embeddings. Preserve trailing slashes and query parameters.
			baseURL, routeKind = endpoint, "\x00exact-embedding-url"
		}
	}
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%d%s", target.EndpointID,
		baseURL, target.UpstreamModel, target.MaxInputTokens, routeKind)
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

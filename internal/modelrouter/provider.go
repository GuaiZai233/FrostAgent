package modelrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"FrostAgent/internal/core"
	"FrostAgent/internal/provider/llm/openai"
)

type snapshotContextKey struct{}

type routedProvider struct {
	manager    *Manager
	workload   Workload
	globalOnly bool
	timeout    time.Duration
}

func (m *Manager) Provider(workload Workload, globalOnly bool, timeout time.Duration) core.LLMProvider {
	return &routedProvider{manager: m, workload: workload, globalOnly: globalOnly, timeout: timeout}
}

func (m *Manager) WithSnapshot(ctx context.Context, snapshot *Snapshot) context.Context {
	if snapshot == nil {
		snapshot = m.Snapshot()
	}
	return context.WithValue(ctx, snapshotContextKey{}, snapshot)
}

func snapshotFromContext(ctx context.Context) *Snapshot {
	if ctx == nil {
		return nil
	}
	snapshot, _ := ctx.Value(snapshotContextKey{}).(*Snapshot)
	return snapshot
}

func (p *routedProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	if p == nil || p.manager == nil {
		return nil, fmt.Errorf("model router is unavailable")
	}
	snapshot := snapshotFromContext(ctx)
	if snapshot == nil {
		snapshot = p.manager.Snapshot()
	}
	scope := Scope{Platform: req.Route.Platform, GroupID: req.Route.GroupID}
	if p.globalOnly {
		scope = Scope{}
	}
	target, err := snapshot.Resolve(p.workload, scope)
	if err != nil {
		return nil, err
	}
	apiKey, err := resolveEndpointAPIKey(p.manager.secrets, Endpoint{
		ID:           target.EndpointID,
		DisplayName:  target.EndpointDisplayName,
		APIKeySource: target.APIKeySource,
		APIKeyRef:    target.APIKeyRef,
	})
	if err != nil {
		return nil, fmt.Errorf("读取 Endpoint %q 的 API Key 失败: %w", target.EndpointDisplayName, err)
	}
	req.Model = target.UpstreamModel
	client := openai.NewClientWithTimeout(target.BaseURL, apiKey, p.timeout)
	return client.Chat(ctx, req)
}

func (m *Manager) ListUpstreamModels(endpointID string) ([]string, error) {
	cfg := m.Draft()
	endpoint, err := endpointByID(cfg, endpointID)
	if err != nil {
		return nil, err
	}
	apiKey, err := m.resolveDraftEndpointAPIKey(endpoint)
	if err != nil {
		return nil, fmt.Errorf("读取 Endpoint %q 的 API Key 失败: %w", endpoint.DisplayName, err)
	}
	fullURL, err := url.JoinPath(endpoint.BaseURL, "models")
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if len(body) > 0 {
			return nil, fmt.Errorf("%s", string(body))
		}
		return nil, fmt.Errorf("%s", response.Status)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if name := strings.TrimSpace(item.ID); name != "" {
			models = append(models, name)
		}
	}
	sort.Strings(models)
	return models, nil
}

func (m *Manager) TestModel(modelID string) (string, time.Duration, error) {
	cfg := m.Draft()
	model, err := modelByID(cfg, modelID)
	if err != nil {
		return "", 0, err
	}
	endpoint, err := endpointByID(cfg, model.EndpointID)
	if err != nil {
		return "", 0, err
	}
	apiKey, err := m.resolveDraftEndpointAPIKey(endpoint)
	if err != nil {
		return "", 0, fmt.Errorf("读取 Endpoint %q 的 API Key 失败: %w", endpoint.DisplayName, err)
	}
	client := openai.NewClient(endpoint.BaseURL, apiKey)
	started := time.Now()
	response, err := client.Chat(context.Background(), core.ChatRequest{
		Model: model.UpstreamModel,
		Messages: []core.ChatMessage{{
			Role:    core.RoleUser,
			Content: "Introduce yourself in one sentence.",
		}},
	})
	duration := time.Since(started)
	if err != nil {
		return "", duration, err
	}
	if content, ok := response.Message.Content.(string); ok {
		return content, duration, nil
	}
	body, _ := json.Marshal(response.Message.Content)
	return string(body), duration, nil
}

func (m *Manager) resolveDraftEndpointAPIKey(endpoint Endpoint) (string, error) {
	return m.secrets.ResolveDraft(endpoint)
}

func endpointByID(cfg Configuration, id string) (Endpoint, error) {
	for _, endpoint := range cfg.Endpoints {
		if endpoint.ID == id {
			return endpoint, nil
		}
	}
	return Endpoint{}, fmt.Errorf("endpoint %q does not exist", id)
}

func modelByID(cfg Configuration, id string) (Model, error) {
	for _, model := range cfg.Models {
		if model.ID == id {
			return model, nil
		}
	}
	return Model{}, fmt.Errorf("model %q does not exist", id)
}

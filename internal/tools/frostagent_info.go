package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FrostAgentInfoToolName is the read-only self-description tool exposed to the model.
const FrostAgentInfoToolName = "get_frostagent_info"

const (
	frostInfoSectionOverview = "overview"
	frostInfoSectionModel    = "model"
	frostInfoSectionFeatures = "features"
)

// Feature keys reported by the features section, in output order.
const (
	FeatureMemory          = "memory"
	FeatureVectorRetrieval = "vector_retrieval"
	FeatureSandbox         = "sandbox"
	FeatureMCP             = "mcp"
	FeatureActionsCat      = "actionscat"
	FeatureSticker         = "sticker"
)

var frostInfoFeatureOrder = []string{
	FeatureMemory,
	FeatureVectorRetrieval,
	FeatureSandbox,
	FeatureMCP,
	FeatureActionsCat,
	FeatureSticker,
}

// FrostAgentInfoSource supplies the live runtime facts reported by the tool.
// Every function is evaluated on each call. A nil function reports the fact as
// unknown or unavailable instead of panicking.
type FrostAgentInfoSource struct {
	BotName   func() string
	Aliases   func() []string
	Version   func() string
	StartedAt func() time.Time
	// Platforms returns the adapters with at least one live connection.
	Platforms func() []string
	// DialogueModel returns only the upstream model name of the dialogue workload.
	// Errors are reported to the model as a generic unavailable state.
	DialogueModel func(ctx context.Context) (string, error)
	// Features reports availability keyed by the Feature* constants.
	Features func() map[string]bool
	// Now is optional and exists so tests can fix the clock.
	Now func() time.Time
}

// FrostAgentInfoTool returns the read-only tool that exposes FrostAgent status.
// It never accepts configuration input from the model and never returns
// endpoints, credentials, or tokens.
func FrostAgentInfoTool(source FrostAgentInfoSource) Tool {
	return Tool{
		name:        FrostAgentInfoToolName,
		description: "查询 FrostAgent 自身的只读运行信息。section 可选：overview（Bot 名称、别名、FrostAgent 版本、运行时长、当前接入平台）、model（当前主对话模型的 Endpoint 模型名称）、features（记忆、向量检索、Sandbox、MCP、ActionsCat、表情包等功能是否可用）。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"section": map[string]any{
					"type":        "string",
					"enum":        []string{frostInfoSectionOverview, frostInfoSectionModel, frostInfoSectionFeatures},
					"description": "要查询的信息类别。",
				},
			},
			"required":             []string{"section"},
			"additionalProperties": false,
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			section, err := parseFrostInfoSection(args)
			if err != nil {
				return "", err
			}
			switch section {
			case frostInfoSectionOverview:
				return marshalFrostInfo(frostInfoOverview(source))
			case frostInfoSectionModel:
				return marshalFrostInfo(frostInfoModel(ctx, source))
			default:
				return marshalFrostInfo(frostInfoFeatures(source))
			}
		},
	}
}

func parseFrostInfoSection(args string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(args))
	decoder.DisallowUnknownFields()
	var input struct {
		Section string `json:"section"`
	}
	if err := decoder.Decode(&input); err != nil {
		return "", errors.New("get_frostagent_info 需要 section 参数，取值为 overview、model 或 features")
	}
	if decoder.More() {
		return "", errors.New("get_frostagent_info 参数格式无效")
	}
	switch input.Section {
	case frostInfoSectionOverview, frostInfoSectionModel, frostInfoSectionFeatures:
		return input.Section, nil
	default:
		return "", errors.New("get_frostagent_info 的 section 必须是 overview、model 或 features")
	}
}

type frostInfoOverviewResult struct {
	BotName       string   `json:"bot_name"`
	Aliases       []string `json:"aliases"`
	Version       string   `json:"version"`
	Uptime        string   `json:"uptime"`
	UptimeSeconds int64    `json:"uptime_seconds"`
	Platforms     []string `json:"platforms"`
}

func frostInfoOverview(source FrostAgentInfoSource) frostInfoOverviewResult {
	result := frostInfoOverviewResult{
		BotName:   "助手",
		Aliases:   []string{},
		Version:   "unknown",
		Uptime:    "未知",
		Platforms: []string{},
	}
	if source.BotName != nil {
		if name := strings.TrimSpace(source.BotName()); name != "" {
			result.BotName = name
		}
	}
	if source.Aliases != nil {
		for _, alias := range source.Aliases() {
			if alias = strings.TrimSpace(alias); alias != "" {
				result.Aliases = append(result.Aliases, alias)
			}
		}
	}
	if source.Version != nil {
		if version := strings.TrimSpace(source.Version()); version != "" {
			result.Version = version
		}
	}
	if source.StartedAt != nil {
		if started := source.StartedAt(); !started.IsZero() {
			now := time.Now()
			if source.Now != nil {
				now = source.Now()
			}
			if now.After(started) {
				elapsed := now.Sub(started)
				result.UptimeSeconds = int64(elapsed / time.Second)
				result.Uptime = formatFrostInfoUptime(elapsed)
			} else {
				result.Uptime = "0d 0h 0m"
			}
		}
	}
	if source.Platforms != nil {
		for _, platform := range source.Platforms() {
			if platform = strings.TrimSpace(platform); platform != "" {
				result.Platforms = append(result.Platforms, platform)
			}
		}
	}
	return result
}

func formatFrostInfoUptime(elapsed time.Duration) string {
	minutes := int64(elapsed / time.Minute)
	return fmt.Sprintf("%dd %dh %dm", minutes/(24*60), minutes/60%24, minutes%60)
}

type frostInfoModelResult struct {
	Available     bool   `json:"available"`
	UpstreamModel string `json:"upstream_model,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

func frostInfoModel(ctx context.Context, source FrostAgentInfoSource) frostInfoModelResult {
	if source.DialogueModel == nil {
		return frostInfoModelResult{Available: false, Reason: "主对话模型未配置"}
	}
	name, err := source.DialogueModel(ctx)
	name = strings.TrimSpace(name)
	if err != nil || name == "" {
		return frostInfoModelResult{Available: false, Reason: "主对话模型未配置或已禁用"}
	}
	return frostInfoModelResult{Available: true, UpstreamModel: name}
}

type frostInfoFeatureEntry struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

type frostInfoFeaturesResult struct {
	Features []frostInfoFeatureEntry `json:"features"`
}

func frostInfoFeatures(source FrostAgentInfoSource) frostInfoFeaturesResult {
	var flags map[string]bool
	if source.Features != nil {
		flags = source.Features()
	}
	entries := make([]frostInfoFeatureEntry, 0, len(frostInfoFeatureOrder))
	for _, name := range frostInfoFeatureOrder {
		entries = append(entries, frostInfoFeatureEntry{Name: name, Available: flags[name]})
	}
	return frostInfoFeaturesResult{Features: entries}
}

func marshalFrostInfo(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", errors.New("FrostAgent 信息序列化失败")
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

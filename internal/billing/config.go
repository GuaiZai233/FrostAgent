package billing

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"FrostAgent/internal/logs"
)

// Config holds runtime configuration for the Alcyone billing integration.
type Config struct {
	Enabled                     bool          `json:"enabled"`
	BaseURL                     string        `json:"base_url"`
	ServiceToken                string        `json:"service_token"`
	Timeout                     time.Duration `json:"timeout"`
	ModelName                   string        `json:"model_name"`
	MaxOutputTokens             int           `json:"max_output_tokens"`
	SafetyMultiplier            float64       `json:"safety_multiplier"`
	CustomPromptPriceMinor      *int64        `json:"custom_prompt_price_minor,omitempty"`
	CustomCompletionPriceMinor  *int64        `json:"custom_completion_price_minor,omitempty"`
}

const (
	DefaultAlcyoneBaseURL   = "http://127.0.0.1:8081"
	DefaultAlcyoneTimeout   = 5 * time.Second
	DefaultMaxOutputTokens  = 2048
	DefaultSafetyMultiplier = 1.2
	DefaultModelName        = "deepseek-chat"
)

// LoadConfigFromEnv reads billing settings from environment variables.
func LoadConfigFromEnv() Config {
	enabledStr := strings.ToLower(strings.TrimSpace(os.Getenv("BILLING_ENABLED")))
	enabled := enabledStr == "true" || enabledStr == "1" || enabledStr == "yes" || enabledStr == "on"

	baseURL := strings.TrimSpace(os.Getenv("ALCYONE_BASE_URL"))
	if baseURL == "" && enabled {
		baseURL = DefaultAlcyoneBaseURL
	}

	serviceToken := strings.TrimSpace(os.Getenv("ALCYONE_SERVICE_TOKEN"))

	timeout := DefaultAlcyoneTimeout
	if timeoutStr := strings.TrimSpace(os.Getenv("ALCYONE_TIMEOUT")); timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil && d > 0 {
			timeout = d
		}
	}

	modelName := strings.TrimSpace(os.Getenv("MODEL_NAME"))
	if modelName == "" {
		modelName = DefaultModelName
	}

	maxOutputTokens := DefaultMaxOutputTokens
	if maxOutputStr := strings.TrimSpace(os.Getenv("BILLING_MAX_OUTPUT_TOKENS")); maxOutputStr != "" {
		if val, err := strconv.Atoi(maxOutputStr); err == nil && val > 0 {
			maxOutputTokens = val
		}
	}

	safetyMultiplier := DefaultSafetyMultiplier
	if safetyStr := strings.TrimSpace(os.Getenv("BILLING_SAFETY_MULTIPLIER")); safetyStr != "" {
		if val, err := strconv.ParseFloat(safetyStr, 64); err == nil && val > 0 {
			safetyMultiplier = val
		}
	}

	var customPromptPrice *int64
	if pStr := strings.TrimSpace(os.Getenv("BILLING_PROMPT_PRICE_PER_MILLION")); pStr != "" {
		if val, err := strconv.ParseInt(pStr, 10, 64); err == nil && val >= 0 {
			customPromptPrice = &val
		}
	}

	var customCompletionPrice *int64
	if cStr := strings.TrimSpace(os.Getenv("BILLING_COMPLETION_PRICE_PER_MILLION")); cStr != "" {
		if val, err := strconv.ParseInt(cStr, 10, 64); err == nil && val >= 0 {
			customCompletionPrice = &val
		}
	}

	return Config{
		Enabled:                    enabled,
		BaseURL:                    baseURL,
		ServiceToken:               serviceToken,
		Timeout:                    timeout,
		ModelName:                  modelName,
		MaxOutputTokens:            maxOutputTokens,
		SafetyMultiplier:           safetyMultiplier,
		CustomPromptPriceMinor:     customPromptPrice,
		CustomCompletionPriceMinor: customCompletionPrice,
	}
}

// InitBillingClient validates config, checks model support in price tables,
// logs relevant warnings or info, and constructs the client.
func InitBillingClient(cfg Config) (*Client, error) {
	if !cfg.Enabled {
		logs.Info(logs.SYSTEM, "💳 计费系统未启用 (BILLING_ENABLED!=true)")
		return nil, nil
	}

	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("billing is enabled but ALCYONE_BASE_URL is empty")
	}

	// Apply custom price override from environment variables if specified
	if cfg.CustomPromptPriceMinor != nil || cfg.CustomCompletionPriceMinor != nil {
		currentPrice, _ := GetPrice(cfg.ModelName)
		if cfg.CustomPromptPriceMinor != nil {
			currentPrice.PromptPricePerMillion = *cfg.CustomPromptPriceMinor
		}
		if cfg.CustomCompletionPriceMinor != nil {
			currentPrice.CompletionPricePerMillion = *cfg.CustomCompletionPriceMinor
		}
		RegisterPrice(cfg.ModelName, currentPrice)
		logs.Info(logs.SYSTEM, fmt.Sprintf(
			"💳 计费系统已应用自定义模型价格: %s (输入 %s 片/1M, 输出 %s 片/1M)",
			cfg.ModelName,
			FormatSnowflakes(currentPrice.PromptPricePerMillion),
			FormatSnowflakes(currentPrice.CompletionPricePerMillion),
		))
	} else {
		price, registered := GetPrice(cfg.ModelName)
		if !registered {
			logs.Warn(logs.SYSTEM, fmt.Sprintf(
				"⚠️ 计费系统：模型 %q 未在官方价格表中注册，将使用默认兜底价格 (输入 %s 片/1M, 输出 %s 片/1M)",
				cfg.ModelName,
				FormatSnowflakes(price.PromptPricePerMillion),
				FormatSnowflakes(price.CompletionPricePerMillion),
			))
		} else {
			logs.Info(logs.SYSTEM, fmt.Sprintf(
				"💳 计费系统模型价格: %s (输入 %s 片/1M, 输出 %s 片/1M)",
				cfg.ModelName,
				FormatSnowflakes(price.PromptPricePerMillion),
				FormatSnowflakes(price.CompletionPricePerMillion),
			))
		}
	}

	client := NewClient(cfg.BaseURL, cfg.ServiceToken, cfg.Timeout)
	logs.Info(logs.SYSTEM, fmt.Sprintf(
		"💳 计费系统已就绪: 地址=%s, 模型=%s, 预留安全倍率=%.2f, 最大输出Token=%d",
		cfg.BaseURL, cfg.ModelName, cfg.SafetyMultiplier, cfg.MaxOutputTokens,
	))

	return client, nil
}

// FormatSnowflakes converts minor units (hundredths) to a human-readable decimal snowflake string.
func FormatSnowflakes(minor int64) string {
	isNeg := minor < 0
	absVal := minor
	if isNeg {
		absVal = -minor
	}
	whole := absVal / 100
	cents := absVal % 100

	if isNeg {
		return fmt.Sprintf("-%d.%02d", whole, cents)
	}
	return fmt.Sprintf("%d.%02d", whole, cents)
}

// Package balancequery owns administrator-configured balance requests and secrets.
package balancequery

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
)

const (
	ToolName       = "balance_query"
	ConfigKey      = "BALANCE_QUERY_CONFIG"
	KeyPlaceholder = "{{API_KEY}}"
	DefaultCurl    = "curl https://api.deepseek.com/user/balance \\\n  -H 'Authorization: Bearer {{API_KEY}}'"
)

type Config struct {
	Curl       string `json:"curl"`
	APIKey     string `json:"api_key,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	// Keyless marks a template saved without any credential (a custom endpoint that needs none).
	// It is set only by saving a blank key with no stored key or provider; clearing a key never sets it.
	Keyless bool `json:"keyless,omitempty"`
	// QuotaPerUnit converts New API quota fields to dollars; 0 disables conversion.
	QuotaPerUnit float64 `json:"quota_per_unit,omitempty"`
	// NewAPI marks a New API template whose responses must be validated as quota payloads.
	NewAPI bool `json:"newapi,omitempty"`
	// USDExchangeRate and QuotaDisplayType come from the New API status and are kept with the template.
	USDExchangeRate  float64 `json:"usd_exchange_rate,omitempty"`
	QuotaDisplayType string  `json:"quota_display_type,omitempty"`
	// CustomCurrencySymbol and CustomCurrencyExchangeRate apply when QuotaDisplayType is CUSTOM.
	CustomCurrencySymbol       string  `json:"custom_currency_symbol,omitempty"`
	CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate,omitempty"`
}

// ProviderCredential is a provider's key with its base URL, read atomically from one published endpoint.
type ProviderCredential struct {
	APIKey  string
	BaseURL string
}

// KeyResolver resolves a provider credential for backend use only.
type KeyResolver func(providerID string) (ProviderCredential, error)

func Load(get func(string) string) (Config, error) {
	cfg := Config{Curl: DefaultCurl}
	if get != nil {
		if raw := get(ConfigKey); raw != "" {
			if json.Unmarshal([]byte(raw), &cfg) != nil {
				return Config{}, errors.New("额度查询配置无法解析")
			}
		}
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if math.IsNaN(c.USDExchangeRate) || math.IsInf(c.USDExchangeRate, 0) || c.USDExchangeRate < 0 ||
		math.IsNaN(c.CustomCurrencyExchangeRate) || math.IsInf(c.CustomCurrencyExchangeRate, 0) || c.CustomCurrencyExchangeRate < 0 {
		return errors.New("汇率必须是有限的非负数")
	}
	if len(c.APIKey) > 8192 || strings.TrimSpace(c.APIKey) != c.APIKey || strings.ContainsAny(c.APIKey, "\r\n\x00") {
		return errors.New("API key 过长或含有非法字符")
	}
	if c.APIKey != "" && strings.Contains(c.Curl, c.APIKey) {
		return errors.New("请将 API key 放在独立字段中，curl 使用 {{API_KEY}} 占位")
	}
	_, err := parseCurl(c.Curl)
	return err
}

// LoadResolved reads a provider credential for this request without persisting it.
func LoadResolved(get func(string) string, resolve KeyResolver) (Config, error) {
	cfg, err := Load(get)
	if err != nil {
		return Config{}, err
	}
	if cfg.ProviderID != "" {
		if resolve == nil {
			return Config{}, errors.New("额度查询 Provider 不可用，请联系管理员")
		}
		credential, err := resolve(cfg.ProviderID)
		if err != nil || credential.APIKey == "" {
			return Config{}, errors.New("额度查询 Provider 的 API Key 不可用，请联系管理员")
		}
		// The provider key is only ever sent to the provider's current origin.
		if !SameOrigin(cfg.Curl, credential.BaseURL) {
			return Config{}, errors.New("额度查询请求地址与 Provider 端点不一致，请重新选择 Provider 或填写 Key")
		}
		cfg.APIKey, cfg.ProviderID = credential.APIKey, ""
	}
	return cfg, nil
}

func Configured(get func(string) string, resolvers ...KeyResolver) bool {
	var resolve KeyResolver
	if len(resolvers) > 0 {
		resolve = resolvers[0]
	}
	cfg, err := LoadResolved(get, resolve)
	return err == nil && (cfg.APIKey != "" || cfg.Keyless) && cfg.Validate() == nil
}

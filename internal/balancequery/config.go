// Package balancequery owns administrator-configured balance requests and secrets.
package balancequery

import (
	"encoding/json"
	"errors"
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
}

type KeyResolver func(providerID string) (string, error)

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
		key, err := resolve(cfg.ProviderID)
		if err != nil || key == "" {
			return Config{}, errors.New("额度查询 Provider 的 API Key 不可用，请联系管理员")
		}
		cfg.APIKey, cfg.ProviderID = key, ""
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

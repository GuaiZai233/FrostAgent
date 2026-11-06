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
	Curl   string `json:"curl"`
	APIKey string `json:"api_key"`
}

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

func Configured(get func(string) string) bool {
	cfg, err := Load(get)
	return err == nil && cfg.APIKey != "" && cfg.Validate() == nil
}

package balancequery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxResponseBytes = 60 * 1024

var queryClient = newQueryClient()

func newQueryClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 32 * 1024
	return &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Execute substitutes credentials only at the outbound HTTP boundary. No URL,
// request headers, transport errors or secret-bearing responses reach the model.
func Execute(ctx context.Context, cfg Config) (string, error) {
	if cfg.APIKey == "" {
		return "", errors.New("额度查询尚未配置 API key，请联系管理员")
	}
	if err := cfg.Validate(); err != nil {
		return "", errors.New("额度查询配置无效，请联系管理员")
	}
	spec, _ := parseCurl(cfg.Curl)
	spec.url.RawQuery = strings.ReplaceAll(spec.url.RawQuery, KeyPlaceholder, url.QueryEscape(cfg.APIKey))
	req, err := http.NewRequestWithContext(ctx, spec.method, spec.url.String(), strings.NewReader(strings.ReplaceAll(spec.body, KeyPlaceholder, cfg.APIKey)))
	if err != nil {
		return "", errors.New("额度查询请求无效")
	}
	for name, values := range spec.headers {
		for _, value := range values {
			req.Header.Add(name, strings.ReplaceAll(value, KeyPlaceholder, cfg.APIKey))
		}
	}
	resp, err := queryClient.Do(req)
	if err != nil {
		return "", errors.New("额度查询请求失败或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return "", errors.New("额度查询不允许重定向")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return "", errors.New("读取额度查询结果失败")
	}
	if len(data) > MaxResponseBytes {
		return "", errors.New("额度查询结果超出大小限制")
	}
	if !utf8.Valid(data) {
		return "", errors.New("额度查询结果不是有效文本")
	}
	result := string(data)
	if containsSecret(result, cfg.APIKey) {
		return "", errors.New("额度查询结果包含 API key，已阻止返回")
	}
	return result, nil
}

func containsSecret(body, key string) bool {
	encodedJSON, _ := json.Marshal(key)
	for _, variant := range []string{key, url.QueryEscape(key), url.PathEscape(key), string(encodedJSON[1 : len(encodedJSON)-1]), base64.StdEncoding.EncodeToString([]byte(key)), base64.RawStdEncoding.EncodeToString([]byte(key)), base64.URLEncoding.EncodeToString([]byte(key)), base64.RawURLEncoding.EncodeToString([]byte(key))} {
		if strings.Contains(body, variant) {
			return true
		}
	}
	var decoded any
	if json.Unmarshal([]byte(body), &decoded) == nil {
		return jsonContainsSecret(decoded, key)
	}
	return false
}

func jsonContainsSecret(value any, key string) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, key)
	case []any:
		for _, item := range v {
			if jsonContainsSecret(item, key) {
				return true
			}
		}
	case map[string]any:
		for name, item := range v {
			if strings.Contains(name, key) || jsonContainsSecret(item, key) {
				return true
			}
		}
	}
	return false
}
